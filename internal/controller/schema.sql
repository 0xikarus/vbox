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
