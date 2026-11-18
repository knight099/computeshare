import { recordUsage } from "../../services/api/src/usage.ts";
import { OpenComputeClient } from "../../packages/sdk-typescript/src/index.ts";
import { after, before, test } from "node:test";
import assert from "node:assert/strict";
import { createServer, type Server } from "node:http";
import { once } from "node:events";
import { generateKeyPairSync, sign } from "node:crypto";
import { generateKeyPair, exportJWK, SignJWT } from "jose";
import pg from "pg";
import WebSocket from "ws";
import { loadConfig, type Config } from "../../packages/config/src/index.ts";
import {
  buildApp,
  enrollmentMessage,
  challengeMessage,
  tokenHash,
} from "../../services/api/src/app.ts";
import { migrate } from "../../services/api/src/migrate.ts";
import { CONSENT_VERSION } from "../../packages/protocol/src/index.ts";

let pool: pg.Pool;
let adminPool: pg.Pool;
let app: Awaited<ReturnType<typeof buildApp>>;
let issuer: Server;
let config: Config;
let base: string;
let alice: string, bob: string, operator: string;
let issue: (
  subject: string,
  roles: string[],
  overrides?: { aud?: string; issuer?: string; expires?: number },
) => Promise<string>;
const schema = `test_${crypto.randomUUID().replaceAll("-", "")}`;
const caps = {
  os: "linux",
  arch: "amd64",
  cpu_cores: 2,
  memory_mb: 2048,
  agent_version: "0.1.0",
  runtimes: [],
};
const keypair = () => {
  const pair = generateKeyPairSync("ed25519");
  const raw = pair.publicKey
    .export({ format: "der", type: "spki" })
    .subarray(-32)
    .toString("base64");
  return { raw, private: pair.privateKey };
};
const key = keypair();
let device: string;
const proof = (text: string) =>
  sign(null, Buffer.from(text), key.private).toString("base64");
const auth = (token: string) => ({ authorization: `Bearer ${token}` });
async function enroll(token = alice, pair = key) {
  return app.inject({
    method: "POST",
    url: "/v1/devices",
    headers: auth(token),
    payload: {
      name: "Test Linux",
      public_key: pair.raw,
      signature: sign(
        null,
        Buffer.from(enrollmentMessage(config.OPERATOR_ID, pair.raw)),
        pair.private,
      ).toString("base64"),
      capabilities: caps,
    },
  });
}
async function credential() {
  const challenge = (
    await app.inject({
      method: "POST",
      url: "/v1/device-auth/challenge",
      payload: { device_id: device },
    })
  ).json();
  const response = await app.inject({
    method: "POST",
    url: "/v1/device-auth/token",
    payload: {
      device_id: device,
      nonce: challenge.nonce,
      signature: proof(
        challengeMessage(config.OPERATOR_ID, device, challenge.nonce),
      ),
    },
  });
  assert.equal(response.statusCode, 200);
  return response.json().access_token as string;
}
async function connect(token: string) {
  const socket = new WebSocket(
    base.replace("http", "ws") + "/v1/agent/connect",
    { headers: auth(token) },
  );
  await once(socket, "open");
  return socket;
}
const message = (id = device, sequence = 0, type = "HELLO") => ({
  protocol_version: 1,
  type,
  device_id: id,
  sequence,
  trace_id: crypto.randomUUID(),
  ...(type === "HELLO" ? { capabilities: caps } : {}),
});

before(async () => {
  const databaseURL = process.env.TEST_DATABASE_URL;
  if (!databaseURL)
    throw new Error(
      "TEST_DATABASE_URL must point to a disposable PostgreSQL database",
    );
  const { publicKey, privateKey } = await generateKeyPair("RS256");
  const jwk = {
    ...(await exportJWK(publicKey)),
    kid: "test-key",
    alg: "RS256",
    use: "sig",
  };
  issuer = createServer((_req, res) => {
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify({ keys: [jwk] }));
  });
  issuer.listen(0, "127.0.0.1");
  await once(issuer, "listening");
  const issuerURL = `http://127.0.0.1:${(issuer.address() as { port: number }).port}`;
  config = loadConfig({
    NODE_ENV: "test",
    DATABASE_URL: databaseURL,
    REDIS_URL: "redis://localhost:6379",
    NATS_URL: "nats://localhost:4222",
    S3_ACCESS_KEY_ID: "test-key",
    S3_SECRET_ACCESS_KEY: "test-only-secret-value",
    S3_ENDPOINT: "http://localhost:9000",
    OIDC_ISSUER: issuerURL,
    OIDC_JWKS_URL: issuerURL,
    OIDC_AUDIENCE: "test-api",
    OPERATOR_ID: crypto.randomUUID(),
  });
  adminPool = new pg.Pool({ connectionString: databaseURL });
  await adminPool.query(`CREATE SCHEMA ${schema}`);
  pool = new pg.Pool({
    connectionString: databaseURL,
    options: `-c search_path=${schema}`,
  });
  await migrate(pool, config);
  issue = async (subject, roles, overrides = {}) =>
    new SignJWT({ realm_access: { roles } })
      .setProtectedHeader({ alg: "RS256", kid: "test-key" })
      .setSubject(subject)
      .setIssuer(overrides.issuer ?? issuerURL)
      .setAudience(overrides.aud ?? "test-api")
      .setIssuedAt()
      .setExpirationTime(
        overrides.expires ?? Math.floor(Date.now() / 1000) + 300,
      )
      .sign(privateKey);
  [alice, bob, operator] = await Promise.all([
    issue("alice", ["contributor"]),
    issue("bob", ["contributor"]),
    issue("operator", ["operator_admin"]),
  ]);
  app = await buildApp(config, {
    pool,
    logger: false,
    ready: async () => ({ database: true }),
  });
  base = await app.listen({ port: 0, host: "127.0.0.1" });
});
after(async () => {
  if (app) await app.close();
  if (issuer) issuer.close();
  if (pool) await pool.end();
  if (adminPool) {
    await adminPool.query(`DROP SCHEMA IF EXISTS ${schema} CASCADE`);
    await adminPool.end();
  }
});

test("liveness remains healthy when readiness dependencies fail", async () => {
  const unhealthy = await buildApp(config, {
    pool,
    logger: false,
    ready: async () => ({ database: false }),
  });
  assert.equal((await unhealthy.inject("/healthz")).statusCode, 200);
  assert.equal((await unhealthy.inject("/readyz")).statusCode, 503);
  await unhealthy.close();
});
test("OIDC rejects missing, expired, wrong audience and wrong issuer credentials", async () => {
  for (const token of [
    "",
    await issue("alice", ["contributor"], { aud: "other-company" }),
    await issue("alice", ["contributor"], {
      issuer: "https://other-company.invalid",
    }),
    await issue("alice", ["contributor"], { expires: 1 }),
  ]) {
    assert.equal(
      (await app.inject({ url: "/v1/me", headers: auth(token) })).statusCode,
      401,
    );
  }
  assert.equal(
    (await app.inject({ url: "/v1/operator/devices", headers: auth(alice) }))
      .statusCode,
    403,
  );
  assert.equal(
    (await app.inject({ url: "/v1/operator/devices", headers: auth(operator) }))
      .statusCode,
    200,
  );
});
test("enrollment requires explicit current consent, contributor role and device-key proof", async () => {
  assert.equal((await enroll()).statusCode, 403);
  assert.equal((await enroll(operator)).statusCode, 403);
  assert.equal(
    (
      await app.inject({
        method: "POST",
        url: "/v1/consent",
        headers: auth(alice),
        payload: { version: CONSENT_VERSION, accepted: false },
      })
    ).statusCode,
    400,
  );
  assert.equal(
    (
      await app.inject({
        method: "POST",
        url: "/v1/consent",
        headers: auth(alice),
        payload: { version: CONSENT_VERSION, accepted: true },
      })
    ).statusCode,
    200,
  );
  const invalid = await app.inject({
    method: "POST",
    url: "/v1/devices",
    headers: auth(alice),
    payload: {
      name: "bad",
      public_key: key.raw,
      signature: proof("wrong-company"),
      capabilities: caps,
    },
  });
  assert.equal(invalid.statusCode, 401);
  const response = await enroll();
  assert.equal(response.statusCode, 201);
  device = response.json().id;
  assert.equal((await enroll()).statusCode, 409);
  assert.equal(
    (
      await pool.query(
        "SELECT count(*)::int AS count FROM device_capability_snapshots",
      )
    ).rows[0].count,
    1,
  );
});
test("ownership checks prevent cross-user enumeration and revocation", async () => {
  assert.deepEqual(
    (await app.inject({ url: "/v1/devices", headers: auth(bob) })).json()
      .devices,
    [],
  );
  assert.equal(
    (await app.inject({ url: `/v1/devices/${device}`, headers: auth(bob) }))
      .statusCode,
    404,
  );
  assert.equal(
    (
      await app.inject({
        method: "POST",
        url: `/v1/devices/${device}/revoke`,
        headers: auth(bob),
      })
    ).statusCode,
    404,
  );
  assert.equal(
    (await app.inject({ url: `/v1/devices/${device}`, headers: auth(alice) }))
      .statusCode,
    200,
  );
});
test("device challenge requires proof, is operator-bound and cannot be replayed", async () => {
  const { nonce } = (
    await app.inject({
      method: "POST",
      url: "/v1/device-auth/challenge",
      payload: { device_id: device },
    })
  ).json();
  const payload = {
    device_id: device,
    nonce,
    signature: proof(challengeMessage(config.OPERATOR_ID, device, nonce)),
  };
  assert.equal(
    (
      await app.inject({
        method: "POST",
        url: "/v1/device-auth/token",
        payload: { ...payload, signature: proof("forged") },
      })
    ).statusCode,
    401,
  );
  const success = await app.inject({
    method: "POST",
    url: "/v1/device-auth/token",
    payload,
  });
  assert.equal(success.statusCode, 200);
  assert.equal(
    (
      await app.inject({
        method: "POST",
        url: "/v1/device-auth/token",
        payload,
      })
    ).statusCode,
    401,
  );
  const stored = (
    await pool.query(
      "SELECT token_hash FROM device_credentials WHERE device_id=$1",
      [device],
    )
  ).rows[0].token_hash;
  assert.equal(stored, tokenHash(success.json().access_token));
  await pool.query(
    "UPDATE device_credentials SET expires_at=now()-interval '1 second'",
  );
  const response = await fetch(base + "/v1/agent/connect", {
    headers: auth(success.json().access_token),
  });
  assert.equal(response.status, 401);
});
test("gateway accepts HELLO/heartbeat, rejects replay and forged device identity", async () => {
  const token = await credential();
  const socket = await connect(token);
  const acknowledged = once(socket, "message");
  socket.send(JSON.stringify(message()));
  assert.equal(JSON.parse((await acknowledged)[0].toString()).type, "ACK");
  const heartbeat = once(socket, "message");
  socket.send(JSON.stringify(message(device, 1, "HEARTBEAT")));
  await heartbeat;
  const replay = once(socket, "close");
  socket.send(JSON.stringify(message(device, 1, "HEARTBEAT")));
  assert.equal((await replay)[0], 1008);
  const forged = await connect(token);
  const rejected = once(forged, "close");
  forged.send(JSON.stringify(message(crypto.randomUUID())));
  assert.equal((await rejected)[0], 1008);
  const status = (
    await app.inject({ url: "/v1/operator/devices", headers: auth(operator) })
  ).json().devices[0].status;
  assert.equal(status, "online");
});
test("capability changes append one snapshot while repeated heartbeats do not", async () => {
  const socket = await connect(await credential());
  try {
    let ack = once(socket, "message");
    socket.send(JSON.stringify(message()));
    await ack;
    const changed = { ...caps, cpu_cores: 4 };
    ack = once(socket, "message");
    socket.send(
      JSON.stringify({
        ...message(device, 1, "HEARTBEAT"),
        capabilities: changed,
      }),
    );
    await ack;
    ack = once(socket, "message");
    socket.send(
      JSON.stringify({
        ...message(device, 2, "HEARTBEAT"),
        capabilities: changed,
      }),
    );
    await ack;
    const snapshots = await pool.query(
      "SELECT capabilities FROM device_capability_snapshots WHERE device_id=$1 ORDER BY id",
      [device],
    );
    assert.equal(snapshots.rowCount, 2);
    assert.equal(snapshots.rows[0].capabilities.cpu_cores, 2);
    assert.equal(snapshots.rows[1].capabilities.cpu_cores, 4);
    const records = await pool.query(
      "SELECT 1 FROM audit_events WHERE action='CAPABILITIES_CHANGED' AND object_id=$1",
      [device],
    );
    assert.equal(records.rowCount, 1);
  } finally {
    socket.close();
  }
});
test("sharing requires explicit consent, isolates owners, and respects Stop", async () => {
  const body = {
    device_id: device,
    duration_seconds: 21600,
    policy: {
      cpu_percent: 25,
      memory_mb: 128,
      gpu_enabled: false,
      idle_only: false,
    },
    consent_version: "sharing-v1",
    accepted: true,
  };
  const originalDeadline = new Date(Date.now() + 300000).toISOString();
  const bounded = await app.inject({
    method: "POST",
    url: "/v1/availability-sessions",
    headers: auth(alice),
    payload: { ...body, consent_expires_at: originalDeadline },
  });
  assert.equal(bounded.statusCode, 201);
  assert.equal(
    Date.parse(bounded.json().expires_at),
    Date.parse(originalDeadline),
  );
  const expired = await app.inject({
    method: "POST",
    url: "/v1/availability-sessions",
    headers: auth(alice),
    payload: {
      ...body,
      consent_expires_at: new Date(Date.now() - 1000).toISOString(),
    },
  });
  assert.equal(expired.statusCode, 400);
  assert.equal(
    (
      await pool.query("SELECT status FROM availability_sessions WHERE id=$1", [
        bounded.json().id,
      ])
    ).rows[0].status,
    "ACTIVE",
  );
  const extended = await app.inject({
    method: "POST",
    url: "/v1/availability-sessions",
    headers: auth(alice),
    payload: {
      ...body,
      consent_expires_at: new Date(Date.now() + 86400000).toISOString(),
    },
  });
  assert.equal(extended.statusCode, 201);
  assert.equal(
    Date.parse(extended.json().expires_at) -
      Date.parse(extended.json().starts_at),
    21600000,
  );
  const refused = await app.inject({
    method: "POST",
    url: "/v1/availability-sessions",
    headers: auth(alice),
    payload: { ...body, accepted: false },
  });
  assert.equal(refused.statusCode, 400);
  assert.equal(
    (
      await app.inject({
        method: "POST",
        url: "/v1/availability-sessions",
        headers: auth(bob),
        payload: body,
      })
    ).statusCode,
    404,
  );
  const response = await app.inject({
    method: "POST",
    url: "/v1/availability-sessions",
    headers: auth(alice),
    payload: body,
  });
  assert.equal(response.statusCode, 201);
  const session = response.json();
  assert.equal(session.status, "ACTIVE");
  assert.equal(
    Date.parse(session.expires_at) - Date.parse(session.starts_at),
    21600000,
  );
  assert.equal(
    (
      await app.inject({
        url: "/v1/availability-sessions/" + session.id,
        headers: auth(bob),
      })
    ).statusCode,
    404,
  );
  assert.equal(
    (
      await app.inject({
        method: "POST",
        url: "/v1/availability-sessions/" + session.id + "/stop",
        headers: auth(alice),
      })
    ).statusCode,
    200,
  );
  assert.equal(
    (
      await app.inject({
        url: "/v1/availability-sessions/" + session.id,
        headers: auth(alice),
      })
    ).json().status,
    "STOPPED",
  );
});
test("only allowlisted public synthetic jobs are accepted and submission is idempotent", async () => {
  const payload = {
    idempotency_key: "one-safe-job",
    workload_type: "synthetic-hash-v1",
    seed: "public-test",
    iterations: 1000,
    timeout_seconds: 10,
  };
  assert.equal(
    (
      await app.inject({
        method: "POST",
        url: "/v1/jobs",
        headers: auth(alice),
        payload,
      })
    ).statusCode,
    403,
  );
  assert.equal(
    (
      await app.inject({
        method: "POST",
        url: "/v1/jobs",
        headers: auth(operator),
        payload: { ...payload, command: "sh" },
      })
    ).statusCode,
    400,
  );
  const first = await app.inject({
    method: "POST",
    url: "/v1/jobs",
    headers: auth(operator),
    payload,
  });
  assert.equal(first.statusCode, 201);
  const duplicate = await app.inject({
    method: "POST",
    url: "/v1/jobs",
    headers: auth(operator),
    payload,
  });
  assert.equal(duplicate.json().id, first.json().id);
  assert.equal(
    (
      await app.inject({
        method: "POST",
        url: "/v1/jobs",
        headers: auth(operator),
        payload: { ...payload, iterations: 2000 },
      })
    ).statusCode,
    409,
  );
});
test(
  "leases fence attempts, reject early/invalid results, and verify only completed known work",
  { timeout: 15000 },
  async () => {
    const body = {
      device_id: device,
      duration_seconds: 21600,
      policy: {
        cpu_percent: 25,
        memory_mb: 128,
        gpu_enabled: false,
        idle_only: false,
      },
      consent_version: "sharing-v1",
      accepted: true,
    };
    const session = (
      await app.inject({
        method: "POST",
        url: "/v1/availability-sessions",
        headers: auth(alice),
        payload: body,
      })
    ).json();
    const socket = await connect(await credential());
    let seq = 0;
    const frame = async (type: string, extra: Record<string, unknown> = {}) => {
      const wait = once(socket, "message");
      socket.send(
        JSON.stringify({
          ...message(device, seq++, type),
          session: { id: session.id, status: "ACTIVE" },
          ...extra,
        }),
      );
      return JSON.parse((await wait)[0].toString());
    };
    try {
      await frame("HELLO", {
        capabilities: { ...caps, runtimes: ["synthetic-hash-v1"] },
      });
      const offered = (await frame("WORK_REQUEST")).offer;
      assert(offered);
      const attempt = { id: offered.attempt_id, epoch: offered.epoch };
      await frame("JOB_COMPLETE", {
        attempt: { ...attempt, result: "0".repeat(64) },
      });
      assert.equal(
        (
          await app.inject({
            url: "/v1/jobs/" + offered.job_id,
            headers: auth(operator),
          })
        ).json().state,
        "RUNNING",
      );
      assert.equal(
        (await frame("JOB_ACCEPT", { attempt: { ...attempt, epoch: 999 } }))
          .cancel_attempt_id,
        attempt.id,
      );
      await frame("JOB_ACCEPT", { attempt });
      await frame("JOB_START", { attempt });
      assert.equal(
        (
          await frame("JOB_COMPLETE", {
            attempt: { ...attempt, result: "0".repeat(64) },
          })
        ).job_state,
        "FAILED",
      );
      const job = (
        await app.inject({
          method: "POST",
          url: "/v1/jobs",
          headers: auth(operator),
          payload: {
            idempotency_key: "known-vector",
            workload_type: "synthetic-hash-v1",
            seed: "public",
            iterations: 2,
            timeout_seconds: 10,
          },
        })
      ).json();
      const next = (await frame("WORK_REQUEST")).offer;
      assert.equal(next.job_id, job.id);
      const a = { id: next.attempt_id, epoch: next.epoch };
      await frame("JOB_ACCEPT", { attempt: a });
      await frame("JOB_START", { attempt: a });
      // Exercise upgrade compatibility: this job predates stored expected hashes.
      await pool.query("UPDATE jobs SET expected_hash=NULL WHERE id=$1", [
        job.id,
      ]);
      const complete = {
        ...a,
        result:
          "315fc33dbdb63592565e8ed77eff5d3cf512638ea9b26631b7277cc73a3fc395",
        usage: { cpu_usage_usec: 1000, memory_peak_bytes: 1024 },
      };
      assert.equal(
        (await frame("JOB_COMPLETE", { attempt: complete })).job_state,
        "SUCCEEDED",
      );
      assert.equal(
        (await frame("JOB_COMPLETE", { attempt: complete })).cancel_attempt_id,
        a.id,
      );
      const events = await pool.query(
        "SELECT 1 FROM job_events WHERE job_id=$1 AND state='VERIFIED'",
        [job.id],
      );
      assert.equal(events.rowCount, 1);
      const usage = (
        await app.inject({ url: "/v1/contributor/usage", headers: auth(alice) })
      ).json();
      assert.equal(
        usage.records.filter(
          (r: { attempt_id: string }) => r.attempt_id === a.id,
        ).length,
        1,
      );
      const record = usage.records.find(
        (r: { attempt_id: string }) => r.attempt_id === a.id,
      );
      assert.equal(record.output_verified, true);
      assert.equal(record.logical_iterations, 2);
      assert.equal(record.cpu_usage_usec, "1000");
      assert.equal(record.telemetry_status, "REPORTED_UNTRUSTED");
      assert.equal(
        (
          await app.inject({ url: "/v1/contributor/usage", headers: auth(bob) })
        ).json().records.length,
        0,
      );
      assert.equal(
        (await app.inject({ url: "/v1/operator/usage", headers: auth(bob) }))
          .statusCode,
        403,
      );
      assert.equal(
        (
          await app.inject({
            url: "/v1/operator/usage",
            headers: auth(operator),
          })
        ).json().records.length,
        1,
      );

      await app.inject({
        method: "POST",
        url: "/v1/availability-sessions/" + session.id + "/stop",
        headers: auth(alice),
      });
      assert.equal((await frame("WORK_REQUEST")).offer, undefined);
    } finally {
      socket.close();
    }
  },
);
test(
  "cancellation never replaces the cancel signal with a new offer",
  { timeout: 10000 },
  async () => {
    const session = (
      await app.inject({
        method: "POST",
        url: "/v1/availability-sessions",
        headers: auth(alice),
        payload: {
          device_id: device,
          duration_seconds: 21600,
          policy: {
            cpu_percent: 25,
            memory_mb: 128,
            gpu_enabled: false,
            idle_only: false,
          },
          consent_version: "sharing-v1",
          accepted: true,
        },
      })
    ).json();
    const create = async (key: string) =>
      (
        await app.inject({
          method: "POST",
          url: "/v1/jobs",
          headers: auth(operator),
          payload: {
            idempotency_key: key,
            workload_type: "synthetic-hash-v1",
            seed: "public",
            iterations: 2,
          },
        })
      ).json();
    const a = await create("cancel-first");
    await create("cancel-next");
    const socket = await connect(await credential());
    let seq = 0;
    const frame = async (type: string, extra: Record<string, unknown> = {}) => {
      const reply = once(socket, "message");
      socket.send(
        JSON.stringify({
          ...message(device, seq++, type),
          session: { id: session.id, status: "ACTIVE" },
          ...extra,
        }),
      );
      return JSON.parse((await reply)[0].toString());
    };
    try {
      await frame("HELLO", {
        capabilities: { ...caps, runtimes: ["synthetic-hash-v1"] },
      });
      const offer = (await frame("WORK_REQUEST")).offer;
      assert.equal(offer.job_id, a.id);
      const attempt = { id: offer.attempt_id, epoch: 1 };
      await frame("JOB_ACCEPT", { attempt });
      await frame("JOB_START", { attempt });
      await app.inject({
        method: "POST",
        url: "/v1/jobs/" + a.id + "/cancel",
        headers: auth(operator),
      });
      const answer = await frame("WORK_REQUEST", { attempt });
      assert.equal(answer.cancel_attempt_id, attempt.id);
      assert.equal(answer.offer, undefined);
    } finally {
      socket.close();
      await app.inject({
        method: "POST",
        url: "/v1/availability-sessions/" + session.id + "/stop",
        headers: auth(alice),
      });
      await pool.query("UPDATE jobs SET state='CANCELED' WHERE state='QUEUED'");
    }
  },
);
test(
  "cancel and completion use the same transaction lock before changing rows",
  { timeout: 15000 },
  async () => {
    const session = (
      await app.inject({
        method: "POST",
        url: "/v1/availability-sessions",
        headers: auth(alice),
        payload: {
          device_id: device,
          duration_seconds: 21600,
          policy: {
            cpu_percent: 25,
            memory_mb: 128,
            gpu_enabled: false,
            idle_only: false,
          },
          consent_version: "sharing-v1",
          accepted: true,
        },
      })
    ).json();
    const job = (
      await app.inject({
        method: "POST",
        url: "/v1/jobs",
        headers: auth(operator),
        payload: {
          idempotency_key: "concurrent-cancel",
          workload_type: "synthetic-hash-v1",
          seed: "public",
          iterations: 2,
        },
      })
    ).json();
    const socket = await connect(await credential());
    let seq = 0;
    const frame = async (type: string, extra: Record<string, unknown> = {}) => {
      const answer = once(socket, "message");
      socket.send(
        JSON.stringify({
          ...message(device, seq++, type),
          session: { id: session.id, status: "ACTIVE" },
          ...extra,
        }),
      );
      return JSON.parse((await answer)[0].toString());
    };
    const blocker = await pool.connect();
    let completion: Promise<unknown> | undefined;
    let cancellation: Promise<{ statusCode: number }> | undefined;
    try {
      await frame("HELLO", {
        capabilities: { ...caps, runtimes: ["synthetic-hash-v1"] },
      });
      const offer = (await frame("WORK_REQUEST")).offer;
      const attempt = { id: offer.attempt_id, epoch: 1 };
      await frame("JOB_ACCEPT", { attempt });
      await frame("JOB_START", { attempt });
      await blocker.query("BEGIN");
      await blocker.query("SELECT pg_advisory_xact_lock(78230002)");
      completion = frame("JOB_COMPLETE", {
        attempt: {
          ...attempt,
          result:
            "315fc33dbdb63592565e8ed77eff5d3cf512638ea9b26631b7277cc73a3fc395",
        },
      });
      cancellation = app.inject({
        method: "POST",
        url: "/v1/jobs/" + job.id + "/cancel",
        headers: auth(operator),
      });
      const end = Date.now() + 3000;
      let waiting = 0;
      while (Date.now() < end) {
        waiting = (
          await adminPool.query(
            "SELECT count(*)::int AS n FROM pg_stat_activity WHERE wait_event='advisory' AND query LIKE '%78230002%' AND state='active'",
          )
        ).rows[0].n;
        if (waiting >= 2) break;
        await new Promise((r) => setTimeout(r, 10));
      }
      assert(
        waiting >= 2,
        "both mutation paths must acquire the shared lock first",
      );
      assert.equal(
        (await blocker.query("SELECT state FROM jobs WHERE id=$1", [job.id]))
          .rows[0].state,
        "RUNNING",
      );
      await blocker.query("COMMIT");
      const [ack, canceled] = await Promise.all([completion, cancellation]);
      assert(ack);
      assert(canceled);
      assert([200, 409].includes(canceled.statusCode));
    } finally {
      await blocker.query("ROLLBACK");
      blocker.release();
      await Promise.allSettled([completion, cancellation]);
      socket.close();
      await app.inject({
        method: "POST",
        url: "/v1/availability-sessions/" + session.id + "/stop",
        headers: auth(alice),
      });
    }
  },
);
test("revocation disconnects active agent and rejects fresh authentication", async () => {
  const session = (
    await app.inject({
      method: "POST",
      url: "/v1/availability-sessions",
      headers: auth(alice),
      payload: {
        device_id: device,
        duration_seconds: 21600,
        policy: {
          cpu_percent: 25,
          memory_mb: 128,
          gpu_enabled: false,
          idle_only: false,
        },
        consent_version: "sharing-v1",
        accepted: true,
      },
    })
  ).json();
  const job = await new OpenComputeClient(
    base,
    () => operator,
  ).submitSyntheticJob({
    idempotency_key: "revoke-running",
    seed: "public",
    iterations: 2,
    max_attempts: 2,
  });
  await pool.query("UPDATE jobs SET state='RUNNING' WHERE id=$1", [job.id]);
  const attempt = (
    await pool.query(
      "INSERT INTO job_attempts(job_id,device_id,session_id,epoch,state,lease_expires_at,started_at) VALUES($1,$2,$3,1,'RUNNING',now()+interval '30 seconds',now()) RETURNING id",
      [job.id, device, session.id],
    )
  ).rows[0].id;
  const socket = await connect(await credential());
  const acknowledged = once(socket, "message");
  socket.send(JSON.stringify(message()));
  await acknowledged;
  const closed = once(socket, "close");
  assert.equal(
    (
      await app.inject({
        method: "POST",
        url: `/v1/devices/${device}/revoke`,
        headers: auth(alice),
      })
    ).statusCode,
    200,
  );
  assert.equal((await closed)[0], 1008);
  assert.equal(
    (
      await pool.query("SELECT status FROM availability_sessions WHERE id=$1", [
        session.id,
      ])
    ).rows[0].status,
    "STOPPED",
  );
  assert.equal(
    (await pool.query("SELECT state FROM job_attempts WHERE id=$1", [attempt]))
      .rows[0].state,
    "CANCELED",
  );
  assert.equal(
    (await pool.query("SELECT state FROM jobs WHERE id=$1", [job.id])).rows[0]
      .state,
    "QUEUED",
  );
  assert.equal(
    (await pool.query("SELECT * FROM usage_records WHERE job_id=$1", [job.id]))
      .rowCount,
    0,
  );

  assert.equal(
    (
      await app.inject({
        method: "POST",
        url: "/v1/device-auth/challenge",
        payload: { device_id: device },
      })
    ).statusCode,
    401,
  );
  const events = (
    await app.inject({
      url: "/v1/operator/audit-events",
      headers: auth(operator),
    })
  ).json().events;
  for (const action of [
    "CONSENT_ACCEPTED",
    "DEVICE_REGISTERED",
    "DEVICE_AUTHENTICATED",
    "DEVICE_CONNECTED",
    "DEVICE_REVOKED",
    "AUDIT_VIEWED",
  ])
    assert(
      events.some(
        (e: { action: string; trace_id: string }) =>
          e.action === action && e.trace_id,
      ),
    );
});
test("database operator binding and repeatable migrations", async () => {
  await migrate(pool, config);
  await assert.rejects(
    () => migrate(pool, { ...config, OPERATOR_ID: crypto.randomUUID() }),
    /another operator/,
  );
});

test("public configuration traffic cannot exhaust authenticated users behind the console proxy", async () => {
  for (let i = 0; i < 241; i++) await app.inject("/v1/config");
  assert.equal(
    (await app.inject({ url: "/v1/me", headers: auth(alice) })).statusCode,
    200,
  );
});

test("contributor history is chronological and microsecond-safe across pages", async () => {
  const owner = (
    await pool.query("SELECT id FROM users WHERE external_subject='alice'")
  ).rows[0].id;
  const session = (
    await pool.query(
      "INSERT INTO availability_sessions(device_id,expires_at,requested_duration_seconds,status,policy_snapshot,consent_version) VALUES($1,now()+interval '1 hour',3600,'STOPPED','{}','sharing-v1') RETURNING id",
      [device],
    )
  ).rows[0].id;
  await pool.query(
    "INSERT INTO jobs(id,submitter_id,idempotency_key,workload_type,payload,timeout_seconds,state,created_at) SELECT ('11111111-1111-4111-8111-'||lpad(i::text,12,'0'))::uuid,$1,'history-'||i,'synthetic-hash-v1','{}',10,'SUCCEEDED','2099-01-01'::timestamptz+i*interval '1 microsecond' FROM generate_series(1,205) i",
    [owner],
  );
  await pool.query(
    "INSERT INTO job_attempts(job_id,device_id,session_id,epoch,state,lease_expires_at) SELECT id,$1,$2,1,'VERIFIED',now() FROM jobs WHERE idempotency_key LIKE 'history-%'",
    [device, session],
  );
  let cursor: string | null = null;
  const seen: string[] = [];
  do {
    const r: {
      statusCode: number;
      json: () => { jobs: { id: string }[]; next_cursor: string | null };
    } = await app.inject({
      url:
        "/v1/contributor/jobs?limit=50" +
        (cursor ? "&cursor=" + encodeURIComponent(cursor) : ""),
      headers: auth(alice),
    });
    assert.equal(r.statusCode, 200);
    const page: { jobs: { id: string }[]; next_cursor: string | null } =
      r.json();
    if (!seen.length)
      assert.equal(page.jobs[0].id, "11111111-1111-4111-8111-000000000205");
    seen.push(...page.jobs.map((j: { id: string }) => j.id));
    cursor = page.next_cursor;
  } while (cursor);
  assert.equal(new Set(seen).size, seen.length);
  assert.equal(seen.filter((id) => id.startsWith("11111111-")).length, 205);
  assert.equal(
    (
      await app.inject({
        url: "/v1/contributor/jobs?limit=0",
        headers: auth(alice),
      })
    ).statusCode,
    400,
  );
  assert.equal(
    (
      await app.inject({
        url: "/v1/contributor/jobs?cursor=invalid",
        headers: auth(alice),
      })
    ).statusCode,
    400,
  );
  assert.deepEqual(
    (
      await app.inject({
        url: "/v1/contributor/jobs?limit=50",
        headers: auth(bob),
      })
    ).json().jobs,
    [],
  );
});

test("typed SDK submits, replays, reads and cancels against authenticated real API", async () => {
  const client = new OpenComputeClient(base, () => operator);
  const input = {
    idempotency_key: "sdk-real-api",
    seed: "public",
    iterations: 2,
  };
  const job = await client.submitSyntheticJob(input);
  assert.equal((await client.submitSyntheticJob(input)).id, job.id);
  assert.equal((await client.getJob(job.id)).state, "QUEUED");
  const listed = await client.listJobs({ limit: 1 });
  assert.equal(listed.jobs.length, 1);
  assert.equal((await client.cancelJob(job.id)).canceled, true);
  assert.equal((await client.getJob(job.id)).state, "CANCELED");
  assert.equal(
    (await new OpenComputeClient(base, () => bob).listContributorJobs()).jobs
      .length,
    0,
  );
});

test("external scheduling requires service role, fresh intent and one atomic claim", async () => {
  config.SCHEDULER_MODE = "external";
  const service = await issue("scheduler", ["scheduler_service"]);
  const candidates = () =>
    app.inject({
      url: "/internal/scheduler/candidates",
      headers: auth(service),
    });
  try {
    assert.equal(
      (
        await app.inject({
          url: "/internal/scheduler/candidates",
          headers: auth(operator),
        })
      ).statusCode,
      403,
    );
    await pool.query("UPDATE devices SET revoked_at=NULL WHERE id=$1", [
      device,
    ]);
    const startSession = async () =>
      (
        await app.inject({
          method: "POST",
          url: "/v1/availability-sessions",
          headers: auth(alice),
          payload: {
            device_id: device,
            duration_seconds: 21600,
            policy: {
              cpu_percent: 25,
              memory_mb: 128,
              gpu_enabled: false,
              idle_only: false,
            },
            consent_version: "sharing-v1",
            accepted: true,
          },
        })
      ).json();
    let session = await startSession();
    const client = new OpenComputeClient(base, () => operator);
    let job = await client.submitSyntheticJob({
      idempotency_key: "external-schedule",
      max_attempts: 2,
      seed: "public",
      iterations: 2,
    });
    const claim = (revision: number) =>
      app.inject({
        method: "POST",
        url: "/internal/scheduler/claims",
        headers: auth(service),
        payload: {
          job_id: job.id,
          device_id: device,
          session_id: session.id,
          request_revision: revision,
        },
      });
    assert.equal(
      (await claim(1)).statusCode,
      409,
      "no autonomous dispatch without a local request",
    );
    const socket = await connect(await credential());
    let seq = 0;
    const frame = async (type: string, extra: Record<string, unknown> = {}) => {
      const reply = once(socket, "message");
      socket.send(
        JSON.stringify({
          ...message(device, seq++, type),
          capabilities:
            type === "HELLO"
              ? { ...caps, runtimes: ["synthetic-hash-v1"] }
              : undefined,
          session: { id: session.id, status: "ACTIVE" },
          ...extra,
        }),
      );
      return JSON.parse((await reply)[0].toString());
    };
    try {
      await frame("HELLO");
      assert.equal((await frame("WORK_REQUEST")).offer, undefined);
      // Old incompatible queue entries must not permanently hide useful work.
      await pool.query(
        "INSERT INTO jobs(submitter_id,idempotency_key,workload_type,payload,timeout_seconds,state,created_at) SELECT submitter_id,'long-prefix-'||i,'synthetic-hash-v1',payload,30,'QUEUED','2000-01-01'::timestamptz+i*interval '1 microsecond' FROM jobs CROSS JOIN generate_series(1,128) i WHERE id=$1",
        [job.id],
      );
      await pool.query(
        "UPDATE availability_sessions SET expires_at=now()+interval '20 seconds' WHERE id=$1",
        [session.id],
      );
      await frame("WORK_REQUEST");
      // A prefix of short-lived devices must not hide a usable later device.
      await pool.query(
        `WITH owner AS (SELECT owner_id FROM devices WHERE id=$1),
        ds AS (INSERT INTO devices(owner_id,public_key,name,capabilities) SELECT owner_id,'scheduler-decoy-'||i,'decoy','{"cpu_cores":1,"memory_mb":64,"runtimes":["synthetic-hash-v1"]}' FROM owner CROSS JOIN generate_series(1,128) i RETURNING id),
        ss AS (INSERT INTO availability_sessions(device_id,starts_at,expires_at,requested_duration_seconds,status,policy_snapshot,consent_version) SELECT id,now(),now()+interval '14 seconds',21600,'ACTIVE','{"memory_mb":64}','sharing-v1' FROM ds RETURNING id,device_id)
        INSERT INTO device_work_requests(device_id,session_id,requested_at,expires_at) SELECT device_id,id,'2000-01-01',now()+interval '3 seconds' FROM ss`,
        [device],
      );
      const snapshot = (await candidates()).json();
      const d = snapshot.devices.find(
        (d: { device_id: string }) => d.device_id === device,
      );
      assert(d);
      assert(snapshot.jobs.some((j: { id: string }) => j.id === job.id));
      assert.equal((await claim(d.revision + 1)).statusCode, 409);
      await pool.query(
        "UPDATE device_work_requests SET expires_at=now()-interval '1 second' WHERE device_id=$1",
        [device],
      );
      assert.equal((await claim(d.revision)).statusCode, 409);
      await frame("WORK_REQUEST");
      await pool.query(
        "UPDATE availability_sessions SET expires_at=now()+interval '14 seconds' WHERE id=$1",
        [session.id],
      );
      assert.equal(
        (await claim(d.revision)).statusCode,
        409,
        "deadline safety margin rechecked on claim",
      );
      await pool.query(
        "UPDATE availability_sessions SET expires_at=now()+interval '6 hours' WHERE id=$1",
        [session.id],
      );
      const results = await Promise.all([claim(d.revision), claim(d.revision)]);
      assert.deepEqual(results.map((r) => r.statusCode).sort(), [201, 409]);
      const firstOffer = (await frame("WORK_REQUEST")).offer;
      assert.equal(firstOffer.job_id, job.id);
      await app.inject({
        method: "POST",
        url: "/v1/availability-sessions/" + session.id + "/stop",
        headers: auth(alice),
      });
      assert(
        !(await candidates())
          .json()
          .devices.some((d: { device_id: string }) => d.device_id === device),
      );
      assert.equal((await claim(d.revision)).statusCode, 409);
      assert.equal((await client.getJob(job.id)).state, "QUEUED");
      session = await startSession();
      await frame("WORK_REQUEST");
      await pool.query(
        "UPDATE jobs SET next_eligible_at=now()+interval '10 seconds' WHERE id=$1",
        [job.id],
      );
      assert(
        !(await candidates())
          .json()
          .jobs.some((j: { id: string }) => j.id === job.id),
      );
      await pool.query(
        "UPDATE jobs SET next_eligible_at=now()-interval '1 second' WHERE id=$1",
        [job.id],
      );
      const replacement = (await candidates())
        .json()
        .devices.find((d: { device_id: string }) => d.device_id === device);
      assert.equal((await claim(replacement.revision)).statusCode, 201);
      const secondOffer = (await frame("WORK_REQUEST")).offer;
      assert.equal(secondOffer.epoch, 2);
      const stale = {
        id: firstOffer.attempt_id,
        epoch: firstOffer.epoch,
        result:
          "315fc33dbdb63592565e8ed77eff5d3cf512638ea9b26631b7277cc73a3fc395",
      };
      assert.equal(
        (await frame("JOB_COMPLETE", { attempt: stale })).cancel_attempt_id,
        stale.id,
      );
      assert.equal((await client.getJob(job.id)).state, "RUNNING");
      const attempt = { id: secondOffer.attempt_id, epoch: secondOffer.epoch };
      await frame("JOB_ACCEPT", { attempt });
      await frame("JOB_START", { attempt });
      const completion = { ...attempt, result: stale.result };
      assert.equal(
        (await frame("JOB_COMPLETE", { attempt: completion })).job_state,
        "SUCCEEDED",
      );
      await frame("JOB_COMPLETE", { attempt: completion });
      assert.equal(
        (
          await pool.query("SELECT * FROM usage_records WHERE job_id=$1", [
            job.id,
          ])
        ).rowCount,
        1,
      );
      for (const mode of [
        "worker-failure",
        "invalid-output",
        "company-cancel",
      ] as const) {
        job = await client.submitSyntheticJob({
          idempotency_key: mode,
          seed: "public",
          iterations: 2,
          max_attempts: 3,
        });
        await frame("WORK_REQUEST");
        const ready = (await candidates())
          .json()
          .devices.find((d: { device_id: string }) => d.device_id === device);
        assert.equal((await claim(ready.revision)).statusCode, 201);
        const offered = (await frame("WORK_REQUEST")).offer;
        const attempt = { id: offered.attempt_id, epoch: offered.epoch };
        await frame("JOB_ACCEPT", { attempt });
        await frame("JOB_START", { attempt });
        if (mode === "company-cancel") await client.cancelJob(job.id);
        const response =
          mode === "invalid-output"
            ? await frame("JOB_COMPLETE", {
                attempt: { ...attempt, result: "0".repeat(64) },
              })
            : await frame("JOB_FAIL", { attempt });
        const expected =
          mode === "worker-failure"
            ? "QUEUED"
            : mode === "invalid-output"
              ? "FAILED"
              : "CANCELED";
        assert.equal((await client.getJob(job.id)).state, expected);
        if (mode === "worker-failure") {
          assert.equal(response.job_state, "QUEUED");
          await client.cancelJob(job.id);
        } else {
          assert.equal(
            (
              await pool.query(
                "SELECT * FROM job_events WHERE job_id=$1 AND state='RETRY_QUEUED'",
                [job.id],
              )
            ).rowCount,
            0,
          );
        }
        assert.equal(
          (
            await pool.query("SELECT * FROM usage_records WHERE job_id=$1", [
              job.id,
            ])
          ).rowCount,
          0,
        );
      }
      await app.inject({
        method: "POST",
        url: "/v1/availability-sessions/" + session.id + "/stop",
        headers: auth(alice),
      });
    } finally {
      socket.close();
    }
  } finally {
    config.SCHEDULER_MODE = "inline";
  }
});

test("usage rejects implausible telemetry, preserves first record and excludes invalid output", async () => {
  const fixture = await pool.query(
    `SELECT a.id,a.device_id,a.session_id,j.submitter_id FROM job_attempts a JOIN jobs j ON j.id=a.job_id WHERE a.state='VERIFIED' AND j.payload->>'iterations' IS NOT NULL LIMIT 1`,
  );
  const original = fixture.rows[0];
  assert(original);
  async function makeAttempt(key: string, state: string) {
    const job = (
      await pool.query(
        'INSERT INTO jobs(submitter_id,idempotency_key,workload_type,payload,timeout_seconds,state) VALUES($1,$2,\'synthetic-hash-v1\',\'{"seed":"public","iterations":2}\',10,$3) RETURNING id',
        [
          original.submitter_id,
          key,
          state === "VERIFIED" ? "SUCCEEDED" : "FAILED",
        ],
      )
    ).rows[0].id;
    const attempt = (
      await pool.query(
        "INSERT INTO job_attempts(job_id,device_id,session_id,epoch,state,lease_expires_at,started_at,finished_at) VALUES($1,$2,$3,1,$4,now(),now()-interval '1 second',now()) RETURNING id",
        [job, original.device_id, original.session_id, state],
      )
    ).rows[0].id;
    return { job, attempt };
  }
  const forged = await makeAttempt("usage-forged", "VERIFIED");
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    await recordUsage(client, forged.attempt, {
      cpu_usage_usec: 600000000,
      memory_peak_bytes: 900000000000,
    });
    await recordUsage(client, forged.attempt, {
      cpu_usage_usec: 1,
      memory_peak_bytes: 1,
    });
    await client.query("COMMIT");
    const record = (
      await pool.query("SELECT * FROM usage_records WHERE job_id=$1", [
        forged.job,
      ])
    ).rows[0];
    assert.equal(record.telemetry_status, "REJECTED");
    assert.equal(record.rejection_reason, "EXCEEDS_POLICY_BOUNDS");
    assert.equal(record.cpu_usage_usec, "600000000");
    const sdk = new OpenComputeClient(base, () => operator);
    const first = await sdk.listUsage({ limit: 1 });
    assert.equal(first.records.length, 1);
    assert(first.next_cursor);
    const second = await sdk.listUsage({ limit: 1, cursor: first.next_cursor });
    assert.equal(second.records.length, 1);
    assert.notEqual(first.records[0].id, second.records[0].id);
    assert.equal(
      (await new OpenComputeClient(base, () => bob).listContributorUsage())
        .records.length,
      0,
    );

    const legacy = await makeAttempt("usage-not-reported", "VERIFIED");
    await recordUsage(client, legacy.attempt);
    assert.equal(
      (
        await pool.query(
          "SELECT telemetry_status FROM usage_records WHERE job_id=$1",
          [legacy.job],
        )
      ).rows[0].telemetry_status,
      "NOT_REPORTED",
    );
    const invalid = await makeAttempt("usage-invalid-output", "FAILED");
    await recordUsage(client, invalid.attempt, {
      cpu_usage_usec: 1,
      memory_peak_bytes: 1,
    });
    assert.equal(
      (
        await pool.query("SELECT * FROM usage_records WHERE job_id=$1", [
          invalid.job,
        ])
      ).rowCount,
      0,
    );
  } finally {
    client.release();
  }
});
