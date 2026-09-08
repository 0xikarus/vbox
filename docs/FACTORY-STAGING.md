# Factory feature-branch staging

Not a production rollout guide. Do not merge/deploy the factory without approval.
Build and test these binaries inside an isolated vmbox:

```sh
go build -o /data/workspace/bin/vmbox-factory ./cmd/vmbox-factory
go build -o /data/workspace/bin/vmbox-controller ./cmd/vmbox-controller
go build -o /data/workspace/bin/vmbox-planner ./cmd/vmbox-planner
bash scripts/test-factory-in-box.sh
```

The existing controller serves the Factory tab and authenticates browser cookies.
A separate factory process owns its PostgreSQL tables and private asset directory.
Give staging its own DB, server identity and storage. Do not point a test controller
at the production DB or turn on its fleet reconciler against production resources.

Configuration (names only; supply values through secure environment):

| Process | Variable | Purpose |
|---|---|---|
| Controller | `VMBOX_FACTORY_URL` | Trusted factory HTTP endpoint; same host loopback recommended |
| Both | `VMBOX_FACTORY_GATEWAY_TOKEN` | Shared random server-only token, at least 32 characters |
| Factory | `VMBOX_FACTORY_DATABASE_URL` | Dedicated PostgreSQL database |
| Factory | `VMBOX_FACTORY_DATA_DIR` | Dedicated absolute persistent private storage directory |
| Factory | `VMBOX_FACTORY_LISTEN` | Defaults to `127.0.0.1:8090` |
| Factory | `VMBOX_FACTORY_MAX_WORKERS` | Defaults to 3; current approved maximum is 6 |
| Factory | `VMBOX_FACTORY_CONTROLLER_URL` | Execution controller HTTPS URL |
| Factory | `VMBOX_FACTORY_CONTROLLER_TOKEN` | Server-only configured account credential; never forwarded to boxes |
| Factory | `VMBOX_FACTORY_ACCOUNT_ID` | Expected controller account; checked against whoami |
| Factory | `VMBOX_FACTORY_GITHUB_APP_ID` | Registered GitHub App identifier |
| Factory | `VMBOX_FACTORY_GITHUB_INSTALLATIONS` | Operator-approved JSON mapping controller account IDs to installation IDs |
| Factory | `VMBOX_FACTORY_ENCRYPTION_KEY` | Separate secret of at least 32 bytes for the App key envelope |
| Factory | `VMBOX_FACTORY_GITHUB_KEY_FILE` | Encrypted key envelope in a regular 0600 file |
| Factory | `VMBOX_FACTORY_EXECUTION_ENABLED` | Set exactly `true` to enable configured planning; otherwise saved work is browseable but Plan/reply are disabled |
| Factory | `VMBOX_FACTORY_PLANNER_BINARY` | Absolute path to the trusted planner binary built inside a vmbox |
| Factory | `VMBOX_FACTORY_SSH_IDENTITY` | Absolute path to a private SSH identity authorized for worker transport |
| Factory | `VMBOX_FACTORY_SSH_KNOWN_HOSTS` | Absolute path to a dedicated persistent known-hosts file |
| Factory | `VMBOX_FACTORY_RESULT_SECRET` | Persistent random secret of at least 32 bytes for per-attempt HMAC capabilities |
| Factory | `VMBOX_FACTORY_RESULT_URL` | HTTPS `/result` endpoint on this factory service, reachable from worker boxes |

With the App ID and encryption key securely configured, `vmbox-factory
seal-github-key` reads a PEM private key on stdin and emits only its encrypted
envelope on stdout. Save that envelope privately and set the key-file path.
Do not commit either plaintext keys, encryption keys, tokens, or real environment
files. Changing the App ID changes the authenticated encryption scope; re-encrypt
the key intentionally. App repository grants are rechecked on access.

The trusted operator allowlist is currently the installation onboarding mechanism;
an interactive GitHub installation callback/management UI remains to be added.
Explicitly enabled planning now connects the controller client, private SSH
stager, planner command and durable result inbox. Missing dependencies fail
startup instead of queuing requests forever. Merely browsing repositories or
persisted records is not proof of execution. Codex's local image adapter has real
visual-grounding evidence; Claude images remain disabled and its live profile
authentication needs verification. Only PNG/JPEG are advertised.

The factory service is trusted infrastructure, not an agent workspace. Keep its
controller token, SSH identity, App key and capability secret out of coding boxes.
Workers receive only the chosen saved agent login, a scoped repository-read grant
held during staging, and a per-attempt result capability in a private job file.
Their process-task prompt contains only a fixed invocation; actual prompts and
credentials travel in uncaptured SSH stdin. Inputs are staged as `vmbox`, matching
the controller's process execution user. The result capability does not grant
controller access. Do not put its header in HTTP access logs.

`/result` must reach the factory backend directly, not the cookie-authenticated
Factory gateway. Literal loopback HTTP is allowed only for isolated tests within
one host; it cannot deliver callbacks across separate boxes. Provision a trusted
HTTPS endpoint for real multi-box staging without changing the production controller.
Keep the factory process alive independently of one-shot workers' auto-hibernation.

Planning admission defaults to three concurrent work items and is capped at six;
it is enforced in PostgreSQL across coordinator processes. Expired observation
leases do not free admission. Long staging renews the same lease and cancels on
ownership loss. Successful result delivery is distinct from agent exit success;
missing reports stay unknown, never an invented completed plan.

The staging binary is create-only/pinned within each planning box. Upgrading it
requires an explicit quiesced lifecycle; do not overwrite binaries used by active
attempts. There is no automatic feature execution or GitHub publication yet.

Current proof and exact test revisions/handles are in `FACTORY-PROGRESS.md`.
Factory, controller, real PostgreSQL and local-Git staging tests run inside vmbox.
Worker GitHub/browser fixtures are not proof of a live authorized App installation
or the full UI-to-agent loop; those remain required acceptance tests.
# Approved-plan publication

Set `VMBOX_FACTORY_ISSUES_WRITE=true` on the separate factory service to enable
its durable issue-publication loop. This additionally requires the configured
planning backend, database, GitHub App and bound controller account; the App
must have Issues write permission for the authorized repository. No setting is
changed automatically. When disabled, capability `publicationReady` is false
and API/UI approval is unavailable, while planning and saved history remain.

Approval publishes the master, dependency-ordered feature issues and a linking
comment. Only then is work `build_queued`. This is not proof of implementation:
feature scheduler/PR/integration wiring remains separate. Ambiguous writes are
reconciled without another initial POST; blocked outcomes require investigation.
No factory branch deployment or live GitHub writes have been performed by this
integration. Test fixtures do not establish live App acceptance.
