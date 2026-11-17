import { test } from "node:test";
import assert from "node:assert/strict";
import pg from "pg";
import { connect, RetentionPolicy, StorageType } from "nats";
import { loadConfig } from "../../packages/config/src/index.ts";
import { migrate } from "../../services/api/src/migrate.ts";
import {
  deliverOutbox,
  openEventPublisher,
  eventNamespace,
} from "../../services/api/src/outbox.ts";

test("transactional outbox survives rollback, broker failure and acknowledgement replay", async () => {
  assert(process.env.TEST_DATABASE_URL);
  const admin = new pg.Pool({
    connectionString: process.env.TEST_DATABASE_URL,
  });
  const schema = "outbox_" + crypto.randomUUID().replaceAll("-", "");
  await admin.query(`CREATE SCHEMA ${schema}`);
  const pool = new pg.Pool({
    connectionString: process.env.TEST_DATABASE_URL,
    options: `-c search_path=${schema}`,
  });
  const operator = crypto.randomUUID();
  const namespace = eventNamespace(operator);
  const nc = await connect({
    servers: process.env.TEST_NATS_URL ?? "nats://127.0.0.1:4222",
  });
  const manager = await nc.jetstreamManager();
  let publisher: Awaited<ReturnType<typeof openEventPublisher>> | undefined;
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
        OPERATOR_ID: operator,
      }),
    );
    const user = (
      await pool.query(
        "INSERT INTO users(external_subject) VALUES('outbox') RETURNING id",
      )
    ).rows[0].id;
    const job = (
      await pool.query(
        "INSERT INTO jobs(submitter_id,idempotency_key,workload_type,payload,timeout_seconds,state) VALUES($1,'outbox','synthetic-hash-v1','{}',10,'QUEUED') RETURNING id",
        [user],
      )
    ).rows[0].id;
    const c = await pool.connect();
    try {
      await c.query("BEGIN");
      await c.query(
        "INSERT INTO job_events(job_id,state,trace_id) VALUES($1,'QUEUED','rolled-back')",
        [job],
      );
      await c.query("ROLLBACK");
    } finally {
      c.release();
    }
    assert.equal((await pool.query("SELECT * FROM event_outbox")).rowCount, 0);
    await pool.query(
      "INSERT INTO job_events(job_id,state,trace_id) VALUES($1,'QUEUED','committed')",
      [job],
    );
    await assert.rejects(
      deliverOutbox(pool, async () => {
        throw new Error("broker offline");
      }),
    );
    assert.equal(
      (
        await pool.query(
          "SELECT * FROM event_outbox WHERE published_at IS NULL",
        )
      ).rowCount,
      1,
    );
    publisher = await openEventPublisher(
      process.env.TEST_NATS_URL ?? "nats://127.0.0.1:4222",
      operator,
    );
    // Fail the SQL acknowledgement after the real JetStream acknowledgement.
    await pool.query(
      "CREATE FUNCTION reject_ack() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.published_at IS NOT NULL THEN RAISE EXCEPTION 'crash after broker ack'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_ack BEFORE UPDATE ON event_outbox FOR EACH ROW EXECUTE FUNCTION reject_ack()",
    );
    await assert.rejects(deliverOutbox(pool, publisher.publish));
    await pool.query(
      "DROP TRIGGER reject_ack ON event_outbox; UPDATE event_outbox SET lease_until=now()-interval '1 second'",
    );
    await Promise.all([
      deliverOutbox(pool, publisher.publish),
      deliverOutbox(pool, publisher.publish),
    ]);
    assert.equal(
      (
        await pool.query(
          "SELECT * FROM event_outbox WHERE published_at IS NOT NULL",
        )
      ).rowCount,
      1,
    );
    const info = await manager.streams.info(namespace.stream);
    assert.equal(
      info.state.messages,
      1,
      "stable message IDs deduplicate ack replay",
    );
    const message = await manager.streams.getMessage(namespace.stream, {
      seq: info.state.first_seq,
    });
    const payload = JSON.parse(new TextDecoder().decode(message.data));
    assert.equal(payload.job_id, job);
    assert.equal(payload.trace_id, "committed");
    assert.equal(payload.seed, undefined);
    assert.equal(info.config.retention, RetentionPolicy.Limits);
    assert.equal(info.config.storage, StorageType.File);
  } finally {
    await publisher?.close();
    await manager.streams.delete(namespace.stream).catch(() => {});
    await nc.close();
    await pool.end();
    await admin.query(`DROP SCHEMA ${schema} CASCADE`);
    await admin.end();
  }
});
