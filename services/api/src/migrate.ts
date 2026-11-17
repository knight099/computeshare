import { readFile } from "node:fs/promises";
import pg from "pg";
import { pathToFileURL } from "node:url";
import { loadConfig, type Config } from "../../../packages/config/src/index.ts";

export async function migrate(pool: pg.Pool, config: Config) {
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    await client.query("SELECT pg_advisory_xact_lock(78230001)");
    await client.query(
      await readFile(new URL("./schema.sql", import.meta.url), "utf8"),
    );
    await client.query(
      await readFile(new URL("./002-compute.sql", import.meta.url), "utf8"),
    );
    for (const [version, file] of [
      [3, "003-services.sql"],
      [4, "004-scheduler.sql"],
      [5, "005-usage.sql"],
    ] as const) {
      if (
        !(
          await client.query(
            "SELECT 1 FROM schema_migrations WHERE version=$1",
            [version],
          )
        ).rowCount
      )
        await client.query(
          await readFile(new URL(file, import.meta.url), "utf8"),
        );
    }
    await client.query(
      "INSERT INTO operators_config(singleton, operator_id, display_name) VALUES(true,$1,$2) ON CONFLICT DO NOTHING",
      [config.OPERATOR_ID, config.OPERATOR_NAME],
    );
    const { rows } = await client.query(
      "SELECT operator_id FROM operators_config",
    );
    if (rows[0]?.operator_id !== config.OPERATOR_ID)
      throw new Error("Database belongs to another operator");
    await client.query("COMMIT");
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const config = loadConfig(process.env);
  const pool = new pg.Pool({ connectionString: config.DATABASE_URL });
  try {
    await migrate(pool, config);
    console.log("Schema version 5 ready");
  } finally {
    await pool.end();
  }
}
