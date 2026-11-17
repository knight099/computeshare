import pg from "pg";

export function createDatabasePool(
  connectionString: string,
  onIdleError: () => void = () => {
    console.error(
      JSON.stringify({
        event: "DATABASE_CONNECTION_LOST",
        timestamp: new Date().toISOString(),
      }),
    );
  },
) {
  const pool = new pg.Pool({
    connectionString,
    connectionTimeoutMillis: 2000,
    query_timeout: 2000,
    statement_timeout: 2000,
    max: 10,
  });
  // Idle connection failures are EventEmitter errors and otherwise terminate Node.
  pool.on("error", onIdleError);
  // Checked-out clients also emit errors between queries. Keep these from
  // terminating Node; pg rejects further queries, transaction callers roll
  // back/release, and the pool discards the broken connection on release.
  // Idle failures still reach the pool listener above exactly once.
  pool.on("connect", (client) => client.on("error", () => {}));
  return pool;
}
