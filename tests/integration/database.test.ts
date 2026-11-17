import { test } from "node:test";
import assert from "node:assert/strict";
import pg from "pg";
import { createDatabasePool } from "../../services/api/src/database.ts";

test("idle database connection loss is handled and the pool reconnects", async () => {
  const connectionString = process.env.TEST_DATABASE_URL;
  assert(connectionString, "TEST_DATABASE_URL required");
  let errors = 0;
  const pool = createDatabasePool(connectionString, () => {
    errors++;
  });
  const admin = new pg.Pool({ connectionString });
  try {
    const client = await pool.connect();
    const pid = (await client.query("SELECT pg_backend_pid() AS pid")).rows[0]
      .pid;
    client.release();
    await admin.query("SELECT pg_terminate_backend($1)", [pid]);
    const deadline = Date.now() + 2000;
    while (!errors && Date.now() < deadline)
      await new Promise((r) => setTimeout(r, 10));
    assert.equal(
      errors,
      1,
      "idle connection errors must be handled without killing the process",
    );
    assert.equal((await pool.query("SELECT 1 AS ok")).rows[0].ok, 1);
  } finally {
    await pool.end();
    await admin.end();
  }
});

test("stalled database probes have a bounded deadline", async () => {
  assert(process.env.TEST_DATABASE_URL, "TEST_DATABASE_URL required");
  const pool = createDatabasePool(process.env.TEST_DATABASE_URL, () => {});
  try {
    const start = Date.now();
    await assert.rejects(() => pool.query("SELECT pg_sleep(5)"));
    assert(
      Date.now() - start < 3500,
      "database probe exceeded its bounded timeout",
    );
  } finally {
    await pool.end();
  }
});
