import type pg from "pg";
import type { Capabilities } from "../../../packages/protocol/src/index.ts";

export async function recordPresence(
  pool: pg.Pool,
  device: string,
  capabilities: Capabilities | undefined,
  trace: string,
) {
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    const encoded = capabilities ? JSON.stringify(capabilities) : null;
    const previous = await client.query(
      "SELECT capabilities IS DISTINCT FROM $2::jsonb AS changed FROM devices WHERE id=$1 AND revoked_at IS NULL FOR UPDATE",
      [device, encoded],
    );
    if (!previous.rowCount) throw new Error("Device unavailable");
    const changed = Boolean(capabilities && previous.rows[0].changed);
    await client.query(
      "UPDATE devices SET last_seen_at=now(),capabilities=COALESCE($2::jsonb,capabilities),version=version+$3 WHERE id=$1",
      [device, encoded, changed ? 1 : 0],
    );
    if (changed) {
      await client.query(
        "INSERT INTO device_capability_snapshots(device_id,capabilities) VALUES($1,$2)",
        [device, encoded],
      );
      await client.query(
        "INSERT INTO audit_events(actor_id,action,object_id,trace_id) VALUES($1,'CAPABILITIES_CHANGED',$1,$2)",
        [device, trace],
      );
    }
    await client.query("COMMIT");
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}
