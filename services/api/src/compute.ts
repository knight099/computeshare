import { transitionFailedJob } from "./recovery.ts";
import type { FastifyInstance, FastifyRequest } from "fastify";
import type pg from "pg";
import { recordUsage, registerUsage } from "./usage.ts";
import { reserveJob, requestedOffer } from "./scheduling.ts";
import { HashVerifier } from "./verifier.ts";
import { registerJobPages } from "./job-pages.ts";
import { z } from "zod";
import type { ControlMessage } from "../../../packages/protocol/src/index.ts";
import { HttpError, requireRole } from "./auth.ts";
import { lockCompute } from "./compute-maintenance.ts";

export const policySchema = z
  .object({
    cpu_percent: z.number().int().min(10).max(100),
    memory_mb: z.number().int().min(64).max(1024),
    gpu_enabled: z.literal(false),
    idle_only: z.literal(false),
  })
  .strict();
const startSchema = z
  .object({
    device_id: z.string().uuid(),
    duration_seconds: z.union([
      z.literal(3600),
      z.literal(7200),
      z.literal(14400),
      z.literal(21600),
      z.literal(28800),
      z.literal(43200),
    ]),
    policy: policySchema,
    consent_version: z.literal("sharing-v1"),
    accepted: z.literal(true),
  })
  .strict();
const jobSchema = z
  .object({
    idempotency_key: z.string().min(1).max(100),
    workload_type: z.literal("synthetic-hash-v1"),
    seed: z
      .string()
      .min(1)
      .max(64)
      .regex(/^[a-zA-Z0-9 _.-]+$/),
    iterations: z.number().int().min(1).max(1000000),
    max_attempts: z.number().int().min(1).max(3).default(1),
    timeout_seconds: z.number().int().min(1).max(30).default(10),
  })
  .strict();
type User = { id: string; subject: string; roles: string[] };
type Auth = {
  pool: pg.Pool;
  user: (r: FastifyRequest) => Promise<User>;
  externalScheduler: () => boolean;
  deviceAuth: (r: FastifyRequest) => Promise<{ id: string }>;
};

export function registerCompute(app: FastifyInstance, auth: Auth) {
  const { pool } = auth;
  const verifier = new HashVerifier();
  app.addHook("onClose", () => verifier.close());
  registerJobPages(app, pool, auth.user);
  registerUsage(app, pool, auth.user);
  async function event(
    c: pg.PoolClient,
    job: string,
    attempt: string | null,
    state: string,
    trace: string,
  ) {
    await c.query(
      "INSERT INTO job_events(job_id,attempt_id,state,trace_id) VALUES($1,$2,$3,$4)",
      [job, attempt, state, trace],
    );
  }
  async function stop(
    c: pg.PoolClient,
    id: string,
    device: string,
    status = "STOPPED",
  ) {
    const s = await c.query(
      "UPDATE availability_sessions SET status=$3,stopped_at=COALESCE(stopped_at,now()) WHERE id=$1 AND device_id=$2 AND status='ACTIVE' RETURNING id",
      [id, device, status],
    );
    if (s.rowCount) {
      const attempts = await c.query(
        "UPDATE job_attempts SET state='CANCELED',finished_at=now() WHERE session_id=$1 AND state IN ('OFFERED','ACCEPTED','RUNNING') RETURNING id,job_id,epoch",
        [id],
      );
      for (const a of attempts.rows) {
        await transitionFailedJob(
          c,
          a.job_id,
          a.epoch,
          "CANCELED",
          `session:${id}`,
        );
        await event(c, a.job_id, a.id, "CANCELED", `session:${id}`);
      }
      await c.query(
        "INSERT INTO audit_events(actor_id,action,object_id,trace_id) VALUES($1,$2,$3,$4)",
        [
          device,
          status === "EXPIRED" ? "SESSION_EXPIRED" : "SESSION_STOPPED",
          id,
          `session:${id}`,
        ],
      );
    }
  }
  async function start(r: FastifyRequest, deviceMode: boolean) {
    const b = startSchema.parse(r.body);
    let owner: string | undefined;
    if (deviceMode) {
      if ((await auth.deviceAuth(r)).id !== b.device_id)
        throw new HttpError(404, "Device not found");
    } else {
      const u = await auth.user(r);
      requireRole(u, ["contributor"]);
      owner = u.id;
    }
    const c = await pool.connect();
    try {
      await c.query("BEGIN");
      await lockCompute(c);
      const d = await c.query(
        "SELECT id FROM devices WHERE id=$1 AND revoked_at IS NULL AND ($2::uuid IS NULL OR owner_id=$2) FOR UPDATE",
        [b.device_id, owner ?? null],
      );
      if (!d.rowCount) throw new HttpError(404, "Device not found");
      const previous = await c.query(
        "SELECT id FROM availability_sessions WHERE device_id=$1 AND status='ACTIVE'",
        [b.device_id],
      );
      for (const row of previous.rows) await stop(c, row.id, b.device_id);
      const { rows } = await c.query(
        "INSERT INTO availability_sessions(device_id,starts_at,expires_at,requested_duration_seconds,status,policy_snapshot,consent_version) VALUES($1,statement_timestamp(),statement_timestamp()+$2*interval '1 second',$2,'ACTIVE',$3,$4) RETURNING *",
        [b.device_id, b.duration_seconds, b.policy, b.consent_version],
      );
      await c.query(
        "INSERT INTO audit_events(actor_id,action,object_id,trace_id) VALUES($1,'SHARING_CONSENT_ACCEPTED',$2,$3)",
        [owner ?? b.device_id, rows[0].id, r.id],
      );
      await c.query("COMMIT");
      return rows[0];
    } catch (e) {
      await c.query("ROLLBACK");
      throw e;
    } finally {
      c.release();
    }
  }
  for (const [url, deviceMode] of [
    ["/v1/availability-sessions", false],
    ["/v1/agent/availability-sessions", true],
  ] as const) {
    app.post(url, async (r, reply) =>
      reply.code(201).send(await start(r, deviceMode)),
    );
    app.post(url + "/:id/stop", async (r) => {
      const id = z
        .string()
        .uuid()
        .parse((r.params as { id: string }).id);
      const u = deviceMode ? undefined : await auth.user(r);
      const d = deviceMode ? await auth.deviceAuth(r) : undefined;
      const c = await pool.connect();
      try {
        await c.query("BEGIN");
        await lockCompute(c);
        const q = await c.query(
          "SELECT s.device_id FROM availability_sessions s JOIN devices d ON d.id=s.device_id WHERE s.id=$1 AND ($2::uuid IS NULL OR d.owner_id=$2) AND ($3::uuid IS NULL OR d.id=$3) FOR UPDATE OF d",
          [id, u?.id ?? null, d?.id ?? null],
        );
        if (!q.rowCount) throw new HttpError(404, "Session not found");
        await stop(c, id, q.rows[0].device_id);
        await c.query("COMMIT");
        return { stopped: true };
      } catch (e) {
        await c.query("ROLLBACK");
        throw e;
      } finally {
        c.release();
      }
    });
  }
  app.get("/v1/availability-sessions/:id", async (r) => {
    const u = await auth.user(r);
    const id = z
      .string()
      .uuid()
      .parse((r.params as { id: string }).id);
    const { rows } = await pool.query(
      "SELECT s.*,CASE WHEN s.status='ACTIVE' AND s.expires_at<=clock_timestamp() THEN 'EXPIRED' ELSE s.status END AS status FROM availability_sessions s JOIN devices d ON d.id=s.device_id WHERE s.id=$1 AND d.owner_id=$2",
      [id, u.id],
    );
    if (!rows[0]) throw new HttpError(404, "Session not found");
    return rows[0];
  });
  app.post("/v1/jobs", async (r, reply) => {
    const u = await auth.user(r);
    requireRole(u, ["operator_admin", "workload_submitter"]);
    const b = jobSchema.parse(r.body);
    const payload = { seed: b.seed, iterations: b.iterations };
    const known = (
      await pool.query(
        "SELECT * FROM jobs WHERE submitter_id=$1 AND idempotency_key=$2",
        [u.id, b.idempotency_key],
      )
    ).rows[0];
    if (known) {
      if (
        known.payload.seed !== b.seed ||
        known.payload.iterations !== b.iterations ||
        known.timeout_seconds !== b.timeout_seconds ||
        known.max_attempts !== b.max_attempts
      )
        throw new HttpError(
          409,
          "Idempotency key already used for different input",
        );
      return reply.code(200).send(known);
    }
    const expected = await verifier.compute(b.seed, b.iterations);
    const c = await pool.connect();
    try {
      await c.query("BEGIN");
      await lockCompute(c);
      let { rows } = await c.query(
        "INSERT INTO jobs(submitter_id,idempotency_key,workload_type,payload,timeout_seconds,state,expected_hash,max_attempts) VALUES($1,$2,$3,$4,$5,'QUEUED',$6,$7) ON CONFLICT(submitter_id,idempotency_key) DO NOTHING RETURNING *",
        [
          u.id,
          b.idempotency_key,
          b.workload_type,
          payload,
          b.timeout_seconds,
          expected,
          b.max_attempts,
        ],
      );
      const created = Boolean(rows.length);
      if (!created) {
        rows = (
          await c.query(
            "SELECT * FROM jobs WHERE submitter_id=$1 AND idempotency_key=$2",
            [u.id, b.idempotency_key],
          )
        ).rows;
        if (
          rows[0].payload.seed !== b.seed ||
          rows[0].payload.iterations !== b.iterations ||
          rows[0].timeout_seconds !== b.timeout_seconds ||
          rows[0].max_attempts !== b.max_attempts
        )
          throw new HttpError(
            409,
            "Idempotency key already used for different input",
          );
      } else await event(c, rows[0].id, null, "QUEUED", r.id);
      await c.query("COMMIT");
      return reply.code(created ? 201 : 200).send(rows[0]);
    } catch (e) {
      await c.query("ROLLBACK");
      throw e;
    } finally {
      c.release();
    }
  });
  app.get("/v1/jobs/:id", async (r) => {
    const u = await auth.user(r);
    const id = z
      .string()
      .uuid()
      .parse((r.params as { id: string }).id);
    const { rows } = await pool.query(
      "SELECT * FROM jobs WHERE id=$1 AND ($2 OR submitter_id=$3)",
      [
        id,
        u.roles.some((r) =>
          ["operator_admin", "operator_support", "auditor"].includes(r),
        ),
        u.id,
      ],
    );
    if (!rows[0]) throw new HttpError(404, "Job not found");
    return {
      ...rows[0],
      attempts: (
        await pool.query(
          "SELECT id,device_id,state,epoch,started_at,finished_at,result_hash FROM job_attempts WHERE job_id=$1 ORDER BY epoch",
          [id],
        )
      ).rows,
    };
  });
  app.post("/v1/jobs/:id/cancel", async (r) => {
    const u = await auth.user(r);
    requireRole(u, ["operator_admin", "workload_submitter"]);
    const id = z
      .string()
      .uuid()
      .parse((r.params as { id: string }).id);
    const c = await pool.connect();
    try {
      await c.query("BEGIN");
      await lockCompute(c);
      const q = await c.query(
        "UPDATE jobs SET state='CANCELED' WHERE id=$1 AND ($2 OR submitter_id=$3) AND state IN ('QUEUED','RUNNING') RETURNING id",
        [id, u.roles.includes("operator_admin"), u.id],
      );
      if (!q.rowCount)
        throw new HttpError(409, "Job unavailable or already terminal");
      await c.query(
        "UPDATE job_attempts SET state='CANCELED',finished_at=now() WHERE job_id=$1 AND state IN ('OFFERED','ACCEPTED','RUNNING')",
        [id],
      );
      await event(c, id, null, "CANCELED", r.id);
      await c.query("COMMIT");
      return { canceled: true };
    } catch (e) {
      await c.query("ROLLBACK");
      throw e;
    } finally {
      c.release();
    }
  });

  async function control(
    device: string,
    m: ControlMessage,
  ): Promise<Record<string, unknown>> {
    let legacyExpected: string | undefined;
    if (m.type === "JOB_COMPLETE" && m.attempt) {
      const prior = (
        await pool.query(
          "SELECT j.payload,j.expected_hash FROM job_attempts a JOIN jobs j ON j.id=a.job_id JOIN availability_sessions s ON s.id=a.session_id WHERE a.id=$1 AND a.device_id=$2 AND a.epoch=$3 AND a.state IN ('OFFERED','ACCEPTED','RUNNING') AND j.state='RUNNING' AND a.lease_expires_at>clock_timestamp() AND s.status='ACTIVE' AND s.expires_at>clock_timestamp()",
          [m.attempt.id, device, m.attempt.epoch],
        )
      ).rows[0];
      if (prior && !prior.expected_hash)
        legacyExpected = await verifier.compute(
          prior.payload.seed,
          prior.payload.iterations,
        );
    }
    const c = await pool.connect();
    try {
      await c.query("BEGIN");
      await lockCompute(c);
      // Serialize scheduling and session Stop using the same device lock order.
      await c.query("SELECT id FROM devices WHERE id=$1 FOR UPDATE", [device]);
      const expired = await c.query(
        "SELECT id FROM availability_sessions WHERE device_id=$1 AND status='ACTIVE' AND expires_at<=clock_timestamp()",
        [device],
      );
      for (const s of expired.rows) await stop(c, s.id, device, "EXPIRED");
      const lost = await c.query(
        "UPDATE job_attempts SET state='LOST',finished_at=now() WHERE device_id=$1 AND lease_expires_at<=clock_timestamp() AND state IN ('OFFERED','ACCEPTED','RUNNING') RETURNING job_id,id,epoch",
        [device],
      );
      for (const a of lost.rows) {
        await transitionFailedJob(c, a.job_id, a.epoch, "FAILED", m.trace_id);
        await event(c, a.job_id, a.id, "LOST", m.trace_id);
      }
      if (m.session && m.session.status !== "ACTIVE")
        await stop(c, m.session.id, device, m.session.status);
      let response: Record<string, unknown> = {};
      if (m.attempt) {
        const a = (
          await c.query(
            "SELECT a.*,j.payload,j.expected_hash,j.state AS job_state FROM job_attempts a JOIN jobs j ON j.id=a.job_id JOIN availability_sessions s ON s.id=a.session_id WHERE a.id=$1 AND a.device_id=$2 AND a.epoch=$3 AND a.lease_expires_at>clock_timestamp() AND s.status='ACTIVE' AND s.expires_at>clock_timestamp() FOR UPDATE OF a",
            [m.attempt.id, device, m.attempt.epoch],
          )
        ).rows[0];
        if (
          !a ||
          !["OFFERED", "ACCEPTED", "RUNNING"].includes(a.state) ||
          a.job_state !== "RUNNING"
        ) {
          response.cancel_attempt_id = m.attempt.id;
        } else {
          let next: string | undefined;
          if (m.type === "JOB_ACCEPT" && a.state === "OFFERED")
            next = "ACCEPTED";
          if (m.type === "JOB_START" && a.state === "ACCEPTED")
            next = "RUNNING";
          if (m.type === "JOB_FAIL") next = "FAILED";
          if (m.type === "JOB_COMPLETE" && a.state === "RUNNING")
            next =
              m.attempt.result === (a.expected_hash ?? legacyExpected)
                ? "VERIFIED"
                : "FAILED";
          if (next) {
            await c.query(
              "UPDATE job_attempts SET state=$2,started_at=CASE WHEN $2='RUNNING' THEN now() ELSE started_at END,finished_at=CASE WHEN $2 IN ('VERIFIED','FAILED') THEN now() ELSE finished_at END,result_hash=$3 WHERE id=$1",
              [a.id, next, m.attempt.result ?? null],
            );
            await event(c, a.job_id, a.id, next, m.trace_id);
            if (next === "VERIFIED") {
              await c.query(
                "UPDATE jobs SET state='SUCCEEDED',result_hash=$2 WHERE id=$1",
                [a.job_id, m.attempt.result],
              );
              await recordUsage(c, a.id, m.attempt.usage);
              response.job_state = "SUCCEEDED";
            } else if (next === "FAILED") {
              response.job_state = await transitionFailedJob(
                c,
                a.job_id,
                a.epoch,
                "FAILED",
                m.trace_id,
                m.type === "JOB_FAIL",
              );
            }
          }
        }
      }
      if (
        m.type === "WORK_REQUEST" &&
        m.session?.status === "ACTIVE" &&
        !m.attempt
      ) {
        const s = (
          await c.query(
            "SELECT s.* FROM availability_sessions s JOIN devices d ON d.id=s.device_id WHERE s.id=$1 AND s.device_id=$2 AND s.status='ACTIVE' AND s.expires_at>clock_timestamp()+interval '6 seconds' AND d.revoked_at IS NULL AND d.capabilities->'runtimes' ? 'synthetic-hash-v1'",
            [m.session.id, device],
          )
        ).rows[0];
        const busy = (
          await c.query(
            "SELECT id FROM job_attempts WHERE device_id=$1 AND state IN ('OFFERED','ACCEPTED','RUNNING')",
            [device],
          )
        ).rowCount;
        if (s && auth.externalScheduler()) {
          const offer = await requestedOffer(c, device, s.id);
          if (offer) response = { offer };
          else if (!busy)
            await c.query(
              "INSERT INTO device_work_requests(device_id,session_id,expires_at) VALUES($1,$2,clock_timestamp()+interval '3 seconds') ON CONFLICT(device_id) DO UPDATE SET session_id=EXCLUDED.session_id,revision=CASE WHEN device_work_requests.session_id=EXCLUDED.session_id THEN device_work_requests.revision ELSE device_work_requests.revision+1 END,requested_at=clock_timestamp(),expires_at=EXCLUDED.expires_at",
              [device, s.id],
            );
        }
        if (s && !busy && !auth.externalScheduler()) {
          const job = (
            await c.query(
              "SELECT * FROM jobs WHERE state='QUEUED' AND next_eligible_at<=clock_timestamp() AND timeout_seconds+5<extract(epoch FROM ($1::timestamptz-now())) ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1",
              [s.expires_at],
            )
          ).rows[0];
          if (job) {
            response = {
              offer: await reserveJob(c, job, device, s, m.trace_id),
            };
          }
        }
      }
      await c.query("COMMIT");
      return response;
    } catch (e) {
      await c.query("ROLLBACK");
      throw e;
    } finally {
      c.release();
    }
  }
  return { control };
}
