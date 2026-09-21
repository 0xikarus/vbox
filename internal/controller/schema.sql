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
-- NULL means this task predates explicit agent activity reporting. New
-- deliveries and set_busy calls establish a durable true/false value.
ALTER TABLE box_tasks ADD COLUMN IF NOT EXISTS agent_busy boolean;
ALTER TABLE box_tasks ADD COLUMN IF NOT EXISTS agent_busy_updated_at timestamptz;
-- Identifies the submitted message which most recently set busy. Correlated
-- replies only clear that generation, so a late reply cannot hide newer work.
ALTER TABLE box_tasks ADD COLUMN IF NOT EXISTS agent_busy_message_id uuid;
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
ALTER TABLE box_messages ADD COLUMN IF NOT EXISTS chat_key text;
ALTER TABLE box_messages ADD COLUMN IF NOT EXISTS parent_message_id uuid REFERENCES box_messages(id) ON DELETE SET NULL;
ALTER TABLE box_messages ADD COLUMN IF NOT EXISTS thread_id uuid;
-- Historical messages become independent roots. We deliberately do not infer
-- parentage from timestamps or adjacency because that would fabricate links.
UPDATE box_messages SET thread_id=id WHERE thread_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS box_messages_chat_key_idx
  ON box_messages(account_id,chat_key) WHERE chat_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS box_messages_task_time_idx
  ON box_messages(account_id,task_id,created_at,id);
CREATE INDEX IF NOT EXISTS box_messages_delivery_idx
  ON box_messages(account_id,state,created_at,id);
CREATE INDEX IF NOT EXISTS box_messages_thread_time_idx
  ON box_messages(account_id,thread_id,created_at,id);
CREATE TABLE IF NOT EXISTS box_message_images (
  message_id uuid NOT NULL REFERENCES box_messages(id) ON DELETE CASCADE,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  image_id uuid NOT NULL REFERENCES run_once_images(id) ON DELETE RESTRICT,
  ordinal integer NOT NULL CHECK (ordinal BETWEEN 1 AND 1000),
  PRIMARY KEY(message_id,image_id),
  UNIQUE(message_id,ordinal)
);
CREATE INDEX IF NOT EXISTS box_message_images_account_idx
  ON box_message_images(account_id,message_id,ordinal);

CREATE TABLE IF NOT EXISTS chat_groups (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  created_by uuid NOT NULL REFERENCES users(id),
  name text NOT NULL,
  idempotency_key text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id,name)
);
ALTER TABLE chat_groups ADD COLUMN IF NOT EXISTS idempotency_key text;
CREATE UNIQUE INDEX IF NOT EXISTS chat_groups_idempotency_idx ON chat_groups(account_id,idempotency_key) WHERE idempotency_key IS NOT NULL;
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
ALTER TABLE chat_group_members ADD COLUMN IF NOT EXISTS subscription_mode text NOT NULL DEFAULT 'following'
  CHECK (subscription_mode IN ('following','mentions','every_message'));
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
ALTER TABLE chat_group_messages ADD COLUMN IF NOT EXISTS parent_message_id uuid REFERENCES chat_group_messages(id) ON DELETE SET NULL;
ALTER TABLE chat_group_messages ADD COLUMN IF NOT EXISTS thread_id uuid;
UPDATE chat_group_messages SET thread_id=id WHERE thread_id IS NULL;
CREATE INDEX IF NOT EXISTS chat_group_messages_thread_idx ON chat_group_messages(account_id,group_id,thread_id,created_at,id);
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

-- Direct worker enrollment is independent of controller/user access tokens.
-- The agent credential identifies exactly one pre-authorized compute slot.
CREATE TABLE IF NOT EXISTS direct_workers (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  slot_id uuid NOT NULL UNIQUE REFERENCES compute_slots(id) ON DELETE CASCADE,
  enrollment_hash bytea UNIQUE,
  enrollment_expires_at timestamptz,
  credential_hash bytea UNIQUE,
  enrolled_at timestamptz,
  revoked_at timestamptz,
  incarnation text NOT NULL DEFAULT '',
  connection_owner text NOT NULL DEFAULT '',
  connection_epoch bigint NOT NULL DEFAULT 0,
  connection_expires_at timestamptz,
  transport_enabled boolean NOT NULL DEFAULT false,
  observation jsonb NOT NULL DEFAULT '{}',
  observed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (enrollment_hash IS NULL OR octet_length(enrollment_hash)=32),
  CHECK (credential_hash IS NULL OR octet_length(credential_hash)=32)
);
CREATE INDEX IF NOT EXISTS direct_workers_account_idx ON direct_workers(account_id);
ALTER TABLE direct_workers ADD COLUMN IF NOT EXISTS migration_sessions jsonb;
ALTER TABLE direct_workers ADD COLUMN IF NOT EXISTS bootstrap_deployment_id text;
ALTER TABLE direct_workers ADD COLUMN IF NOT EXISTS replacement_deployment_instance_id text;
ALTER TABLE direct_workers ADD COLUMN IF NOT EXISTS replacement_started_at timestamptz;

-- Credential digests, never Railway tokens. Quota survives process restarts.
CREATE TABLE IF NOT EXISTS railway_api_budgets (
 quota_scope text PRIMARY KEY CHECK (quota_scope ~ '^[a-f0-9]{64}$'),
 next_request_at timestamptz NOT NULL DEFAULT '1970-01-01T00:00:00Z',
 cooldown_until timestamptz NOT NULL DEFAULT '1970-01-01T00:00:00Z',
 remote_hourly_limit integer CHECK (remote_hourly_limit > 0)
);
CREATE TABLE IF NOT EXISTS railway_api_requests (
 quota_scope text NOT NULL REFERENCES railway_api_budgets(quota_scope) ON DELETE CASCADE,
 requested_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS railway_api_requests_scope_time ON railway_api_requests(quota_scope,requested_at);
ALTER TABLE railway_api_requests ADD COLUMN IF NOT EXISTS background boolean NOT NULL DEFAULT false;
-- A single active connection owner is required until cross-controller routing is
-- implemented. A second process cannot silently host an unreachable worker hub.
CREATE TABLE IF NOT EXISTS direct_worker_controller_lease (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  owner text NOT NULL,
  expires_at timestamptz NOT NULL
);

ALTER TABLE direct_workers ADD COLUMN IF NOT EXISTS observation_epoch bigint NOT NULL DEFAULT 0;
ALTER TABLE direct_workers ADD COLUMN IF NOT EXISTS observation_sequence bigint NOT NULL DEFAULT 0;

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

-- Short-lived, account-scoped desktop replay. Each frame is a bounded JPEG
-- captured inside the running box, never from a browser VNC canvas.
CREATE TABLE IF NOT EXISTS desktop_replay_frames (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
 captured_at timestamptz NOT NULL,
 width integer NOT NULL CHECK (width BETWEEN 1 AND 4096),
 height integer NOT NULL CHECK (height BETWEEN 1 AND 4096),
 data bytea NOT NULL CHECK (octet_length(data) BETWEEN 1 AND 262144),
 UNIQUE(box_id,captured_at)
);
CREATE INDEX IF NOT EXISTS desktop_replay_frames_recent_idx
 ON desktop_replay_frames(account_id,box_id,captured_at DESC);

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

-- Web push keeps one controller-wide VAPID keypair; browsers register
-- per-endpoint subscriptions that receive agent reply notifications.
CREATE TABLE IF NOT EXISTS web_push_settings (
 id boolean PRIMARY KEY DEFAULT true CHECK (id),
 vapid_public text NOT NULL,
 vapid_private text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS push_subscriptions (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 endpoint text NOT NULL,
 p256dh text NOT NULL,
 auth text NOT NULL,
 user_agent text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(account_id, endpoint)
);
CREATE INDEX IF NOT EXISTS push_subscriptions_account_idx ON push_subscriptions(account_id);

-- Webhooks only queue metadata refresh hints; lifecycle authority remains with
-- fenced database state and fresh provider evidence.
CREATE TABLE IF NOT EXISTS railway_refresh_hints (
 event_hash text PRIMARY KEY CHECK(event_hash ~ '^[a-f0-9]{64}$'),
 delivery_hash text NOT NULL CHECK(delivery_hash ~ '^[a-f0-9]{64}$'),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 slot_id uuid NOT NULL REFERENCES compute_slots(id) ON DELETE CASCADE,
 provider_credential text NOT NULL,
 project_id text NOT NULL, environment_id text NOT NULL, service_id text NOT NULL,
 deployment_id text NOT NULL, event_type text NOT NULL, event_timestamp timestamptz NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','claimed','done')),
 claim_owner text, claim_expires_at timestamptz,
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 received_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS railway_refresh_hints_pending_idx ON railway_refresh_hints(state,next_attempt_at,received_at);
CREATE INDEX IF NOT EXISTS railway_refresh_hints_scope_idx ON railway_refresh_hints(account_id,provider_credential,project_id,environment_id,service_id,state);

-- Instruction presets hold trusted user-authored Markdown guidance, kept
-- deliberately separate from encrypted login profiles and credentials.
CREATE TABLE IF NOT EXISTS instruction_presets (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  name text NOT NULL,
  markdown text NOT NULL,
  revision bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(account_id,name)
);
CREATE TABLE IF NOT EXISTS instruction_preset_defaults (
  account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
  preset_name text NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY(account_id,preset_name) REFERENCES instruction_presets(account_id,name) ON DELETE CASCADE
);
-- Boxes snapshot their selected Markdown with preset/version provenance.
-- Snapshots never reference live preset contents: preset edits or deletion
-- must not silently modify existing boxes.
CREATE TABLE IF NOT EXISTS box_instruction_snapshots (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  source text NOT NULL CHECK (source IN ('none','preset','custom')),
  preset_name text,
  preset_revision bigint,
  modified boolean NOT NULL DEFAULT false,
  markdown text NOT NULL,
  tool_guidance text NOT NULL DEFAULT '',
  applied_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(account_id,box_id)
);
-- Existing boxes keep empty guidance; only new creations populate this field.
ALTER TABLE box_instruction_snapshots ADD COLUMN IF NOT EXISTS tool_guidance text NOT NULL DEFAULT '';
-- Directed, owner-managed contact overrides. A missing row means inherit,
-- can_message=true means allow, and can_message=false means block. The legacy
-- can_receive value was never part of delivery authorization and remains only
-- so existing rows can be retained without inventing reciprocal permissions.
CREATE TABLE IF NOT EXISTS box_contacts (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  contact_box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  can_message boolean NOT NULL DEFAULT true,
  can_receive boolean NOT NULL DEFAULT true,
  created_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(box_id, contact_box_id),
  CHECK (box_id <> contact_box_id)
);
CREATE INDEX IF NOT EXISTS box_contacts_owner_idx ON box_contacts(account_id, box_id);
CREATE INDEX IF NOT EXISTS box_contacts_target_idx ON box_contacts(account_id, contact_box_id);
-- Older controllers created only the requested direction. Complete those
-- relationships on upgrade, swapping the directional permission flags.
INSERT INTO box_contacts(account_id,box_id,contact_box_id,can_message,can_receive,created_by,created_at,updated_at)
SELECT account_id,contact_box_id,box_id,can_receive,can_message,created_by,created_at,updated_at
FROM box_contacts
ON CONFLICT(box_id,contact_box_id) DO NOTHING;

-- Native agent roles are account scoped and deliberately have no built-in
-- names. Permission keys and typed JSON configs are validated by the
-- controller's catalogue rather than inferred from those names.
CREATE TABLE IF NOT EXISTS agent_roles (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  name text NOT NULL,
  description text NOT NULL DEFAULT '',
  created_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id,name)
);
CREATE TABLE IF NOT EXISTS agent_role_permissions (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  role_id uuid NOT NULL REFERENCES agent_roles(id) ON DELETE CASCADE,
  permission text NOT NULL,
  scope text NOT NULL,
  config jsonb NOT NULL DEFAULT '{}'::jsonb,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(role_id,permission)
);
CREATE INDEX IF NOT EXISTS agent_role_permissions_account_idx ON agent_role_permissions(account_id,role_id);
CREATE TABLE IF NOT EXISTS agent_role_contact_grants (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  role_id uuid NOT NULL REFERENCES agent_roles(id) ON DELETE CASCADE,
  contact_box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  PRIMARY KEY(role_id,contact_box_id)
);
CREATE INDEX IF NOT EXISTS agent_role_contact_grants_account_idx ON agent_role_contact_grants(account_id,role_id);
CREATE TABLE IF NOT EXISTS box_role_assignments (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  role_id uuid NOT NULL REFERENCES agent_roles(id) ON DELETE CASCADE,
  assigned_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(box_id,role_id)
);
CREATE INDEX IF NOT EXISTS box_role_assignments_account_idx ON box_role_assignments(account_id,box_id);

-- Agent-initiated work is durable and fenced to an assignment generation.
-- The run budget is independent of desktop inactivity and only has a live
-- deadline while the box is allocated.
CREATE TABLE IF NOT EXISTS agent_run_budgets (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  assignment_generation bigint NOT NULL,
  remaining_seconds bigint NOT NULL CHECK (remaining_seconds >= 0),
  deadline_at timestamptz,
  extension_seconds bigint NOT NULL DEFAULT 0 CHECK (extension_seconds >= 0),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(account_id,box_id)
);
CREATE INDEX IF NOT EXISTS agent_run_budgets_deadline_idx ON agent_run_budgets(deadline_at) WHERE deadline_at IS NOT NULL;
CREATE TABLE IF NOT EXISTS agent_run_budget_extensions (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  assignment_generation bigint NOT NULL,
  idempotency_key text NOT NULL,
  minutes integer NOT NULL CHECK (minutes > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id,box_id,assignment_generation,idempotency_key)
);
CREATE TABLE IF NOT EXISTS agent_followups (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  created_by uuid NOT NULL REFERENCES users(id),
  assignment_generation bigint NOT NULL,
  text text NOT NULL,
  delay_seconds integer NOT NULL DEFAULT 0 CHECK (delay_seconds >= 0),
  due_at timestamptz NOT NULL,
  state text NOT NULL CHECK (state IN ('queued','delivering','delivered','canceled','failed')),
  idempotency_key text NOT NULL,
  failure_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id,box_id,idempotency_key)
);
ALTER TABLE agent_followups ADD COLUMN IF NOT EXISTS delay_seconds integer NOT NULL DEFAULT 0 CHECK (delay_seconds >= 0);
CREATE INDEX IF NOT EXISTS agent_followups_due_idx ON agent_followups(state,due_at);
CREATE TABLE IF NOT EXISTS agent_box_creations (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  creator_box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  created_box_id uuid REFERENCES logical_boxes(id) ON DELETE SET NULL,
  requested_name text NOT NULL,
  requested_agent text NOT NULL,
  requested_disk_gib bigint NOT NULL,
  requested_role_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  idempotency_key text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id,creator_box_id,idempotency_key)
);
ALTER TABLE agent_box_creations ADD COLUMN IF NOT EXISTS requested_agent text NOT NULL DEFAULT 'codex';
ALTER TABLE agent_box_creations ADD COLUMN IF NOT EXISTS requested_disk_gib bigint NOT NULL DEFAULT 10;
ALTER TABLE agent_box_creations ADD COLUMN IF NOT EXISTS requested_role_ids jsonb NOT NULL DEFAULT '[]'::jsonb;
CREATE TABLE IF NOT EXISTS agent_box_deletions (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  actor_box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  idempotency_key text NOT NULL,
  target_ref text NOT NULL,
  confirmation text NOT NULL,
  target_box_id uuid,
  target_name text,
  accepted boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(account_id,actor_box_id,idempotency_key)
);
CREATE TABLE IF NOT EXISTS agent_box_restarts (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  actor_box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  target_box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  target_ref text NOT NULL,
  target_name text NOT NULL,
  confirmation text NOT NULL,
  idempotency_key text NOT NULL,
  state text NOT NULL DEFAULT 'requested' CHECK (state IN ('requested','hibernating','allocating','complete','failed')),
  failure_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id,actor_box_id,idempotency_key)
);
CREATE TABLE IF NOT EXISTS agent_email_addresses (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  creator_box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  address text,
  provider_ref text,
  address_type text NOT NULL,
  domain text NOT NULL,
  local_part text NOT NULL DEFAULT '',
  state text NOT NULL CHECK (state IN ('provisioning','ready','failed')),
  idempotency_key text NOT NULL,
  failure_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id,creator_box_id,idempotency_key)
);
ALTER TABLE agent_email_addresses ADD COLUMN IF NOT EXISTS local_part text NOT NULL DEFAULT '';

-- No manager roles are migrated or synthesized. Existing explicit contact rows
-- already represent the send permissions enforced before this migration.
ALTER TABLE logical_boxes DROP COLUMN IF EXISTS role;

-- Owner-designated protected boxes are invisible and unreachable to agents.
CREATE TABLE IF NOT EXISTS box_protection (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  box_id uuid NOT NULL REFERENCES logical_boxes(id) ON DELETE CASCADE,
  protected_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(account_id, box_id)
);

-- Inter-box messages carry their origin; 'box' is a distinct direction so the
-- chat UI and prompts can attribute the sender instead of showing a false owner.
ALTER TABLE box_messages ADD COLUMN IF NOT EXISTS sender_box_id uuid REFERENCES logical_boxes(id) ON DELETE SET NULL;
DO $$ BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid='box_messages'::regclass AND conname='box_messages_direction_check'
      AND position('''box''' IN pg_get_constraintdef(oid))=0
  ) THEN
    ALTER TABLE box_messages DROP CONSTRAINT box_messages_direction_check;
    ALTER TABLE box_messages ADD CONSTRAINT box_messages_direction_check
      CHECK (direction IN ('user','system','agent','box'));
  END IF;
END $$;
