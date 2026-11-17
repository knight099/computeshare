import { test } from "node:test";
import assert from "node:assert/strict";
import pg from "pg";
import { loadConfig } from "../../packages/config/src/index.ts";
import { migrate } from "../../services/api/src/migrate.ts";
import { reserveJob } from "../../services/api/src/scheduling.ts";
import {
  expireCompute,
  lockCompute,
} from "../../services/api/src/compute-maintenance.ts";

test("lease and session expiry become terminal without an agent reconnect", async () => {
  assert(process.env.TEST_DATABASE_URL);
  const admin = new pg.Pool({
    connectionString: process.env.TEST_DATABASE_URL,
  });
  const schema = "expiry_" + crypto.randomUUID().replaceAll("-", "");
  await admin.query(`CREATE SCHEMA ${schema}`);
  const pool = new pg.Pool({
    connectionString: process.env.TEST_DATABASE_URL,
    options: `-c search_path=${schema}`,
  });
  try {
    await migrate(
      pool,
      loadConfig({
        DATABASE_URL: process.env.TEST_DATABASE_URL,
        REDIS_URL: "redis://localhost:6379",
        NATS_URL: "nats://localhost:4222",
        S3_ENDPOINT: "http://localhost:9000",
        S3_ACCESS_KEY_ID: "test",
        S3_SECRET_ACCESS_KEY: "test-secret-value",
        OIDC_ISSUER: "http://localhost:8081",
        OIDC_AUDIENCE: "api",
        OPERATOR_ID: crypto.randomUUID(),
      }),
    );
    const user = (
      await pool.query(
        "INSERT INTO users(external_subject) VALUES('offline') RETURNING id",
      )
    ).rows[0].id;
    const d = (
      await pool.query(
        "INSERT INTO devices(owner_id,public_key,name,capabilities) VALUES($1,'test-public','offline','{}') RETURNING id",
        [user],
      )
    ).rows[0].id;
    const s = (
      await pool.query(
        "INSERT INTO availability_sessions(device_id,starts_at,expires_at,requested_duration_seconds,status,policy_snapshot,consent_version) VALUES($1,now()-interval '2 hour',now()+interval '1 hour',21600,'ACTIVE','{}','sharing-v1') RETURNING id",
        [d],
      )
    ).rows[0].id;
    const j = (
      await pool.query(
        "INSERT INTO jobs(submitter_id,idempotency_key,workload_type,payload,timeout_seconds,state) VALUES($1,'expired','synthetic-hash-v1','{}',10,'RUNNING') RETURNING id",
        [user],
      )
    ).rows[0].id;
    await pool.query(
      "INSERT INTO job_attempts(job_id,device_id,session_id,epoch,state,lease_expires_at) VALUES($1,$2,$3,1,'RUNNING',now()-interval '1 second')",
      [j, d, s],
    );
    await expireCompute(pool);
    assert.equal(
      (await pool.query("SELECT state FROM jobs WHERE id=$1", [j])).rows[0]
        .state,
      "FAILED",
    );
    assert.equal(
      (await pool.query("SELECT state FROM job_attempts WHERE job_id=$1", [j]))
        .rows[0].state,
      "LOST",
    );
    const retryJob = (
      await pool.query(
        "INSERT INTO jobs(submitter_id,idempotency_key,workload_type,payload,timeout_seconds,state,max_attempts) VALUES($1,'retry','synthetic-hash-v1','{\"seed\":\"public\",\"iterations\":2}',10,'RUNNING',2) RETURNING *",
        [user],
      )
    ).rows[0];
    await pool.query(
      "INSERT INTO job_attempts(job_id,device_id,session_id,epoch,state,lease_expires_at) VALUES($1,$2,$3,1,'RUNNING',now()-interval '1 second')",
      [retryJob.id, d, s],
    );
    await expireCompute(pool);
    const retried = (
      await pool.query(
        "SELECT state,next_eligible_at>clock_timestamp() AS delayed FROM jobs WHERE id=$1",
        [retryJob.id],
      )
    ).rows[0];
    assert.equal(retried.state, "QUEUED");
    assert.equal(retried.delayed, true);
    const c = await pool.connect();
    let second;
    try {
      await c.query("BEGIN");
      await lockCompute(c);
      second = await reserveJob(
        c,
        retryJob,
        d,
        { id: s, expires_at: new Date(Date.now() + 3600000) },
        "retry-test",
      );
      assert.equal(second.epoch, 2);
      await c.query("COMMIT");
    } finally {
      c.release();
    }
    await pool.query(
      "UPDATE job_attempts SET lease_expires_at=now()-interval '1 second' WHERE id=$1",
      [second.attempt_id],
    );
    await expireCompute(pool);
    assert.equal(
      (await pool.query("SELECT state FROM jobs WHERE id=$1", [retryJob.id]))
        .rows[0].state,
      "FAILED",
    );
    assert.equal(
      (
        await pool.query(
          "SELECT * FROM job_events WHERE job_id=$1 AND state='RETRY_QUEUED'",
          [retryJob.id],
        )
      ).rowCount,
      1,
    );
    await pool.query(
      "UPDATE availability_sessions SET expires_at=now()-interval '1 second' WHERE id=$1",
      [s],
    );
    await expireCompute(pool);
    assert.equal(
      (
        await pool.query(
          "SELECT status FROM availability_sessions WHERE id=$1",
          [s],
        )
      ).rows[0].status,
      "EXPIRED",
    );
  } finally {
    await pool.end();
    await admin.query(`DROP SCHEMA ${schema} CASCADE`);
    await admin.end();
  }
});
