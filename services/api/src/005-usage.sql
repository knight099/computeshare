CREATE TABLE usage_records (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
 attempt_id uuid NOT NULL UNIQUE REFERENCES job_attempts(id),
 device_id uuid NOT NULL REFERENCES devices(id),
 session_id uuid NOT NULL REFERENCES availability_sessions(id),
 output_verified boolean NOT NULL CHECK(output_verified),
 logical_iterations integer NOT NULL CHECK(logical_iterations>0),
 cpu_usage_usec bigint CHECK(cpu_usage_usec>=0),
 memory_peak_bytes bigint CHECK(memory_peak_bytes>=0),
 server_elapsed_usec bigint NOT NULL CHECK(server_elapsed_usec>=0),
 telemetry_status text NOT NULL CHECK(telemetry_status IN ('NOT_REPORTED','REPORTED_UNTRUSTED','REJECTED')),
 rejection_reason text,
 policy_snapshot jsonb NOT NULL,
 observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((telemetry_status='NOT_REPORTED' AND cpu_usage_usec IS NULL AND memory_peak_bytes IS NULL) OR (telemetry_status<>'NOT_REPORTED' AND cpu_usage_usec IS NOT NULL AND memory_peak_bytes IS NOT NULL))
);
CREATE INDEX usage_history ON usage_records(observed_at DESC,id DESC);
CREATE INDEX usage_device_history ON usage_records(device_id,observed_at DESC,id DESC);
INSERT INTO schema_migrations(version) VALUES(5);
