# vmbox

Provider-neutral development boxes with exact-argv jobs, durable `/data`, and an optional multi-user controller.

## Build

Go 1.26 or Docker is required. `./install.sh` always installs the Go CLI; when
Go is unavailable locally, the installer uses the Go Docker image as its build
environment.

Either way, `install.sh` also writes the standalone deployment bundle that
`vmbox` hands to `railway up`. Railway uploads that directory verbatim as the
build context, so it has to be complete on its own. By default the bundle builds
the current `vmbox-runtime` from the installed source and overlays it, the
entrypoint, and tmux configuration on the audited public worker image pinned by
digest. This keeps the runtime protocol in lockstep with the CLI while reusing
the audited agent toolchain.
`./install.sh --bundle-from-source` installs the full Go build payload
(`Dockerfile`, `.dockerignore`, `go.mod`, `go.sum`, `cmd/`, `internal/`)
instead. Neither layout contains a credential.

```bash
go build ./cmd/vmbox
go build ./cmd/vmbox-controller
go build ./cmd/vmbox-hostd
go test ./...
```

No custom workload image is required. Docker and Railway default to
the upstream `node:22-bookworm-slim` image; Incus defaults to the official
Ubuntu 24.04 image. On first start, vmbox installs the selected tools and its
small runtime helpers. Reopening the box is idempotent and preserves `/data`.
On Railway, `vmbox stop BOX` removes the active deployment but preserves the
service and volume. `vmbox BOX` or `vmbox resume` redeploys it; only
`vmbox clean BOX --yes` permanently deletes the service and `/data`.

### Preloaded workload image

The repository Dockerfile can build the complete workload image locally. It
includes the runtime, tmux, GitHub CLI, Codex, Claude Code, OpenCode, Bun, and
Foundry, so box bootstrap only refreshes the small vmbox runtime payload.

```bash
# Local Docker image
make box-image

# Publish for Railway; authenticate to the registry first
VMBOX_IMAGE=ghcr.io/OWNER/vmbox-box:latest make box-image-push
docker buildx imagetools inspect ghcr.io/OWNER/vmbox-box:latest
```

Railway cannot pull from the laptop's local Docker image store. Publish the
image, then configure the context with the immutable
`ghcr.io/OWNER/vmbox-box@sha256:...` reference reported by the registry. Set
`VMBOX_COMPONENTS` to a comma-separated subset when a smaller image is useful.

## Standalone

Contexts contain provider identifiers, never credential values. Provider tokens stay in their documented environment variables.

```bash
vmbox context add local-docker --provider docker --docker-context default
vmbox context add railway --provider railway --project PROJECT_ID --environment ENVIRONMENT_ID
# Or, for standalone use with an existing `railway login` session:
vmbox context add railway-local --provider railway --project PROJECT_ID --environment ENVIRONMENT_ID --railway-cli-auth
vmbox context add ubuntu --provider incus --incus-remote build-host

vmbox --context local-docker new worker --detach -- bun test
vmbox --context local-docker new review \
  --application-profile codex="$HOME/.codex-work" \
  --github-credential github.com:octocat:ssh \
  --instructions "$PWD/AGENTS.md"
vmbox --context local-docker new next-worker --reuse
vmbox --context local-docker run worker -- printf '%s\n' 'exact argv'
vmbox --context local-docker task-status worker RUN_ID
vmbox --context local-docker resume
vmbox --context local-docker resize
vmbox --context local-docker stop worker
vmbox --context local-docker clean worker --yes
```

Everything after `--` is forwarded as an argument vector. vmbox never inserts a shell. Use `bash -lc '...'` explicitly when shell expansion is intended.

Detached commands print a run ID. `task-status` returns durable JSON state and
the last visible output line for that run.

New boxes collect every choice before provisioning and save the last complete,
secret-free setup per context and working directory. `--reuse` reloads it only
from that same directory. Application profiles,
GitHub credentials, and Markdown instructions are independently selected;
Markdown is installed only as workspace instructions, never as authentication.

Docker supports local, named, SSH, and mutually authenticated TLS contexts. An unauthenticated TCP daemon is rejected. Docker and Incus control sockets are never mounted into workloads. Override `--image` only when you need a different Debian/Ubuntu-compatible OCI base.

## Controller

The controller requires PostgreSQL and never stores repositories. Bootstrap prints a one-time owner token; store it in a password manager or environment, not in this repository.

Controller owners can use `vmbox controller init|ensure`, manage exactly `owner` and `user` account roles with `vmbox users`, store account-scoped encrypted provider credentials with `vmbox credentials`, and configure/test/remove webhook, Telegram, and Discord delivery with `vmbox notifications`. See [controller operations](docs/CONTROLLER.md).

```bash
export DATABASE_URL='postgres://...'
vmbox-controller bootstrap
vmbox-controller

export VMBOX_CONTROLLER_TOKEN='...'
vmbox context add team --provider docker --docker-context build-host --controller https://controller.example
vmbox --context team boxes create worker --allocate
vmbox --context team worker
```

A valid controller context is automatically used and displayed. If the controller is unavailable, vmbox fails closed; `--standalone` is the only way to bypass it. The versioned scheduler contract is [docs/openapi.yaml](docs/openapi.yaml).

`vmbox-hostd` manages unprivileged Incus containers (or opt-in QEMU VMs) on an Ubuntu host. It accepts only authenticated requests and can advertise capacity to the controller over an outbound connection.

## Runtime interaction

```bash
vmbox-report 'Running integration tests'
answer="$(vmbox-ask 'Which API version should I target?')"
vmbox-finish --success --summary 'Opened PR #42'
```

Events are sequenced and durable. Output previews strip control sequences, normalize carriage returns, cap size, and redact configured secrets. Raw job execution remains generic; Codex, Claude Code, OpenCode, Bun, and Foundry are optional image components, not scheduler concepts.

See [docs/PROVIDERS.md](docs/PROVIDERS.md) and [docs/CONTROLLER.md](docs/CONTROLLER.md).
