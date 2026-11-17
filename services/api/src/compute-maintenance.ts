import { transitionFailedJob } from "./recovery.ts";
import type pg from "pg";

// All initial compute mutations acquire this lock before any device/session/job row.
// This intentionally serializes the small FIFO preview; partitioning is future work.
export async function lockCompute(c: pg.PoolClient) {
  await c.query("SELECT pg_advisory_xact_lock(78230002)");
}
export async function expireCompute(pool: pg.Pool) {
  const c = await pool.connect();
  try {
    await c.query("BEGIN");
    await lockCompute(c);
    const sessions = await c.query(
      "UPDATE availability_sessions SET status='EXPIRED',stopped_at=now() WHERE status='ACTIVE' AND expires_at<=now() RETURNING id,device_id",
    );
    for (const s of sessions.rows)
      await c.query(
        "INSERT INTO audit_events(actor_id,action,object_id,trace_id) VALUES($1,'SESSION_EXPIRED',$2,'expiry-sweep')",
        [s.device_id, s.id],
      );
    const attempts = await c.query(
      "UPDATE job_attempts a SET state=CASE WHEN s.status='ACTIVE' THEN 'LOST' ELSE 'CANCELED' END,finished_at=now() FROM availability_sessions s WHERE s.id=a.session_id AND a.state IN ('OFFERED','ACCEPTED','RUNNING') AND (a.lease_expires_at<=now() OR s.status<>'ACTIVE') RETURNING a.id,a.job_id,a.state,a.epoch",
    );
    for (const a of attempts.rows) {
      await transitionFailedJob(
        c,
        a.job_id,
        a.epoch,
        a.state === "LOST" ? "FAILED" : "CANCELED",
        "expiry-sweep",
      );
      await c.query(
        "INSERT INTO job_events(job_id,attempt_id,state,trace_id) VALUES($1,$2,$3,'expiry-sweep')",
        [a.job_id, a.id, a.state],
      );
    }
    await c.query("COMMIT");
    return {
      sessions: sessions.rowCount ?? 0,
      attempts: attempts.rowCount ?? 0,
    };
  } catch (e) {
    await c.query("ROLLBACK");
    throw e;
  } finally {
    c.release();
  }
}
export function startComputeMaintenance(pool: pg.Pool, onError: () => void) {
  let pending: Promise<unknown> | undefined;
  const tick = () => {
    if (!pending)
      pending = expireCompute(pool)
        .catch(onError)
        .finally(() => {
          pending = undefined;
        });
  };
  tick();
  const timer = setInterval(tick, 1000);
  timer.unref();
  return async () => {
    clearInterval(timer);
    await pending;
  };
}
