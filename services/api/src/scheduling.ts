import type { FastifyInstance, FastifyRequest } from "fastify";
import type pg from "pg";
import { z } from "zod";
import type { Principal } from "./auth.ts";
import { HttpError, requireRole } from "./auth.ts";
import { lockCompute } from "./compute-maintenance.ts";

type JobRow = {
  id: string;
  payload: Record<string, unknown>;
  timeout_seconds: number;
};
type SessionRow = { id: string; expires_at: Date };
export async function reserveJob(
  c: pg.PoolClient,
  job: JobRow,
  device: string,
  session: SessionRow,
  trace: string,
) {
  const a = (
    await c.query(
      "INSERT INTO job_attempts(job_id,device_id,session_id,epoch,state,lease_expires_at) VALUES($1,$2,$3,(SELECT COALESCE(max(epoch),0)+1 FROM job_attempts WHERE job_id=$1),'OFFERED',LEAST($4::timestamptz,clock_timestamp()+interval '30 seconds')) RETURNING *",
      [job.id, device, session.id, session.expires_at],
    )
  ).rows[0];
  await c.query("UPDATE jobs SET state='RUNNING' WHERE id=$1", [job.id]);
  await c.query(
    "INSERT INTO job_events(job_id,attempt_id,state,trace_id) VALUES($1,$2,'OFFERED',$3)",
    [job.id, a.id, trace],
  );
  return {
    attempt_id: a.id,
    job_id: job.id,
    session_id: session.id,
    epoch: a.epoch,
    lease_expires_at: a.lease_expires_at,
    ...job.payload,
    timeout_seconds: job.timeout_seconds,
    workload_type: "synthetic-hash-v1",
  };
}
export async function requestedOffer(
  c: pg.PoolClient,
  device: string,
  session: string,
) {
  const a = (
    await c.query(
      "SELECT a.id AS attempt_id,a.job_id,a.session_id,a.epoch,a.lease_expires_at,j.payload,j.timeout_seconds,j.workload_type FROM job_attempts a JOIN jobs j ON j.id=a.job_id WHERE a.device_id=$1 AND a.session_id=$2 AND a.state='OFFERED' AND j.state='RUNNING' AND a.lease_expires_at>clock_timestamp()",
      [device, session],
    )
  ).rows[0];
  if (!a) return undefined;
  const { payload, ...offer } = a;
  return { ...offer, ...payload };
}
// Both candidates and claims use the same live eligibility predicate. Snapshot
// decisions are proposals; only the locked transaction may reserve capacity.
const eligible = `s.status='ACTIVE' AND s.expires_at>clock_timestamp()+interval '6 seconds' AND d.revoked_at IS NULL
 AND r.expires_at>clock_timestamp() AND d.capabilities->'runtimes' ? 'synthetic-hash-v1'
 AND (d.capabilities->>'cpu_cores')::integer>=1 AND (d.capabilities->>'memory_mb')::integer>=64
 AND (s.policy_snapshot->>'memory_mb')::integer>=64
 AND NOT EXISTS(SELECT 1 FROM job_attempts a WHERE a.device_id=d.id AND a.state IN ('OFFERED','ACCEPTED','RUNNING'))`;
const devicesFrom =
  "device_work_requests r JOIN devices d ON d.id=r.device_id JOIN availability_sessions s ON s.id=r.session_id AND s.device_id=d.id";
const claimSchema = z
  .object({
    job_id: z.string().uuid(),
    device_id: z.string().uuid(),
    session_id: z.string().uuid(),
    request_revision: z.number().int().positive(),
  })
  .strict();
export function registerScheduler(
  app: FastifyInstance,
  pool: pg.Pool,
  authenticate: (r: FastifyRequest) => Promise<Principal>,
) {
  async function authorize(r: FastifyRequest) {
    requireRole(await authenticate(r), ["scheduler_service"]);
  }
  app.get("/internal/scheduler/candidates", async (r) => {
    await authorize(r);
    // Current workload requirements are uniformly one CPU / 64 MiB. Keeping
    // the longest remaining sessions guarantees the bounded device window
    // contains a match whenever one exists; filter jobs before their FIFO limit.
    const { rows } = await pool.query(`WITH devices AS MATERIALIZED (
      SELECT d.id AS device_id,s.id AS session_id,r.revision,r.requested_at,1 AS cpu_cores,
      LEAST((d.capabilities->>'memory_mb')::integer,(s.policy_snapshot->>'memory_mb')::integer) AS memory_mb,
      extract(epoch FROM s.expires_at-clock_timestamp())::float AS remaining_seconds
      FROM ${devicesFrom} WHERE ${eligible}
      ORDER BY remaining_seconds DESC,r.requested_at,d.id LIMIT 128
    ), jobs AS (
      SELECT j.id,j.created_at,j.timeout_seconds,1 AS cpu_cores,64 AS memory_mb
      FROM jobs j WHERE j.state='QUEUED' AND j.next_eligible_at<=clock_timestamp() AND EXISTS(SELECT 1 FROM devices d WHERE d.remaining_seconds>j.timeout_seconds+5)
      ORDER BY j.created_at,j.id LIMIT 128
    ) SELECT COALESCE((SELECT json_agg(j ORDER BY j.created_at,j.id) FROM jobs j),'[]'::json) AS jobs,
      COALESCE((SELECT json_agg(d) FROM devices d),'[]'::json) AS devices`);
    return rows[0];
  });
  app.post("/internal/scheduler/claims", async (r, reply) => {
    await authorize(r);
    const b = claimSchema.parse(r.body);
    const c = await pool.connect();
    try {
      await c.query("BEGIN");
      await lockCompute(c);
      const session = (
        await c.query(
          `SELECT s.id,s.expires_at FROM ${devicesFrom} WHERE ${eligible} AND d.id=$1 AND s.id=$2 AND r.revision=$3 FOR UPDATE OF d,s,r`,
          [b.device_id, b.session_id, b.request_revision],
        )
      ).rows[0];
      if (!session)
        throw new HttpError(409, "Work request is no longer eligible");
      const job = (
        await c.query(
          "SELECT * FROM jobs WHERE id=$1 AND state='QUEUED' AND next_eligible_at<=clock_timestamp() AND timeout_seconds+5<extract(epoch FROM ($2::timestamptz-clock_timestamp())) FOR UPDATE",
          [b.job_id, session.expires_at],
        )
      ).rows[0];
      if (!job) throw new HttpError(409, "Job is no longer eligible");
      const offer = await reserveJob(c, job, b.device_id, session, r.id);
      await c.query("DELETE FROM device_work_requests WHERE device_id=$1", [
        b.device_id,
      ]);
      await c.query("COMMIT");
      return reply.code(201).send({ attempt_id: offer.attempt_id });
    } catch (e) {
      await c.query("ROLLBACK");
      throw e;
    } finally {
      c.release();
    }
  });
}
