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
- Railway is the control plane, not the data path. The active deployment instance is resolved once per operation and cached; every remote command then streams over one direct OpenSSH `ControlMaster` whose `ControlPath` is a hashed, fixed-width name inside a private per-user directory, so it always fits a Unix socket address.
- The cached endpoint and its master are invalidated when the deployment identity changes and whenever SSH reports its own transport failure (exit 255), after which the operation retries once.
- Railway SSH uses a vmbox-only known-hosts file. Endpoint rotation is retried by removing only the stale Railway entry from that isolated file; normal `~/.ssh/known_hosts` is never changed.
- Remote work runs as the unprivileged `vmbox` user with `HOME=/data/home`. Railway's SSH data path lands as root, so agent profiles, GitHub configuration, instruction files, workspace files, and the tmux session that hosts an agent would otherwise all belong to root and be invisible to the agents. Credential verification is dropped to the same user, so a root-readable login can never report a false positive.
- Files installed from outside the box are handed to `vmbox:vmbox`: directories the runtime creates use `0700`, credentials use `0600`, and directories the box already had keep their mode and owner.
- A `/data` probe distinguishes a definitive answer from a failed transport. An unreachable box is reported as unreachable; only a conclusive negative can trigger a repair redeploy.
- First-time provisioning is recorded before the Railway service is created and cleared only after the credential sync has been applied, so an interrupted setup resumes instead of presenting a silently unconfigured box.
- Stop preserves service and storage; cleanup deletes the verified service before its exact attached volume.

## Ubuntu/Incus

- Unprivileged system containers are the default; `--incus-vm` opts into QEMU.
- Limits and a custom `/data` volume are applied with ownership config.
- Direct standalone operation uses a configured Incus remote; controller hosts use `vmbox-hostd` so workloads never receive the Incus socket.
