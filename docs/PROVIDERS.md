# Provider behavior

All providers implement one lifecycle contract: validate, create/inspect/list, start/stop/resize/delete, storage, deploy, connect, logs, usage, exact argv, and reconciliation. Destructive operations compare account, box, and lease ownership before acting and are idempotent.

Docker and Railway use the upstream `node:22-bookworm-slim` image by
default. The CLI bootstraps the selected components plus tmux and
`vmbox-runtime` after the provider reports the workload ready; it does not
require a vmbox-owned registry image. Incus uses `images:ubuntu/24.04`.

For lower cold-start latency, run `make box-image` to build the repository
Dockerfile locally. Railway requires a registry-published image: set
`VMBOX_IMAGE=REGISTRY/IMAGE:TAG` and run `make box-image-push`, then use the
resulting immutable `REGISTRY/IMAGE@sha256:...` reference in the Railway
context. Credentials and application profiles are synchronized at runtime and
must never be baked into this image.

The full worker image contains Node.js, Bun, Codex CLI, Claude Code,
OpenCode, Foundry/Forge, Railway CLI, GitHub CLI, Git, tmux, SSH client,
`vmbox-runtime`, and the controller binary, plus their required Debian runtime
utilities. Exact versions and fingerprints are recorded inside each image at
`/usr/local/lib/vmbox-image-manifest`. Each build step removes installer
caches and all known credential directories under `/root`; `/data/home` and
`/data/workspace` are empty at publication time.

## Docker

- Local engine, rootless engine, named contexts, SSH, and mTLS are supported; unauthenticated TCP is rejected.
- `docker exec` is the default transport; no SSH daemon is required.
- An isolated internal network and labeled `/data` volume are created per box.
- Cleanup verifies labels on the exact container, network, and volume.

## Railway

- A service and ready `/data` volume are created before deployment.
- The exact submitted deployment ID is polled to terminal readiness; interrupted submissions are reconciled only when one unambiguous new deployment exists.
- `RAILWAY_API_TOKEN` is supplied only in the Railway subprocess environment, never in argv or saved context data.
- Standalone contexts may explicitly select `--railway-cli-auth` to reuse an existing `railway login` session; controller credentials still require a token.
- Ownership is held in provider variables and never includes credentials.
- SSH invokes `vmbox-runtime` with encoded JSON argv, avoiding shell parsing.
- Railway SSH uses a vmbox-only known-hosts file. Endpoint rotation is retried by removing only the stale Railway entry from that isolated file; normal `~/.ssh/known_hosts` is never changed.
- Stop preserves service and storage; cleanup deletes the verified service before its exact attached volume.

## Ubuntu/Incus

- Unprivileged system containers are the default; `--incus-vm` opts into QEMU.
- Limits and a custom `/data` volume are applied with ownership config.
- Direct standalone operation uses a configured Incus remote; controller hosts use `vmbox-hostd` so workloads never receive the Incus socket.
