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
CREATE TABLE IF NOT EXISTS login_profiles (
  account_id uuid NOT NULL REFERENCES accounts(id),
  application text NOT NULL CHECK (application IN ('claude', 'codex')),
  name text NOT NULL,
  encrypted_value text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(account_id, application, name)
);
ALTER TABLE login_profiles DROP CONSTRAINT IF EXISTS login_profiles_application_check;
ALTER TABLE login_profiles ADD CONSTRAINT login_profiles_application_check CHECK (application IN ('claude','codex','opencode','github'));
CREATE TABLE IF NOT EXISTS notification_destinations (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id),
  kind text NOT NULL CHECK (kind IN ('webhook', 'discord')),
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
ALTER TABLE fleet_settings ADD COLUMN IF NOT EXISTS region text NOT NULL DEFAULT '';
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
  default_agent text NOT NULL DEFAULT 'claude' CHECK (default_agent IN ('codex','claude','opencode','shell')),
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
ALTER TABLE logical_boxes ADD COLUMN IF NOT EXISTS default_agent text NOT NULL DEFAULT 'claude' CHECK (default_agent IN ('codex','claude','opencode','shell'));
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

CREATE TABLE IF NOT EXISTS box_tasks (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  logical_box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id),
  requested_role text NOT NULL CHECK (requested_role IN ('owner','user')),
  agent text NOT NULL CHECK (agent IN ('codex','claude','opencode','shell')),
  session_name text NOT NULL DEFAULT 'vmbox',
  prompt text NOT NULL,
  state text NOT NULL CHECK (state IN ('queued','waiting_capacity','starting','active','failed','cancelled')),
  idempotency_key text NOT NULL,
  failure_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id,idempotency_key)
);
ALTER TABLE box_tasks ADD COLUMN IF NOT EXISTS requested_role text NOT NULL DEFAULT 'user' CHECK (requested_role IN ('owner','user'));
ALTER TABLE box_tasks ALTER COLUMN requested_role DROP DEFAULT;
CREATE INDEX IF NOT EXISTS box_tasks_reconcile_idx
  ON box_tasks(account_id,state,created_at,id);
-- One-shot process execution is deliberately separate from historical
-- interactive tasks. Process exit is not a claim about prompt completion.
CREATE TABLE IF NOT EXISTS process_tasks (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  logical_box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id),
  requested_role text NOT NULL,
  idempotency_key text NOT NULL,
  state text NOT NULL,
  result jsonb NOT NULL,
  auto_checked boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS process_tasks_pending_idx ON process_tasks(created_at) WHERE NOT auto_checked;
CREATE TABLE IF NOT EXISTS run_once_requests (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id),
 user_id uuid NOT NULL REFERENCES users(id),
 request_key text NOT NULL,
 request jsonb NOT NULL,
 state text NOT NULL DEFAULT 'queued',
 box_id uuid,
 failure text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(account_id,request_key)
);
-- Completed one-shot results outlive their disposable box and volume.
ALTER TABLE run_once_requests ADD COLUMN IF NOT EXISTS result jsonb;
CREATE TABLE IF NOT EXISTS run_once_images (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 media_type text NOT NULL,
 data bytea NOT NULL,
 download_token text NOT NULL,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS box_messages (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  task_id uuid NOT NULL REFERENCES box_tasks(id) ON DELETE CASCADE,
  user_id uuid REFERENCES users(id),
  direction text NOT NULL CHECK (direction IN ('user','system','agent')),
  body text NOT NULL,
  submit boolean NOT NULL DEFAULT true,
  state text NOT NULL CHECK (state IN ('queued','delivering','streaming','delivered','ambiguous','failed')),
  idempotency_key text NOT NULL,
  failure_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id,idempotency_key)
);
DO $$ BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid='box_messages'::regclass AND conname='box_messages_state_check'
      AND position('streaming' IN pg_get_constraintdef(oid))=0
  ) THEN
    ALTER TABLE box_messages DROP CONSTRAINT box_messages_state_check;
    ALTER TABLE box_messages ADD CONSTRAINT box_messages_state_check
      CHECK (state IN ('queued','delivering','streaming','delivered','ambiguous','failed'));
  END IF;
END $$;
CREATE INDEX IF NOT EXISTS box_messages_task_time_idx
  ON box_messages(account_id,task_id,created_at,id);
CREATE INDEX IF NOT EXISTS box_messages_delivery_idx
  ON box_messages(account_id,state,created_at,id);

CREATE TABLE IF NOT EXISTS chat_groups (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  created_by uuid NOT NULL REFERENCES users(id),
  name text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id,name)
);
CREATE TABLE IF NOT EXISTS chat_group_members (
  group_id uuid NOT NULL REFERENCES chat_groups(id) ON DELETE CASCADE,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  logical_box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  agent text NOT NULL DEFAULT 'claude' CHECK (agent IN ('codex','claude','opencode','shell')),
  can_receive boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(group_id,logical_box_id)
);
CREATE INDEX IF NOT EXISTS chat_group_members_box_idx
  ON chat_group_members(account_id,logical_box_id);
CREATE TABLE IF NOT EXISTS chat_group_messages (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  group_id uuid NOT NULL REFERENCES chat_groups(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id),
  source_box_id uuid REFERENCES logical_boxes(id) ON DELETE SET NULL,
  body text NOT NULL,
  idempotency_key text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS chat_group_messages_time_idx
  ON chat_group_messages(account_id,group_id,created_at,id);
CREATE TABLE IF NOT EXISTS chat_group_deliveries (
  message_id uuid NOT NULL REFERENCES chat_group_messages(id) ON DELETE CASCADE,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  logical_box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  task_id uuid REFERENCES box_tasks(id) ON DELETE SET NULL,
  box_message_id uuid REFERENCES box_messages(id) ON DELETE SET NULL,
  state text NOT NULL CHECK (state IN ('queued','dispatching','delivered','ambiguous','failed')),
  failure_reason text,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(message_id,logical_box_id)
);
CREATE INDEX IF NOT EXISTS chat_group_deliveries_state_idx
  ON chat_group_deliveries(account_id,state,updated_at);
-- Additive controller-first metadata. No terminal transcripts are retained.
CREATE TABLE IF NOT EXISTS controller_defaults (
 account_id uuid PRIMARY KEY REFERENCES accounts(id),
 provider text NOT NULL,
 provider_credential text NOT NULL,
 FOREIGN KEY(account_id,provider,provider_credential) REFERENCES provider_credentials(account_id,provider,name)
);
CREATE TABLE IF NOT EXISTS coworker_settings (
  account_id uuid PRIMARY KEY REFERENCES accounts(id),
  enabled boolean NOT NULL DEFAULT false
);
CREATE TABLE IF NOT EXISTS coworkers (
  account_id uuid NOT NULL REFERENCES accounts(id),
  box_id uuid NOT NULL REFERENCES logical_boxes(id),
  token_hash bytea NOT NULL UNIQUE,
  encrypted_token text NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(account_id,box_id)
);
CREATE TABLE IF NOT EXISTS coworker_events (
  sequence bigserial PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id),
  recipient_box_id uuid NOT NULL,
  sender_box_id uuid,
  message_key text NOT NULL,
  kind text NOT NULL,
  data jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  acknowledged_at timestamptz,
  FOREIGN KEY(account_id,recipient_box_id) REFERENCES coworkers(account_id,box_id),
  FOREIGN KEY(account_id,sender_box_id) REFERENCES coworkers(account_id,box_id),
  UNIQUE(account_id,sender_box_id,message_key)
);
CREATE INDEX IF NOT EXISTS coworker_inbox ON coworker_events(account_id,recipient_box_id,sequence);
ALTER TABLE coworker_events ALTER COLUMN sender_box_id DROP NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS coworker_owner_message_key ON coworker_events(account_id,message_key) WHERE sender_box_id IS NULL;
CREATE TABLE IF NOT EXISTS coworker_boards (
  account_id uuid PRIMARY KEY REFERENCES accounts(id),
  revision bigint NOT NULL DEFAULT 0,
  board jsonb NOT NULL DEFAULT '{"tasks":[]}'::jsonb,
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- Retired feature: retain historical data for scoped box cleanup, but revoke
-- enrollment and discard recoverable credentials. No coworker API is exposed.
UPDATE coworker_settings SET enabled=false WHERE enabled;
UPDATE coworkers SET enabled=false,encrypted_token='' WHERE enabled OR encrypted_token<>'';
CREATE TABLE IF NOT EXISTS session_observations (
 account_id uuid NOT NULL,
 box_id uuid NOT NULL,
 incarnation text NOT NULL,
 session_name text NOT NULL,
 fingerprint text NOT NULL,
 state text NOT NULL,
 revision uuid NOT NULL,
 sequence bigserial NOT NULL,
 observed_at timestamptz NOT NULL,
 partial boolean NOT NULL DEFAULT false,
 PRIMARY KEY(account_id,box_id,incarnation)
);
CREATE TABLE IF NOT EXISTS session_probe_watermarks (
 account_id uuid NOT NULL,
 box_id uuid NOT NULL,
 observed_at timestamptz NOT NULL,
 PRIMARY KEY(account_id,box_id)
);
CREATE TABLE IF NOT EXISTS session_acknowledgements (
 account_id uuid NOT NULL,
 user_id uuid NOT NULL,
 box_id uuid NOT NULL,
 incarnation text NOT NULL,
 sequence bigint NOT NULL,
 PRIMARY KEY(account_id,user_id,box_id,incarnation)
);

-- Per-agent browser credentials. Metadata is returned separately from ciphertext.
CREATE TABLE IF NOT EXISTS desktop_secrets (
  account_id uuid NOT NULL REFERENCES accounts(id),
  box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  secret_key text NOT NULL,
  origin text NOT NULL,
  encrypted_value text NOT NULL,
  creator_id uuid NOT NULL REFERENCES users(id),
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','confirmed')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(account_id,box_id,secret_key)
);

-- One scoped desktop MCP credential per box; old assignments cannot reuse it.
CREATE TABLE IF NOT EXISTS desktop_agent_tokens (
  box_id uuid PRIMARY KEY REFERENCES logical_boxes(id) ON DELETE CASCADE,
  account_id uuid NOT NULL REFERENCES accounts(id),
  user_id uuid NOT NULL REFERENCES users(id),
  fencing_token text NOT NULL,
  token_hash bytea NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS browser_state_imports (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id),
 box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
 encrypted_value text NOT NULL,
 origins jsonb NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','applied')),
 created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS box_notes (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id),
 box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES users(id),
 body text NOT NULL,
 idempotency_key text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(account_id,idempotency_key)
);

CREATE TABLE IF NOT EXISTS desktop_secret_requests (
 account_id uuid NOT NULL REFERENCES accounts(id),
 box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
 secret_key text NOT NULL,
 origin text NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','fulfilled','cancelled')),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(account_id,box_id,secret_key)
);

-- Existing boxes retain their stop policy; new boxes default to four hours.
ALTER TABLE logical_boxes ADD COLUMN IF NOT EXISTS idle_timeout_seconds integer NOT NULL DEFAULT 0 CHECK (idle_timeout_seconds BETWEEN 0 AND 604800);
ALTER TABLE logical_boxes ALTER COLUMN idle_timeout_seconds SET DEFAULT 14400;
