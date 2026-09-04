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
- An agent is authenticated only when it says so in its own structured answer, never because its check exited zero. A box whose synced credential is correctly owned but stale still runs `claude auth status --json` to completion with status zero and reports the failure only as `"loggedIn": false` inside the payload, so the parsed field is the verdict and an answer that will not parse counts as unauthenticated.
- No operation redeploys a box because a probe failed. The shell CLI's `/data` mount probe could not distinguish a definitive negative from a failed transport and escalated the latter into a repair redeploy of a healthy box; it was removed together with `vmbox.sh`, and nothing in the Go CLI reintroduces it. `Reconcile` only starts a box that should be running or stops one that should not.
- Ownership metadata is written before the rest of a service's configuration, so a creation interrupted between `VMBOX_ACCOUNT_ID` and `VMBOX_BOX_ID` leaves a service that `vmbox new` adopts and repairs instead of starting it incomplete. That window closes as soon as `VMBOX_BOX_ID` is set, which is well before the credential sync: a creation interrupted after the service is deployed but before profiles and GitHub credentials have been synced still presents as complete, and those credentials have to be installed with `vmbox auth <box>`. Holding the provisioning record open until the sync has been applied needs a provider-level marker the current interface cannot express.
- Stop preserves service and storage; cleanup deletes the verified service before its exact attached volume.

## Ubuntu/Incus

- Unprivileged system containers are the default; `--incus-vm` opts into QEMU.
- Limits and a custom `/data` volume are applied with ownership config.
- Direct standalone operation uses a configured Incus remote; controller hosts use `vmbox-hostd` so workloads never receive the Incus socket.
