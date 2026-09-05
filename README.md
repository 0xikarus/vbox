# vmbox

Persistent remote boxes, controlled by a provider-agnostic CLI and a mandatory
controller. Claude, Codex, OpenCode and shell run in tmux; native OpenSSH carries
terminal input/output. The optional lightweight web UI only configures resources.

## Install and connect

Go 1.26 or Docker builds the CLI. OpenSSH is required for native attachment.
No Railway, Docker or Incus client/token is needed for ordinary CLI operations.

```bash
./install.sh
vmbox context add team --controller https://YOUR-CONTROLLER
# Supply VMBOX_CONTROLLER_TOKEN through secure environment configuration.
vmbox ls --json
vmbox sessions helper1
vmbox helper1 --session existing-session
vmbox task helper1 --agent claude --prompt "What's today's date?" --json
vmbox updates helper1 --json
```

Controller login does not provision SSH credentials. Configure an SSH agent or
`VMBOX_SSH_IDENTITY_FILE`; native attachment requires the account owner role.
Explicit session selection never creates or replaces a session. Detach using
Ctrl-a d; closing the client keeps the worker and tmux processes running.

## Controller administration

```bash
vmbox providers schema
vmbox providers create railway primary --config-file railway.json --secret-env PROVIDER_SECRET_JSON
vmbox providers default railway primary
vmbox providers update railway primary --config-file image-edit.json --json
vmbox boxes update helper1 --default-agent codex
vmbox fleet slots set 2
```

Provider secrets stay encrypted on the controller; public reads never return
them. Updates are revision-protected and preserve omitted secrets. Retargeting
or deleting provider aliases requires explicit resource migration.
Standalone mode, provider-specific client contexts and web terminal sharing are
removed. Existing resources, credentials and legacy local bundles are preserved.

Read [controller-first contracts and migration](docs/CONTROLLER-FIRST.md),
[controller operations](docs/CONTROLLER.md), and [OpenAPI](docs/openapi.yaml).

## Build and verification

```bash
go build ./cmd/vmbox
go build ./cmd/vmbox-controller
go test ./...
go vet ./...
npm run test:browser
bash tests/run.sh
```

Native tests use a real isolated tmux/PTY when available. Database integration
requires `VMBOX_TEST_DATABASE_URL` pointing at disposable PostgreSQL. Browser
tests cover configuration, not real mobile keyboards or production agent replies.
Worker images remain controller/operator infrastructure; the installer ships
neither a standalone deployment bundle nor provider tooling.
