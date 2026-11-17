import type pg from "pg";
// Caller holds the compute lock. Company cancellation is terminal; local
// interruptions may retry only within the immutable submitted attempt budget.
export async function transitionFailedJob(
  c: pg.PoolClient,
  job: string,
  epoch: number,
  terminal: "FAILED" | "CANCELED",
  trace: string,
  retryable = true,
) {
  const { rows } = await c.query(
    `UPDATE jobs SET state=CASE WHEN $4::boolean AND $2<max_attempts THEN 'QUEUED' ELSE $3 END,
    next_eligible_at=CASE WHEN $4::boolean AND $2<max_attempts THEN clock_timestamp()+LEAST(8,power(2,$2-1))::integer*interval '1 second' ELSE next_eligible_at END
    WHERE id=$1 AND state='RUNNING' RETURNING state`,
    [job, epoch, terminal, retryable],
  );
  const state = rows[0]?.state;
  if (state === "QUEUED")
    await c.query(
      "INSERT INTO job_events(job_id,state,trace_id) VALUES($1,'RETRY_QUEUED',$2)",
      [job, trace],
    );
  return state as "QUEUED" | "FAILED" | "CANCELED" | undefined;
}
