# Controller operations

See [controller-first contracts, roles, migration and rollback](CONTROLLER-FIRST.md)
and [OpenAPI v1alpha2](openapi.yaml) for the current CLI/API contract.

The controller uses PostgreSQL for accounts, owner/user roles, hashed tokens,
encrypted provider aliases, fleet allocation, tasks and per-user update checkpoints.
The web application includes per-box desktop and terminal views; the CLI uses
the same controller for box access.

Codex, Claude, and OpenCode are supported for persistent managed sessions,
Agent chat, Run once, desktop MCP registration, and encrypted saved profiles.
OpenCode starts with `--auto`; its first Agent-chat message is a native startup
prompt and later messages use its loopback session API.

Required operator configuration:

```text
DATABASE_URL
VMBOX_CONTROLLER_LISTEN=:8080
VMBOX_CONTROLLER_URL=https://controller.example
VMBOX_IMAGE=ghcr.io/owner/vmbox-service@sha256:...
VMBOX_ENCRYPTION_KEY=<secure base64-encoded 32-byte key>
```

Use TLS at ingress. Never put credentials in images, contexts, reusable profiles,
logs or workload environment. Controller account bootstrap prints the owner token
once; store it securely. Provider accounts and defaults are managed on the
controller, through either CLI or the lightweight web forms.

For a single-account Railway controller, startup can seed an absent encrypted
`railway/primary` credential from `RAILWAY_API_TOKEN` or `RAILWAY_TOKEN`.
The latter is a project token and uses the project-access header. Configure
`RAILWAY_PROJECT_ID` and `RAILWAY_ENVIRONMENT_ID` explicitly. Existing owner-managed
credentials are not overwritten by environment seeding. Some account capabilities
cannot be proven by a read-only validation probe.

HTTP Railway management requests share a PostgreSQL budget keyed by credential
digest, including across aliases and controller restarts. Defaults are 80 requests
per rolling hour and at least one second between requests. Operators can set
`VMBOX_RAILWAY_API_HOURLY_LIMIT` (1–100000) and
`VMBOX_RAILWAY_API_INTERVAL` (20ms–1m) to fit their Railway quota. Provider response
headers can lower the allowance and impose a shared cooldown; database failures
deny requests. Foreground and background requests share the total limit, with
reservations for each class when the effective allowance exceeds one request.
Inventory refreshes are coalesced and cached. Legacy logs and billing CLI paths
are outside this HTTP budget; they are not used for direct box runtime access.

Controller-initiated Railway SSH needs a dedicated registered key. Supply
`VMBOX_RAILWAY_SSH_PRIVATE_KEY_B64` securely; startup materializes it privately,
never in the worker image or volume. Native CLI connections to enrolled workers
use controller authentication. Legacy worker access and Railway port forwarding
still need a separate registered SSH key; a controller token is not an SSH key.

`VMBOX_INITIAL_COMPUTE_BOX_SLOTS` optionally seeds initial capacity. Once explicitly
configured, environment changes do not overwrite it. Capacity edits can create
billable compute; provider config edits alone never trigger deployment or scaling.

The separately built `vmbox-bootstrap` operator executable retains explicit
Railway controller provisioning; ordinary `vmbox` never invokes it. See the
migration guide for build flags. Production deployment remains an explicit operator
action, not an implicit consequence of running CLI tests.

```bash
vmbox users add alice --role user
vmbox users list
vmbox providers schema
vmbox providers list
vmbox providers validate railway primary
vmbox notifications list
vmbox notifications setup webhook ops --secret-env WEBHOOK_SECRET_JSON
vmbox notifications test webhook ops
```

Webhook and Discord secret inputs are JSON objects from named secure
environment variables. Discord also require user and chat/channel
allowlists. Notification/integration message APIs and historical groups remain
supported backend records; removing their web views does not erase them.

For already-running boxes, enable the new native runtime explicitly after an
approved controller rollout: `vmbox sessions BOX --enable`. Record process and
session identities before/after. This operation does not restart worker compute.
Fresh allocations bind the native assignment automatically.

Controller restart should not affect remote tmux. Hibernation is different:
it saves/restores workspace state but cannot preserve live process identities.
Never use a worker restart as evidence of controller-only recovery.

## Direct worker transport rollout

Set `VMBOX_DIRECT_WORKERS=1` on the controller and use one controller replica.
The controller image must contain its matching `/usr/local/bin/vmbox-worker-agent`
and `/usr/local/bin/vmbox-runtime`; `VMBOX_CONTROLLER_URL` must be the externally
reachable HTTPS address. Workers establish outbound authenticated WebSockets to
that address. No per-worker public port is required.

The flag enables enrollment, activation and recovery. Disabling it does not
switch an already enabled worker back to Railway: disconnected workers fail
closed. Ordinary screenshots, input, files and terminals use the worker channel.
Infrastructure allocation and a fenced, one-time agent installation still need
Railway access, so a Railway cooldown can delay creation or migration.

For an existing running box, an owner can POST to
`/v1/worker-slots/{slot}/enrollment`, then POST to
`/v1/worker-slots/{slot}/activate` after the agent connects. Installation captures
native session identities and activation verifies they survived. Neither operation
restarts the worker. An ambiguous installation is retained for inspection;
`/v1/worker-slots/{slot}/recover` probes the pinned deployment before recovery.
Do not blindly repeat bootstrap or restart compute to repair a disconnected agent.
See [delivery evidence](DIRECT-WORKER-ACCEPTANCE.md) for verification and rollout
steps that remain outstanding.

Optional `VMBOX_RAILWAY_WEBHOOK_SECRET` is a base64url-encoded 32-byte random secret.
Configure the Railway project webhook URL as
`https://CONTROLLER/v1/railway-webhooks/SECRET`, selecting deployment events.
Treat the full URL as a credential and redact it from proxy/access logs.
The receiver validates project, environment and service scope, persists deduplicated
refresh hints and refreshes inventory through the shared background request budget.
Events never directly change allocation readiness or authorize destructive actions.
Registration in Railway is separate from enabling the receiver.
