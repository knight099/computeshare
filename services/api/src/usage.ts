import type pg from "pg";
import type { FastifyInstance, FastifyRequest } from "fastify";
import { requireRole, type Principal } from "./auth.ts";
import {
  historyPageSchema,
  decodeHistoryCursor,
  encodeHistoryCursor,
} from "./pagination.ts";
type Report = { cpu_usage_usec: number; memory_peak_bytes: number };
// Output validity is independently established before this call. Host telemetry
// remains untrusted even when within policy bounds. Never calculate money here.
export async function recordUsage(
  c: pg.PoolClient,
  attempt: string,
  report?: Report,
) {
  await c.query(
    `INSERT INTO usage_records(job_id,attempt_id,device_id,session_id,output_verified,logical_iterations,cpu_usage_usec,memory_peak_bytes,server_elapsed_usec,telemetry_status,rejection_reason,policy_snapshot)
    SELECT j.id,a.id,a.device_id,a.session_id,true,(j.payload->>'iterations')::integer,$2::bigint,$3::bigint,
    GREATEST(0,extract(epoch FROM (clock_timestamp()-a.started_at))*1000000)::bigint,
    CASE WHEN $2::bigint IS NULL THEN 'NOT_REPORTED'
      WHEN $2>(j.timeout_seconds+1)::bigint*1000000 OR $3>(s.policy_snapshot->>'memory_mb')::bigint*1048576 THEN 'REJECTED'
      ELSE 'REPORTED_UNTRUSTED' END,
    CASE WHEN $2>(j.timeout_seconds+1)::bigint*1000000 OR $3>(s.policy_snapshot->>'memory_mb')::bigint*1048576 THEN 'EXCEEDS_POLICY_BOUNDS' END,
    s.policy_snapshot FROM job_attempts a JOIN jobs j ON j.id=a.job_id JOIN availability_sessions s ON s.id=a.session_id
    WHERE a.id=$1 AND a.state='VERIFIED' AND j.state='SUCCEEDED'
    ON CONFLICT(job_id) DO NOTHING`,
    [
      attempt,
      report?.cpu_usage_usec ?? null,
      report?.memory_peak_bytes ?? null,
    ],
  );
}
export function registerUsage(
  app: FastifyInstance,
  pool: pg.Pool,
  user: (r: FastifyRequest) => Promise<Principal & { id: string }>,
) {
  for (const contributor of [true, false])
    app.get(
      contributor ? "/v1/contributor/usage" : "/v1/operator/usage",
      async (r) => {
        const u = await user(r);
        if (!contributor)
          requireRole(u, [
            "operator_admin",
            "operator_support",
            "auditor",
            "workload_submitter",
          ]);
        const all =
          !contributor &&
          u.roles.some((role) =>
            ["operator_admin", "operator_support", "auditor"].includes(role),
          );
        const p = historyPageSchema.parse(r.query),
          cursor = decodeHistoryCursor(p.cursor);
        const visibility = contributor
          ? "($2::boolean OR d.owner_id=$1)"
          : "($2::boolean OR j.submitter_id=$1)";
        const { rows } = await pool.query(
          `SELECT u.id,u.job_id,u.attempt_id,u.device_id,u.session_id,u.output_verified,u.logical_iterations,u.cpu_usage_usec,u.memory_peak_bytes,u.server_elapsed_usec,u.telemetry_status,u.rejection_reason,u.policy_snapshot,u.observed_at,to_char(u.observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') AS cursor_time FROM usage_records u JOIN jobs j ON j.id=u.job_id JOIN devices d ON d.id=u.device_id WHERE ${visibility} AND ($3::timestamptz IS NULL OR (u.observed_at,u.id)<($3::timestamptz,$4::uuid)) ORDER BY u.observed_at DESC,u.id DESC LIMIT $5`,
          [u.id, all, cursor?.time ?? null, cursor?.id ?? null, p.limit + 1],
        );
        const shown = rows.slice(0, p.limit),
          last = shown.at(-1);
        return {
          records: shown.map((row) => {
            const { cursor_time, ...record } = row;
            void cursor_time;
            return record;
          }),
          next_cursor:
            rows.length > p.limit && last
              ? encodeHistoryCursor(last.cursor_time, last.id)
              : null,
        };
      },
    );
}
