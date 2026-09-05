# Controller operations

See [controller-first contracts, roles, migration and rollback](CONTROLLER-FIRST.md)
and [OpenAPI v1alpha2](openapi.yaml) for the current CLI/API contract.

The controller uses PostgreSQL for accounts, owner/user roles, hashed tokens,
encrypted provider aliases, fleet allocation, tasks and per-user update checkpoints.
The web application is configuration-only. Agent interaction uses the CLI.

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

Controller-initiated Railway SSH needs a dedicated registered key. Supply
`VMBOX_RAILWAY_SSH_PRIVATE_KEY_B64` securely; startup materializes it privately,
never in the worker image or volume. Native CLI clients separately need SSH
authentication; their controller token is not an SSH credential.

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
