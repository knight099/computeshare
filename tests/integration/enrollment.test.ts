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
test("revocation disconnects active agent and rejects fresh authentication", async () => {
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
