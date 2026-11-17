CREATE TABLE device_work_requests (
 device_id uuid PRIMARY KEY REFERENCES devices(id),
 session_id uuid NOT NULL REFERENCES availability_sessions(id),
 revision integer NOT NULL DEFAULT 1 CHECK(revision>0),
 requested_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL
);
CREATE INDEX work_requests_expiry ON device_work_requests(expires_at);
INSERT INTO schema_migrations(version) VALUES(4);
