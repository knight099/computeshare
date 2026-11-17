import type { FastifyInstance, FastifyRequest } from "fastify";
import type pg from "pg";
import { z } from "zod";
import { HttpError, requireRole } from "./auth.ts";
type User = { id: string; subject: string; roles: string[] };
const cursorSchema = z
  .object({ time: z.iso.datetime({ precision: 6 }), id: z.string().uuid() })
  .strict();
const pageSchema = z
  .object({
    limit: z.coerce.number().int().min(1).max(200).default(200),
    cursor: z
      .string()
      .max(512)
      .regex(/^[A-Za-z0-9_-]+$/)
      .optional(),
  })
  .strict();
export function registerJobPages(
  app: FastifyInstance,
  pool: pg.Pool,
  user: (r: FastifyRequest) => Promise<User>,
) {
  for (const contributor of [false, true])
    app.get(
      contributor ? "/v1/contributor/jobs" : "/v1/operator/jobs",
      async (r) => {
        const u = await user(r);
        if (!contributor)
          requireRole(u, [
            "operator_admin",
            "operator_support",
            "auditor",
            "workload_submitter",
          ]);
        const p = pageSchema.parse(r.query);
        let cursor: z.infer<typeof cursorSchema> | undefined;
        if (p.cursor) {
          try {
            cursor = cursorSchema.parse(
              JSON.parse(Buffer.from(p.cursor, "base64url").toString("utf8")),
            );
          } catch {
            throw new HttpError(400, "Invalid history cursor");
          }
        }
        const all =
          !contributor &&
          u.roles.some((role) =>
            ["operator_admin", "operator_support", "auditor"].includes(role),
          );
        const visibility = contributor
          ? "($2::boolean OR EXISTS(SELECT 1 FROM job_attempts a JOIN devices d ON d.id=a.device_id WHERE a.job_id=j.id AND d.owner_id=$1))"
          : "($2 OR j.submitter_id=$1)";
        const { rows } = await pool.query(
          `SELECT j.id,j.workload_type,j.state,j.created_at,j.result_hash,to_char(j.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') AS cursor_time FROM jobs j WHERE ${visibility} AND ($3::timestamptz IS NULL OR (j.created_at,j.id)<($3::timestamptz,$4::uuid)) ORDER BY j.created_at DESC,j.id DESC LIMIT $5`,
          [u.id, all, cursor?.time ?? null, cursor?.id ?? null, p.limit + 1],
        );
        const shown = rows.slice(0, p.limit);
        const last = shown.at(-1);
        return {
          jobs: shown.map((row) => ({
            id: row.id,
            workload_type: row.workload_type,
            state: row.state,
            created_at: row.created_at,
            result_hash: row.result_hash,
          })),
          next_cursor:
            rows.length > p.limit && last
              ? Buffer.from(
                  JSON.stringify({ time: last.cursor_time, id: last.id }),
                ).toString("base64url")
              : null,
        };
      },
    );
}
