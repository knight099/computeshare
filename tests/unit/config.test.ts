import { test } from "node:test";
import assert from "node:assert/strict";
import { loadConfig } from "../../packages/config/src/index.ts";

export const env = {
  DATABASE_URL: "postgres://user:secret@localhost/opencompute",
  REDIS_URL: "redis://localhost:6379",
  NATS_URL: "nats://localhost:4222",
  S3_ACCESS_KEY_ID: "test-key",
  S3_SECRET_ACCESS_KEY: "test-only-secret-value",
  S3_ENDPOINT: "http://localhost:9000",
  OIDC_ISSUER: "http://localhost:8081/realms/opencompute",
  OIDC_AUDIENCE: "opencompute-api",
  OPERATOR_ID: "7a444a90-6ca6-4421-9bf6-6fa686e5aa0f",
};
test("configuration requires valid endpoints and safe bounded token TTL", () => {
  assert.equal(loadConfig(env).PORT, 8080);
  assert.throws(() =>
    loadConfig({ ...env, DEVICE_TOKEN_TTL_SECONDS: "86400" }),
  );
  assert.throws(
    () => loadConfig({ ...env, DATABASE_URL: "secret" }),
    /DATABASE_URL/,
  );
  assert.throws(() => loadConfig({ ...env, NODE_ENV: "production" }), /HTTPS/);
});
test("configuration errors redact supplied secrets", () => {
  assert.throws(
    () => loadConfig({ ...env, DATABASE_URL: "do-not-log-me" }),
    (e) => e instanceof Error && !e.message.includes("do-not-log-me"),
  );
});
