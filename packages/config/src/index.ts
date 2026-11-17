import { z } from "zod";

const url = z.string().url();
const schema = z.object({
  NODE_ENV: z
    .enum(["development", "test", "production"])
    .default("development"),
  SCHEDULER_MODE: z.enum(["inline", "external"]).default("inline"),
  PORT: z.coerce.number().int().min(1).max(65535).default(8080),
  DATABASE_URL: url,
  REDIS_URL: url,
  NATS_URL: url,
  S3_ENDPOINT: url,
  S3_ACCESS_KEY_ID: z.string().min(1),
  S3_SECRET_ACCESS_KEY: z.string().min(16),
  OIDC_ISSUER: url,
  OIDC_JWKS_URL: url.optional(),
  OIDC_AUDIENCE: z.string().min(1),
  OPERATOR_ID: z.string().uuid(),
  OPERATOR_NAME: z.string().min(1).max(100).default("OpenCompute"),
  DEVICE_TOKEN_TTL_SECONDS: z.coerce
    .number()
    .int()
    .min(60)
    .max(900)
    .default(300),
});

export type Config = z.infer<typeof schema>;
export function loadConfig(env: Record<string, string | undefined>): Config {
  const result = schema.safeParse(env);
  if (!result.success) {
    // Do not include values: URLs may contain database or storage passwords.
    throw new Error(
      `Invalid configuration: ${result.error.issues.map((i) => i.path.join(".")).join(", ")}`,
    );
  }
  const c = result.data;
  if (
    c.NODE_ENV === "production" &&
    [c.OIDC_ISSUER, c.S3_ENDPOINT, c.OIDC_JWKS_URL ?? c.OIDC_ISSUER].some(
      (u) => !u.startsWith("https://"),
    )
  ) {
    throw new Error("Production identity and artifact endpoints require HTTPS");
  }
  return c;
}
