CREATE TABLE IF NOT EXISTS availability_sessions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 device_id uuid NOT NULL REFERENCES devices(id),
 starts_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL,
 requested_duration_seconds integer NOT NULL CHECK(requested_duration_seconds IN (3600,7200,14400,21600,28800,43200)),
 status text NOT NULL CHECK(status IN ('ACTIVE','STOPPED','EXPIRED')),
 policy_snapshot jsonb NOT NULL,
 consent_version text NOT NULL,
 stopped_at timestamptz,
 CHECK(expires_at>starts_at)
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_session_per_device ON availability_sessions(device_id) WHERE status='ACTIVE';
CREATE TABLE IF NOT EXISTS jobs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 submitter_id uuid NOT NULL REFERENCES users(id),
 idempotency_key text NOT NULL,
 workload_type text NOT NULL CHECK(workload_type='synthetic-hash-v1'),
 payload jsonb NOT NULL,
 timeout_seconds integer NOT NULL CHECK(timeout_seconds BETWEEN 1 AND 30),
 state text NOT NULL CHECK(state IN ('QUEUED','RUNNING','VERIFYING','SUCCEEDED','FAILED','CANCELED')),
 result_hash text,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(submitter_id,idempotency_key)
);
CREATE TABLE IF NOT EXISTS job_attempts (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 job_id uuid NOT NULL REFERENCES jobs(id),
 device_id uuid NOT NULL REFERENCES devices(id),
 session_id uuid NOT NULL REFERENCES availability_sessions(id),
 epoch integer NOT NULL CHECK(epoch>0),
 state text NOT NULL CHECK(state IN ('OFFERED','ACCEPTED','RUNNING','VERIFIED','FAILED','CANCELED','LOST')),
 lease_expires_at timestamptz NOT NULL,
 started_at timestamptz,
 finished_at timestamptz,
 result_hash text,
 UNIQUE(job_id,epoch)
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_attempt_per_device ON job_attempts(device_id) WHERE state IN ('OFFERED','ACCEPTED','RUNNING');
CREATE TABLE IF NOT EXISTS job_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 job_id uuid NOT NULL REFERENCES jobs(id),
 attempt_id uuid REFERENCES job_attempts(id),
 state text NOT NULL,
 trace_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS queued_jobs ON jobs(created_at) WHERE state='QUEUED';
CREATE INDEX IF NOT EXISTS session_history ON availability_sessions(device_id,starts_at);
INSERT INTO schema_migrations(version) VALUES(2) ON CONFLICT DO NOTHING;
