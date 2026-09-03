CREATE TABLE IF NOT EXISTS accounts (
  id uuid PRIMARY KEY,
  name text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS users (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id),
  subject text NOT NULL,
  role text NOT NULL CHECK (role IN ('owner', 'user')),
  disabled_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id, subject)
);
CREATE TABLE IF NOT EXISTS access_tokens (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id),
  user_id uuid NOT NULL REFERENCES users(id),
  token_hash bytea NOT NULL UNIQUE,
  expires_at timestamptz,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS provider_credentials (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id),
  provider text NOT NULL,
  name text NOT NULL,
  encrypted_value text NOT NULL,
  config jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id, provider, name)
);
CREATE TABLE IF NOT EXISTS notification_destinations (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id),
  kind text NOT NULL CHECK (kind IN ('webhook', 'telegram', 'discord')),
  name text NOT NULL,
  encrypted_secret text NOT NULL,
  config jsonb NOT NULL DEFAULT '{}'::jsonb,
  allowed_users jsonb NOT NULL DEFAULT '[]'::jsonb,
  allowed_chats jsonb NOT NULL DEFAULT '[]'::jsonb,
  enabled boolean NOT NULL DEFAULT true,
  last_error text,
  last_attempt_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id, kind, name)
);
CREATE TABLE IF NOT EXISTS runs (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id),
  user_id uuid NOT NULL REFERENCES users(id),
  provider text NOT NULL,
  box_id text,
  state text NOT NULL,
  request jsonb NOT NULL,
  external_reference text,
  idempotency_key text,
  lease text NOT NULL,
  summary text,
  exit_code integer,
  last_output_at timestamptz,
  last_heartbeat_at timestamptz,
  last_activity text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  started_at timestamptz,
  finished_at timestamptz,
  UNIQUE(account_id, idempotency_key)
);
CREATE TABLE IF NOT EXISTS events (
  id text PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id),
  run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  sequence bigint NOT NULL,
  type text NOT NULL,
  state text,
  stream text,
  message text,
  data jsonb,
  signature text,
  timestamp timestamptz NOT NULL,
  UNIQUE(run_id, sequence)
);
CREATE INDEX IF NOT EXISTS events_run_time_idx ON events(account_id, run_id, timestamp);
CREATE TABLE IF NOT EXISTS questions (
  id text PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id),
  run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  prompt text NOT NULL,
  state text NOT NULL DEFAULT 'open',
  blocking boolean NOT NULL DEFAULT true,
  asked_at timestamptz NOT NULL,
  expires_at timestamptz,
  answer text,
  answered_at timestamptz,
  answered_by uuid REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS audit_log (
  sequence bigserial PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id),
  user_id uuid REFERENCES users(id),
  action text NOT NULL,
  target_type text NOT NULL,
  target_id text NOT NULL,
  detail jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS hosts (
  id text NOT NULL,
  account_id uuid NOT NULL REFERENCES accounts(id),
  capabilities jsonb NOT NULL,
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(account_id, id)
);
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled_at timestamptz;
ALTER TABLE events ALTER COLUMN id TYPE text USING id::text;
ALTER TABLE questions ALTER COLUMN id TYPE text USING id::text;

CREATE TABLE IF NOT EXISTS fleet_settings (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  provider text NOT NULL,
  provider_credential text NOT NULL DEFAULT '',
  compute_box_slots integer NOT NULL DEFAULT 4 CHECK (compute_box_slots BETWEEN 0 AND 32),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(account_id, provider, provider_credential)
);
CREATE TABLE IF NOT EXISTS compute_slots (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  provider text NOT NULL,
  provider_credential text NOT NULL DEFAULT '',
  ordinal integer NOT NULL CHECK (ordinal > 0),
  state text NOT NULL CHECK (state IN ('starting','free','reserved','occupied','draining','unhealthy','stopped')),
  service_id text,
  service_name text,
  deployment_instance_id text,
  region text,
  image text,
  image_version text,
  health text NOT NULL DEFAULT 'unknown',
  assignment_generation bigint NOT NULL DEFAULT 0,
  lease_owner text,
  lease_expires_at timestamptz,
  fencing_token text,
  failure_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id, provider, provider_credential, ordinal)
);
CREATE UNIQUE INDEX IF NOT EXISTS compute_slots_service_idx
  ON compute_slots(account_id, provider, service_id) WHERE service_id IS NOT NULL;
CREATE TABLE IF NOT EXISTS logical_boxes (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  owner_user_id uuid NOT NULL REFERENCES users(id),
  name text NOT NULL,
  provider text NOT NULL,
  provider_credential text NOT NULL DEFAULT '',
  state text NOT NULL CHECK (state IN ('detached','reserved','attaching','running','draining','hibernating','hibernated','deleting','failed')),
  volume_id text NOT NULL,
  volume_name text NOT NULL,
  slot_id uuid REFERENCES compute_slots(id),
  assignment_generation bigint NOT NULL DEFAULT 0,
  lease_owner text,
  lease_expires_at timestamptz,
  fencing_token text,
  restoration_state text,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  failure_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id, name),
  UNIQUE(slot_id),
  UNIQUE(account_id, provider, volume_id)
);
CREATE TABLE IF NOT EXISTS allocation_requests (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  logical_box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  state text NOT NULL CHECK (state IN ('queued','reserved','attaching','ready','failed','cancelled')),
  idempotency_key text NOT NULL,
  requested_by uuid NOT NULL REFERENCES users(id),
  slot_id uuid REFERENCES compute_slots(id),
  assignment_generation bigint,
  fencing_token text,
  failure_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id, idempotency_key)
);
ALTER TABLE allocation_requests ADD COLUMN IF NOT EXISTS phase text;
ALTER TABLE allocation_requests ADD COLUMN IF NOT EXISTS retry_count integer NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS allocation_requests_queue_idx
  ON allocation_requests(account_id, state, created_at, id);
CREATE INDEX IF NOT EXISTS logical_boxes_detached_idx
  ON logical_boxes(account_id, provider, provider_credential, state);
