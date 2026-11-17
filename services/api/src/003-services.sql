ALTER TABLE jobs ADD COLUMN expected_hash text CHECK(expected_hash IS NULL OR expected_hash ~ '^[a-f0-9]{64}$');
CREATE TABLE event_outbox (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 job_event_id bigint NOT NULL UNIQUE REFERENCES job_events(id),
 payload jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 published_at timestamptz,
 lease_owner uuid,
 lease_until timestamptz,
 publish_attempts integer NOT NULL DEFAULT 0 CHECK(publish_attempts>=0)
);
CREATE INDEX outbox_pending ON event_outbox(created_at,id) WHERE published_at IS NULL;
CREATE FUNCTION capture_job_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO event_outbox(job_event_id,payload) VALUES(NEW.id,jsonb_build_object(
  'protocol_version',1,'event_id',NEW.id::text,'job_id',NEW.job_id,'attempt_id',NEW.attempt_id,
  'state',NEW.state,'trace_id',NEW.trace_id,'occurred_at',NEW.created_at));
 RETURN NEW;
END $$;
CREATE TRIGGER job_event_outbox AFTER INSERT ON job_events FOR EACH ROW EXECUTE FUNCTION capture_job_event();
CREATE INDEX job_history ON jobs(created_at DESC,id DESC);
INSERT INTO schema_migrations(version) VALUES(3);
