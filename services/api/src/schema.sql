CREATE TABLE IF NOT EXISTS schema_migrations (
  version integer PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS operators_config (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  operator_id uuid NOT NULL UNIQUE,
  display_name text NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  external_subject text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS consents (
  user_id uuid NOT NULL REFERENCES users(id),
  version text NOT NULL,
  accepted_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, version)
);
CREATE TABLE IF NOT EXISTS devices (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id uuid NOT NULL REFERENCES users(id),
  public_key text NOT NULL UNIQUE,
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  capabilities jsonb NOT NULL,
  revoked_at timestamptz,
  last_seen_at timestamptz,
  registered_at timestamptz NOT NULL DEFAULT now(),
  version integer NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS devices_owner_idx ON devices(owner_id);
CREATE TABLE IF NOT EXISTS device_capability_snapshots (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  device_id uuid NOT NULL REFERENCES devices(id),
  capabilities jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS device_challenges (
  device_id uuid PRIMARY KEY REFERENCES devices(id),
  nonce text NOT NULL,
  expires_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS device_credentials (
  token_hash text PRIMARY KEY,
  device_id uuid NOT NULL REFERENCES devices(id),
  expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS device_credentials_expiry_idx ON device_credentials(expires_at);
CREATE TABLE IF NOT EXISTS audit_events (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  actor_id text NOT NULL,
  action text NOT NULL,
  object_id text NOT NULL,
  trace_id text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO schema_migrations(version) VALUES (1) ON CONFLICT DO NOTHING;
