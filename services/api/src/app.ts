import Fastify, { type FastifyRequest } from "fastify";
import websocket from "@fastify/websocket";
import { createHash, createPublicKey, randomBytes, verify } from "node:crypto";
import pg from "pg";
import { z, ZodError } from "zod";
import type { Config } from "../../../packages/config/src/index.ts";
import {
  capabilitiesSchema,
  controlMessageSchema,
  CONSENT_VERSION,
} from "../../../packages/protocol/src/index.ts";
import { HttpError, oidcVerifier, requireRole } from "./auth.ts";
import { recordPresence } from "./presence.ts";
import { lockCompute } from "./compute-maintenance.ts";
import { registerScheduler } from "./scheduling.ts";
import { registerCompute } from "./compute.ts";

export const tokenHash = (value: string) =>
  createHash("sha256").update(value).digest("hex");
const deviceId = z.string().uuid();
const publicKey = z.string().regex(/^[A-Za-z0-9+/]{43}=$/);
export const enrollmentSchema = z
  .object({
    name: z.string().trim().min(1).max(80),
    public_key: publicKey,
    signature: z.string().max(128),
    capabilities: capabilitiesSchema,
  })
  .strict();
export function enrollmentMessage(operator: string, key: string) {
  return `opencompute-enroll-v1:${operator}:${key}`;
}
export function challengeMessage(
  operator: string,
  device: string,
  nonce: string,
) {
  return `opencompute-auth-v1:${operator}:${device}:${nonce}`;
}
export function verifySignature(
  key: string,
  message: string,
  signature: string,
) {
  try {
    const raw = Buffer.from(key, "base64");
    if (raw.length !== 32 || raw.toString("base64") !== key) return false;
    const spki = Buffer.concat([
      Buffer.from("302a300506032b6570032100", "hex"),
      raw,
    ]);
    return verify(
      null,
      Buffer.from(message),
      createPublicKey({ key: spki, format: "der", type: "spki" }),
      Buffer.from(signature, "base64"),
    );
  } catch {
    return false;
  }
}

export async function buildApp(
  config: Config,
  deps: {
    pool: pg.Pool;
    ready: () => Promise<Record<string, boolean>>;
    logger?: boolean;
  },
) {
  const { pool } = deps;
  const app = Fastify({
    logger:
      deps.logger === false
        ? false
        : {
            redact: [
              "req.headers.authorization",
              "req.headers.cookie",
              'res.headers["set-cookie"]',
            ],
          },
    bodyLimit: 32768,
    requestTimeout: 10000,
  });
  const authenticate = oidcVerifier(config);
  const connected = new Map<string, { close: () => void }>();
  const rates = new Map<string, { count: number; until: number }>();
  const gc = setInterval(() => {
    for (const [key, rate] of rates)
      if (rate.until < Date.now()) rates.delete(key);
  }, 60000);
  gc.unref();
  function consumeRate(key: string, limit = 240) {
    const previous = rates.get(key);
    const rate =
      previous && previous.until > Date.now()
        ? previous
        : { count: 0, until: Date.now() + 60000 };
    if (rates.size >= 10000 && !rates.has(key))
      throw new HttpError(429, "Too many requests");
    rates.set(key, rate);
    if (++rate.count > limit) throw new HttpError(429, "Too many requests");
  }
  app.addHook("onRequest", async (req) => {
    // Device authentication is separate from human OIDC. Never trust arbitrary forwarded IP headers.
    if (
      req.url.startsWith("/v1/device-auth/") ||
      req.url.startsWith("/v1/agent/connect")
    )
      consumeRate(`device-auth:${req.ip}`);
  });
  app.addHook("onSend", async (_req, reply) => {
    reply.header("Cache-Control", "no-store");
    reply.header("X-Content-Type-Options", "nosniff");
  });
  app.setErrorHandler((error, req, reply) => {
    const status =
      error instanceof ZodError
        ? 400
        : error instanceof HttpError
          ? error.statusCode
          : ((error as { statusCode?: number }).statusCode ?? 500);
    if (status >= 500)
      req.log.error(
        {
          trace_id: req.id,
          code: (error as { code?: string }).code ?? "internal",
        },
        "Request failed",
      );
    reply.code(status).send({
      error:
        status >= 500
          ? "Internal server error"
          : error instanceof ZodError
            ? "Invalid request"
            : error instanceof Error
              ? error.message
              : "Request rejected",
      trace_id: req.id,
    });
  });
  await app.register(websocket, { options: { maxPayload: 16384 } });

  async function user(req: FastifyRequest) {
    const principal = await authenticate(req).catch((error) => {
      consumeRate(`unauthenticated:${req.ip}`);
      throw error;
    });
    // The console proxy shares an IP for all users; only a verified subject can select this bucket.
    consumeRate(`user:${principal.subject}`);
    const { rows } = await pool.query(
      "INSERT INTO users(external_subject) VALUES($1) ON CONFLICT(external_subject) DO UPDATE SET external_subject=EXCLUDED.external_subject RETURNING id",
      [principal.subject],
    );
    return { ...principal, id: rows[0].id as string };
  }
  async function audit(
    actor: string,
    action: string,
    object: string,
    req: FastifyRequest,
    client: Pick<pg.Pool, "query"> = pool,
  ) {
    await client.query(
      "INSERT INTO audit_events(actor_id,action,object_id,trace_id) VALUES($1,$2,$3,$4)",
      [actor, action, object, req.id],
    );
  }
  async function deviceAuth(req: FastifyRequest) {
    const header = req.headers.authorization;
    if (!header?.startsWith("Bearer "))
      throw new HttpError(401, "Device credential required");
    const { rows } = await pool.query(
      "SELECT d.id, c.expires_at FROM device_credentials c JOIN devices d ON d.id=c.device_id WHERE c.token_hash=$1 AND c.expires_at>now() AND d.revoked_at IS NULL",
      [tokenHash(header.slice(7))],
    );
    if (!rows[0])
      throw new HttpError(401, "Invalid or expired device credential");
    consumeRate(
      `device:${rows[0].id}`,
      req.routeOptions.url === "/v1/agent/connect" ? 1200 : 60,
    );
    return rows[0] as { id: string; expires_at: Date };
  }

  const compute = registerCompute(app, {
    pool,
    user,
    deviceAuth,
    externalScheduler: () => config.SCHEDULER_MODE === "external",
  });
  registerScheduler(app, pool, authenticate);
  app.get("/healthz", async () => ({ status: "ok" }));
  app.get("/readyz", async (_req, reply) => {
    let checks: Record<string, boolean>;
    try {
      checks = await deps.ready();
    } catch {
      checks = { dependencies: false };
    }
    const ready =
      Object.values(checks).length > 0 && Object.values(checks).every(Boolean);
    return reply
      .code(ready ? 200 : 503)
      .send({ status: ready ? "ready" : "not_ready", checks });
  });
  app.get("/v1/config", async () => ({
    operator_id: config.OPERATOR_ID,
    operator_name: config.OPERATOR_NAME,
    consent_version: CONSENT_VERSION,
    oidc_issuer: config.OIDC_ISSUER,
    oidc_client_id: "opencompute-console",
    execution_enabled: true,
    execution_mode: "synthetic-only",
  }));
  app.get("/v1/me", async (req) => {
    const u = await user(req);
    return { id: u.id, roles: u.roles };
  });
  app.post("/v1/consent", async (req) => {
    const u = await user(req);
    requireRole(u, ["contributor"]);
    z.object({ version: z.literal(CONSENT_VERSION), accepted: z.literal(true) })
      .strict()
      .parse(req.body);
    const client = await pool.connect();
    try {
      await client.query("BEGIN");
      await client.query(
        "INSERT INTO consents(user_id,version) VALUES($1,$2) ON CONFLICT DO NOTHING",
        [u.id, CONSENT_VERSION],
      );
      await audit(u.id, "CONSENT_ACCEPTED", CONSENT_VERSION, req, client);
      await client.query("COMMIT");
    } catch (e) {
      await client.query("ROLLBACK");
      throw e;
    } finally {
      client.release();
    }
    return { version: CONSENT_VERSION, accepted: true };
  });
  app.post("/v1/devices", async (req, reply) => {
    const u = await user(req);
    requireRole(u, ["contributor"]);
    const body = enrollmentSchema.parse(req.body);
    if (
      !verifySignature(
        body.public_key,
        enrollmentMessage(config.OPERATOR_ID, body.public_key),
        body.signature,
      )
    )
      throw new HttpError(401, "Invalid device proof");
    const client = await pool.connect();
    try {
      await client.query("BEGIN");
      const consent = await client.query(
        "SELECT 1 FROM consents WHERE user_id=$1 AND version=$2",
        [u.id, CONSENT_VERSION],
      );
      if (!consent.rowCount)
        throw new HttpError(403, "Current consent required");
      const { rows } = await client.query(
        "INSERT INTO devices(owner_id,public_key,name,capabilities) VALUES($1,$2,$3,$4) ON CONFLICT(public_key) DO NOTHING RETURNING id",
        [u.id, body.public_key, body.name, body.capabilities],
      );
      if (!rows[0]) throw new HttpError(409, "Device key already registered");
      await client.query(
        "INSERT INTO device_capability_snapshots(device_id,capabilities) VALUES($1,$2)",
        [rows[0].id, body.capabilities],
      );
      await audit(u.id, "DEVICE_REGISTERED", rows[0].id, req, client);
      await client.query("COMMIT");
      return reply
        .code(201)
        .send({ id: rows[0].id, operator_id: config.OPERATOR_ID });
    } catch (e) {
      await client.query("ROLLBACK");
      throw e;
    } finally {
      client.release();
    }
  });
  app.get("/v1/devices", async (req) => {
    const u = await user(req);
    const { rows } = await pool.query(
      "SELECT id,name,capabilities,registered_at,last_seen_at,revoked_at FROM devices WHERE owner_id=$1 ORDER BY registered_at DESC LIMIT 200",
      [u.id],
    );
    return { devices: rows };
  });
  app.get("/v1/devices/:id", async (req) => {
    const u = await user(req);
    const id = deviceId.parse((req.params as { id: string }).id);
    const { rows } = await pool.query(
      "SELECT id,name,capabilities,registered_at,last_seen_at,revoked_at FROM devices WHERE id=$1 AND owner_id=$2",
      [id, u.id],
    );
    if (!rows[0]) throw new HttpError(404, "Device not found");
    return rows[0];
  });
  app.post("/v1/devices/:id/revoke", async (req) => {
    const u = await user(req);
    const id = deviceId.parse((req.params as { id: string }).id);
    const client = await pool.connect();
    try {
      await client.query("BEGIN");
      await lockCompute(client);
      const { rowCount } = await client.query(
        "UPDATE devices SET revoked_at=COALESCE(revoked_at,now()),version=version+1 WHERE id=$1 AND owner_id=$2 RETURNING id",
        [id, u.id],
      );
      if (!rowCount) throw new HttpError(404, "Device not found");
      await client.query("DELETE FROM device_credentials WHERE device_id=$1", [
        id,
      ]);
      await compute.stopDeviceSessions(client, id);
      await audit(u.id, "DEVICE_REVOKED", id, req, client);
      await client.query("COMMIT");
    } catch (e) {
      await client.query("ROLLBACK");
      throw e;
    } finally {
      client.release();
    }
    connected.get(id)?.close();
    return { revoked: true };
  });
  app.post("/v1/device-auth/challenge", async (req) => {
    const body = z.object({ device_id: deviceId }).strict().parse(req.body);
    const nonce = randomBytes(32).toString("base64url");
    const { rowCount } = await pool.query(
      "INSERT INTO device_challenges(device_id,nonce,expires_at) SELECT id,$2,now()+interval '60 seconds' FROM devices WHERE id=$1 AND revoked_at IS NULL ON CONFLICT(device_id) DO UPDATE SET nonce=EXCLUDED.nonce,expires_at=EXCLUDED.expires_at",
      [body.device_id, nonce],
    );
    if (!rowCount) throw new HttpError(401, "Device unavailable");
    return { nonce, operator_id: config.OPERATOR_ID, expires_in: 60 };
  });
  app.post("/v1/device-auth/token", async (req) => {
    const body = z
      .object({
        device_id: deviceId,
        nonce: z.string().max(100),
        signature: z.string().max(128),
      })
      .strict()
      .parse(req.body);
    const client = await pool.connect();
    try {
      await client.query("BEGIN");
      // Device lock serializes issuance against revocation; nonce is consumed only on success.
      const { rows } = await client.query(
        "SELECT public_key FROM devices WHERE id=$1 AND revoked_at IS NULL FOR UPDATE",
        [body.device_id],
      );
      if (
        !rows[0] ||
        !verifySignature(
          rows[0].public_key,
          challengeMessage(config.OPERATOR_ID, body.device_id, body.nonce),
          body.signature,
        )
      )
        throw new HttpError(401, "Invalid device proof");
      const challenge = await client.query(
        "DELETE FROM device_challenges WHERE device_id=$1 AND nonce=$2 AND expires_at>now() RETURNING device_id",
        [body.device_id, body.nonce],
      );
      if (!challenge.rowCount)
        throw new HttpError(401, "Challenge expired or already used");
      const token = randomBytes(32).toString("base64url");
      await client.query(
        "DELETE FROM device_credentials WHERE expires_at<=now() OR device_id=$1",
        [body.device_id],
      );
      await client.query(
        "INSERT INTO device_credentials(token_hash,device_id,expires_at) VALUES($1,$2,now()+$3*interval '1 second')",
        [tokenHash(token), body.device_id, config.DEVICE_TOKEN_TTL_SECONDS],
      );
      await audit(
        body.device_id,
        "DEVICE_AUTHENTICATED",
        body.device_id,
        req,
        client,
      );
      await client.query("COMMIT");
      return {
        access_token: token,
        expires_in: config.DEVICE_TOKEN_TTL_SECONDS,
        token_type: "Bearer",
      };
    } catch (e) {
      await client.query("ROLLBACK");
      throw e;
    } finally {
      client.release();
    }
  });
  app.get("/v1/operator/devices", async (req) => {
    const u = await user(req);
    requireRole(u, ["operator_admin", "operator_support", "auditor"]);
    const { rows } = await pool.query(
      "SELECT id,name,capabilities,registered_at,last_seen_at,revoked_at, CASE WHEN revoked_at IS NOT NULL THEN 'revoked' WHEN last_seen_at>now()-interval '45 seconds' THEN 'online' ELSE 'offline' END AS status FROM devices ORDER BY registered_at DESC LIMIT 200",
    );
    return { devices: rows };
  });
  app.get("/v1/operator/audit-events", async (req) => {
    const u = await user(req);
    requireRole(u, ["operator_admin", "auditor"]);
    await audit(u.id, "AUDIT_VIEWED", config.OPERATOR_ID, req);
    return {
      events: (
        await pool.query(
          "SELECT * FROM audit_events ORDER BY id DESC LIMIT 200",
        )
      ).rows,
    };
  });

  app.get(
    "/v1/agent/connect",
    {
      websocket: true,
      preValidation: async (req) => {
        await deviceAuth(req);
      },
    },
    (socket, req) => {
      // Attach immediately: WebSocket messages must not race an awaited lookup.
      let sequence = -1;
      let hello = false;
      let busy = false;
      let activeDevice: string | undefined;
      const identity = deviceAuth(req);
      let expiry: ReturnType<typeof setTimeout> | undefined;
      let lastMessage = Date.now();
      const watchdog = setInterval(() => {
        if (Date.now() - lastMessage > 45000)
          socket.close(1008, "Heartbeat timeout");
      }, 5000);
      watchdog.unref();
      const entry = { close: () => socket.close(1008, "Device revoked") };
      socket.on("message", async (raw) => {
        if (busy) {
          socket.close(1008, "Concurrent messages");
          return;
        }
        busy = true;
        try {
          const credential = await identity;
          const message = controlMessageSchema.parse(
            JSON.parse(raw.toString()),
          );
          if (
            message.device_id !== credential.id ||
            message.sequence <= sequence ||
            (!hello && message.type !== "HELLO") ||
            (hello && message.type === "HELLO")
          )
            throw new Error("Invalid sequence or identity");
          if (!hello) {
            if (!message.capabilities) throw new Error("Capabilities required");
            connected.get(credential.id)?.close();
            connected.set(credential.id, entry);
            activeDevice = credential.id;
            expiry = setTimeout(
              () => socket.close(1008, "Credential expired"),
              Math.max(1, credential.expires_at.getTime() - Date.now()),
            );
            expiry.unref();
          }
          // Revocation and token expiry are rechecked on every message, including existing connections.
          await deviceAuth(req);
          if (message.type === "HELLO" || message.type === "HEARTBEAT")
            await recordPresence(
              pool,
              credential.id,
              message.capabilities,
              req.id,
            );
          if (!hello)
            await audit(credential.id, "DEVICE_CONNECTED", credential.id, req);
          sequence = message.sequence;
          hello = true;
          lastMessage = Date.now();
          const control = await compute.control(credential.id, message);
          socket.send(
            JSON.stringify({
              protocol_version: 1,
              type: "ACK",
              device_id: credential.id,
              sequence,
              trace_id: message.trace_id,
              server_time: new Date().toISOString(),
              ...control,
            }),
          );
        } catch {
          socket.close(1008, "Invalid control message or credential");
        } finally {
          busy = false;
        }
      });
      socket.on("close", () => {
        clearInterval(watchdog);
        if (expiry) clearTimeout(expiry);
        if (activeDevice && connected.get(activeDevice) === entry)
          connected.delete(activeDevice);
      });
      void identity.catch(() => socket.close(1008, "Device unavailable"));
    },
  );
  app.addHook("onClose", async () => {
    clearInterval(gc);
    for (const c of connected.values()) c.close();
  });
  return app;
}
