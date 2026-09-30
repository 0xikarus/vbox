# Controller operations

The controller owns accounts, encrypted credentials, worker pools, box storage,
allocation, and lifecycle state. It uses PostgreSQL and serves the web UI and API;
the CLI connects to the same controller. See the [API specification](openapi.yaml)
and [architecture guide](AGENT-GUIDE.md) for the detailed contracts.

## Configure the controller

```text
DATABASE_URL=<PostgreSQL connection string>
VMBOX_CONTROLLER_LISTEN=:8080
VMBOX_CONTROLLER_URL=https://controller.example
VMBOX_ENCRYPTION_KEY=<base64-encoded 32-byte key>
```

Run behind HTTPS. `VMBOX_CONTROLLER_URL` must be reachable by workers that call
back to the controller. The controller image must contain a matching
`/usr/local/bin/vmbox-runtime`; agent-backed dedicated workers also require
`/usr/local/bin/vmbox-worker-agent`. Keep the database URL, encryption key, and
account tokens out of images, logs, and worker environments. Account bootstrap
prints the owner token once; store it securely.

The repository Dockerfile includes the controller and matching runtime. Its
default entrypoint starts a worker, so override the entrypoint with
`/usr/local/bin/vmbox-controller` for a controller deployment. Point it at a
PostgreSQL database; migrations run at startup. Before signing in, run the same
binary once with the `bootstrap` argument and the same `DATABASE_URL` to create
the first owner. Capture the token privately. A reverse proxy should terminate
HTTPS at the public `VMBOX_CONTROLLER_URL`.

Choose a worker path before allocating boxes:

| Worker path | Setup |
| --- | --- |
| Self-hosted Linux worker with one container per box | [Linux VPS guide](LINUX-VPS-SETUP.md) |
| Shared worker with trusted boxes on one host | [Shared workers](SHARED-WORKERS.md) |
| Dedicated Railway services and volumes | [Railway provider](PROVIDERS.md#railway) |

The [provider guide](PROVIDERS.md) also describes the Docker and Incus adapters.
Provider aliases, credentials, and the default pool are managed on the controller.
Creating or editing an alias does not deploy workers. Set capacity explicitly;
`VMBOX_INITIAL_COMPUTE_BOX_SLOTS` can seed initial capacity but does not override
later owner changes. Capacity changes can incur hosting charges.

```bash
vbox users add alice --role user
vbox users list
vbox providers schema
vbox providers list
vbox pools create
vbox pools default
vbox fleet status
```

Pass provider secrets through the CLI's named secure environment input, never as
command arguments or in provider config JSON. The controller encrypts stored
secrets. Check each provider's prerequisites before selecting it as the default.

Notifications can be listed, configured, and tested with
`vbox notifications list|setup|test`. Webhook and Discord secrets are JSON
objects supplied through named secure environment variables; Discord also
requires user and chat/channel allowlists.

## Run and maintain boxes

An allocation binds a box and its persistent storage to a compute slot. A
hibernated box retains its files but not its processes. Closing a browser or CLI
viewer leaves a running box alone. Deleting a box removes its files, so confirm
the exact target and account before acting. A controller restart should not
restart remote tmux sessions; do not restart workers to test controller recovery.

Fresh dedicated allocations bind native runtime assignments automatically.
Existing running boxes may require the explicit owner operation
`vbox sessions BOX --enable` after an approved controller rollout. Record
process and session identities before and after; this operation should not
restart worker compute.

The optional direct-worker transport is currently used for enrolled dedicated
Railway workers. With `VMBOX_DIRECT_WORKERS=1`, those workers connect to the
controller through outbound authenticated WebSockets for terminal, desktop,
file, and runtime traffic. The [Railway provider notes](PROVIDERS.md#railway)
cover enrollment, infrastructure calls, SSH bootstrap, and webhooks. A worker
that loses its authenticated connection fails closed; the controller does not
silently replay terminal input or assume a task completed.

For operations that create resources, use explicitly disposable workers, boxes,
and databases. PostgreSQL integration tests require `VMBOX_TEST_DATABASE_URL`
pointing to a disposable database. A green run with those tests skipped is not
database evidence. See [verification guidance](AGENT-GUIDE.md#verification-and-honest-evidence).
