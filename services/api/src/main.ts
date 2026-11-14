import { connect } from "nats";
import { createClient } from "redis";
import { loadConfig } from "../../../packages/config/src/index.ts";
import { buildApp } from "./app.ts";
import { probeStorage } from "./storage.ts";
import { createDatabasePool } from "./database.ts";
import { withDeadline } from "./deadline.ts";
import { startAuthMaintenance } from "./maintenance.ts";

const config = loadConfig(process.env);
const pool = createDatabasePool(config.DATABASE_URL);
const redis = createClient({
  url: config.REDIS_URL,
  socket: { connectTimeout: 2000 },
});
redis.on("error", () => {}); // Readiness exposes outages without leaking connection strings.
void redis.connect().catch(() => {});
const app = await buildApp(config, {
  pool,
  ready: async () => {
    const probes = await Promise.allSettled([
      pool
        .query(
          "SELECT 1 FROM schema_migrations s CROSS JOIN operators_config o WHERE s.version=1 AND o.operator_id=$1",
          [config.OPERATOR_ID],
        )
        .then((r) => {
          if (r.rowCount !== 1)
            throw new Error("Database schema or operator mismatch");
        }),
      redis.isReady
        ? redis.withCommandOptions({ timeout: 2000 }).ping()
        : Promise.reject(new Error("Redis unavailable")),
      (async () => {
        const nc = await connect({
          servers: config.NATS_URL,
          timeout: 2000,
          reconnect: false,
        });
        try {
          await withDeadline(nc.flush(), 2000, () => {
            void nc.close();
          });
        } finally {
          await nc.close();
        }
      })(),
      probeStorage(
        config.S3_ENDPOINT,
        config.S3_ACCESS_KEY_ID,
        config.S3_SECRET_ACCESS_KEY,
      ),
      fetch(
        `${config.OIDC_JWKS_URL ?? `${config.OIDC_ISSUER}/protocol/openid-connect/certs`}`,
        { signal: AbortSignal.timeout(2000) },
      ).then((r) => {
        if (!r.ok) throw new Error("Identity unavailable");
      }),
    ]);
    return Object.fromEntries(
      ["postgres", "redis", "nats", "storage", "identity"].map((key, i) => [
        key,
        probes[i].status === "fulfilled",
      ]),
    );
  },
});
const stopMaintenance = startAuthMaintenance(pool, () =>
  app.log.warn(
    { event: "AUTH_MAINTENANCE_FAILED" },
    "Expired credential cleanup will retry",
  ),
);
app.addHook("onClose", async () => {
  await stopMaintenance();
  if (redis.isOpen) redis.destroy();
  await pool.end();
});
for (const signal of ["SIGINT", "SIGTERM"])
  process.on(signal, () => {
    void app.close();
  });
await app.listen({ port: config.PORT, host: "0.0.0.0" });
