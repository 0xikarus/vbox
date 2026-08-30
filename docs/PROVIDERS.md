# Provider behavior

All providers implement one lifecycle contract: validate, create/inspect/list, start/stop/resize/delete, storage, deploy, connect, logs, usage, exact argv, and reconciliation. Destructive operations compare account, box, and lease ownership before acting and are idempotent.

## Docker

- Local engine, rootless engine, named contexts, SSH, and mTLS are supported; unauthenticated TCP is rejected.
- `docker exec` is the default transport; no SSH daemon is required.
- An isolated internal network and labeled `/data` volume are created per box.
- Cleanup verifies labels on the exact container, network, and volume.

## Railway

- A service and ready `/data` volume are created before deployment.
- The exact submitted deployment ID is polled to terminal readiness; interrupted submissions are reconciled only when one unambiguous new deployment exists.
- `RAILWAY_API_TOKEN` is supplied only in the Railway subprocess environment, never in argv or saved context data.
- Ownership is held in provider variables and never includes credentials.
- SSH invokes `vmbox-runtime` with encoded JSON argv, avoiding shell parsing.
- Stop preserves service and storage; cleanup deletes the verified service before its exact attached volume.

## Sevalla

- One application per box, sourced from the selected Linux/AMD64 OCI image.
- Private image pulls can select a Sevalla `--docker-registry-credential-id`; controller credential config uses `dockerRegistryCredentialId`.
- Clusters and process resource types are discovered from `/v3/resources`.
- Direct commands use the documented `command: []` endpoint and return separate stdout, stderr, and exit status.
- Interactive sessions use the official authenticated WebSocket terminal and attach to the shared tmux runtime.
- Suspend/activate, deploy, logs, metrics, resize, rate-limit backoff, and asynchronous deletion reconciliation are implemented.
- Sevalla's public v3 API currently has no application-disk create/update/delete endpoint. Automated `/data` is therefore reported unavailable. A manually pre-attached disk can be declared with `--pre-attached-disk`; vmbox never calls undocumented APIs or scrapes the dashboard.

## Ubuntu/Incus

- Unprivileged system containers are the default; `--incus-vm` opts into QEMU.
- Limits and a custom `/data` volume are applied with ownership config.
- Direct standalone operation uses a configured Incus remote; controller hosts use `vmbox-hostd` so workloads never receive the Incus socket.
