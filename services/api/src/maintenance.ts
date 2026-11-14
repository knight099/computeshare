import type pg from "pg";

export async function cleanupExpiredDeviceAuth(db: Pick<pg.Pool, "query">) {
  const credentials = await db.query(
    "DELETE FROM device_credentials WHERE expires_at<=now()",
  );
  const challenges = await db.query(
    "DELETE FROM device_challenges WHERE expires_at<=now()",
  );
  return {
    credentials: credentials.rowCount ?? 0,
    challenges: challenges.rowCount ?? 0,
  };
}

export function startAuthMaintenance(pool: pg.Pool, onError: () => void) {
  let pending: Promise<unknown> | undefined;
  const tick = () => {
    if (pending) return;
    pending = cleanupExpiredDeviceAuth(pool)
      .catch(onError)
      .finally(() => {
        pending = undefined;
      });
  };
  tick();
  const timer = setInterval(tick, 60000);
  timer.unref();
  return async () => {
    clearInterval(timer);
    await pending;
  };
}
