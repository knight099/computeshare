import { test } from "node:test";
import assert from "node:assert/strict";
import pg from "pg";
import { cleanupExpiredDeviceAuth } from "../../services/api/src/maintenance.ts";

test("maintenance deletes only expired credential/challenge rows and preserves audit history", async () => {
  assert(process.env.TEST_DATABASE_URL);
  const pool = new pg.Pool({ connectionString: process.env.TEST_DATABASE_URL });
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    // Dedicated temporary tables keep this integration test independent of application data.
    await client.query(
      "CREATE TEMP TABLE device_credentials(token_hash text,expires_at timestamptz)",
    );
    await client.query(
      "CREATE TEMP TABLE device_challenges(nonce text,expires_at timestamptz)",
    );
    await client.query(
      "CREATE TEMP TABLE audit_events(action text);INSERT INTO audit_events VALUES('retained')",
    );
    await client.query(
      "INSERT INTO device_credentials VALUES('expired',now()-interval '1 second'),('active',now()+interval '1 hour')",
    );
    await client.query(
      "INSERT INTO device_challenges VALUES('expired',now()-interval '1 second'),('active',now()+interval '1 hour')",
    );
    assert.deepEqual(await cleanupExpiredDeviceAuth(client), {
      credentials: 1,
      challenges: 1,
    });
    assert.equal(
      (await client.query("SELECT token_hash FROM device_credentials")).rows[0]
        .token_hash,
      "active",
    );
    assert.equal(
      (await client.query("SELECT nonce FROM device_challenges")).rows[0].nonce,
      "active",
    );
    assert.equal(
      (await client.query("SELECT count(*)::int AS n FROM audit_events"))
        .rows[0].n,
      1,
    );
    assert.deepEqual(await cleanupExpiredDeviceAuth(client), {
      credentials: 0,
      challenges: 0,
    });
  } finally {
    await client.query("ROLLBACK");
    client.release();
    await pool.end();
  }
});
