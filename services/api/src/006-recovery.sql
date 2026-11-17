ALTER TABLE jobs ADD COLUMN max_attempts integer NOT NULL DEFAULT 1 CHECK(max_attempts BETWEEN 1 AND 3);
ALTER TABLE jobs ADD COLUMN next_eligible_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX retry_eligible_jobs ON jobs(next_eligible_at,created_at,id) WHERE state='QUEUED';
INSERT INTO schema_migrations(version) VALUES(6);
