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

test("checked-out connection loss does not crash API process and pool recovers", async () => {
  const { spawn } = await import("node:child_process");
  const { once } = await import("node:events");
  const script = `
    import assert from 'node:assert/strict';
    import pg from 'pg';
    import {createDatabasePool} from './services/api/src/database.ts';
    const pool=createDatabasePool(process.env.TEST_DATABASE_URL,()=>{});
    const admin=new pg.Pool({connectionString:process.env.TEST_DATABASE_URL});
    const client=await pool.connect();
    const pid=(await client.query('SELECT pg_backend_pid() AS pid')).rows[0].pid;
    await admin.query('SELECT pg_terminate_backend($1)',[pid]);
    await new Promise(r=>setTimeout(r,100));
    await assert.rejects(client.query('SELECT 1'));
    client.release();
    assert.equal((await pool.query('SELECT 1 AS ok')).rows[0].ok,1);
    await pool.end();await admin.end();
  `;
  const child = spawn(
    process.execPath,
    ["--import", "tsx", "--input-type=module", "-e", script],
    { env: process.env, stdio: ["ignore", "ignore", "pipe"], timeout: 10000 },
  );
  let output = "";
  child.stderr.on("data", (b) => {
    output += b.toString();
  });
  const [code] = await once(child, "exit");
  assert.equal(
    code,
    0,
    "database connection crash regression: " + output.slice(-1500),
  );
});
