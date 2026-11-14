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
  return pool;
}
