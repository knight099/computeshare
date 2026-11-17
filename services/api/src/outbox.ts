import { randomUUID } from "node:crypto";
import type pg from "pg";
import { connect, RetentionPolicy, StorageType, nanos } from "nats";

type Envelope = { id: string; job_event_id: string; payload: unknown };
export function eventNamespace(operator: string) {
  if (
    !/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(
      operator,
    )
  )
    throw new Error("Invalid operator namespace");
  return {
    stream: "OC_" + operator.replaceAll("-", ""),
    subject: `opencompute.${operator}.jobs.events`,
  };
}

// Claims commit before broker IO. A crash after publish can replay an event;
// consumers must deduplicate by event_id even beyond JetStream's dedup window.
export async function deliverOutbox(
  pool: pg.Pool,
  publish: (event: Envelope) => Promise<void>,
) {
  const owner = randomUUID();
  const c = await pool.connect();
  let rows: Envelope[];
  try {
    await c.query("BEGIN");
    rows = (
      await c.query<Envelope>(
        `WITH pending AS (
      SELECT id FROM event_outbox WHERE published_at IS NULL AND (lease_until IS NULL OR lease_until<clock_timestamp())
      ORDER BY created_at,id LIMIT 10 FOR UPDATE SKIP LOCKED
    ) UPDATE event_outbox o SET lease_owner=$1,lease_until=clock_timestamp()+interval '60 seconds',publish_attempts=publish_attempts+1
      FROM pending WHERE o.id=pending.id RETURNING o.id,o.job_event_id::text,o.payload`,
        [owner],
      )
    ).rows;
    await c.query("COMMIT");
  } catch (e) {
    await c.query("ROLLBACK");
    throw e;
  } finally {
    c.release();
  }
  try {
    for (const event of rows) {
      await publish(event);
      await pool.query(
        "UPDATE event_outbox SET published_at=clock_timestamp(),lease_owner=NULL,lease_until=NULL WHERE id=$1 AND lease_owner=$2 AND published_at IS NULL",
        [event.id, owner],
      );
    }
    return rows.length;
  } finally {
    // If the DB is unavailable the finite lease provides crash recovery instead.
    await pool.query(
      "UPDATE event_outbox SET lease_owner=NULL,lease_until=NULL WHERE lease_owner=$1 AND published_at IS NULL",
      [owner],
    );
  }
}
export async function openEventPublisher(url: string, operator: string) {
  const ns = eventNamespace(operator);
  const nc = await connect({ servers: url, timeout: 2000, reconnect: false });
  try {
    const manager = await nc.jetstreamManager({ timeout: 2000 });
    let info;
    try {
      info = await manager.streams.info(ns.stream);
    } catch (e) {
      if ((e as { code?: string }).code !== "404") throw e;
      try {
        info = await manager.streams.add({
          name: ns.stream,
          subjects: [ns.subject],
          storage: StorageType.File,
          retention: RetentionPolicy.Limits,
          max_age: nanos(7 * 24 * 60 * 60 * 1000),
          max_bytes: 64 * 1024 * 1024,
          duplicate_window: nanos(120000),
        });
      } catch {
        // Another replica may have created it concurrently.
        info = await manager.streams.info(ns.stream);
      }
    }
    if (
      info.config.subjects?.length !== 1 ||
      info.config.subjects[0] !== ns.subject ||
      info.config.storage !== StorageType.File ||
      info.config.retention !== RetentionPolicy.Limits
    )
      throw new Error("Event stream configuration mismatch");
    const js = nc.jetstream({ timeout: 2000 });
    return {
      publish: async (event: Envelope) => {
        await js.publish(
          ns.subject,
          new TextEncoder().encode(JSON.stringify(event.payload)),
          { msgID: `${operator}:${event.job_event_id}`, timeout: 2000 },
        );
      },
      close: () => nc.close(),
    };
  } catch (e) {
    await nc.close();
    throw e;
  }
}
export function startEventOutbox(
  pool: pg.Pool,
  url: string,
  operator: string,
  onError: () => void,
) {
  let publisher: Awaited<ReturnType<typeof openEventPublisher>> | undefined;
  let pending: Promise<unknown> | undefined;
  const tick = () => {
    if (!pending)
      pending = (async () => {
        publisher ??= await openEventPublisher(url, operator);
        await deliverOutbox(pool, publisher.publish);
      })()
        .catch(async () => {
          await publisher?.close();
          publisher = undefined;
          onError();
        })
        .finally(() => {
          pending = undefined;
        });
  };
  tick();
  const timer = setInterval(tick, 500);
  timer.unref();
  return async () => {
    clearInterval(timer);
    await pending;
    await publisher?.close();
  };
}
