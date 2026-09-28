# vmbox

vmbox gives each agent a persistent remote Linux box with a shell, desktop, and files. A controller manages accounts, worker capacity, and box storage. Use the CLI or browser to work with Codex, Claude Code, or OpenCode in the same box.

Boxes keep their files when hibernated. Running processes stop and start fresh when a box wakes. Closing a terminal or browser viewer leaves a running box alone.

## Get started

The CLI runs on Linux or macOS. On Windows, use a Linux environment such as WSL. Install Git, OpenSSH, and Go 1.26 or Docker, then:

```bash
git clone https://github.com/0xikarus/vmbox-service.git
cd vmbox-service
./install.sh
```

The installer places `vmbox` in `~/.local/bin`. Open a new terminal if it is not on your `PATH`.

Connect to a controller and enter the token supplied by its administrator at the hidden prompt:

```bash
vmbox connect https://YOUR-CONTROLLER
vmbox whoami
```

To host your own controller, follow [controller operations](docs/CONTROLLER.md). To add workers on a Linux server, follow the [Linux VPS guide](docs/LINUX-VPS-SETUP.md). A controller owner must configure a worker pool and free capacity before boxes can start:

```bash
vmbox pools create
vmbox pools default
vmbox fleet location
vmbox fleet slots set 1
vmbox fleet status
```

Provisioning capacity can incur provider charges. Each running box needs a free compute slot. Hibernation frees compute while retaining the box's volume.

Create a box and connect:

```bash
vmbox new work
vmbox work
```

The creation form lets you choose a worker pool, location, agent, saved login, and optional tools. Leave the startup command blank for a shell; run `codex`, `claude`, `opencode`, or another program inside it. Disconnect from tmux with **Ctrl-a, then d** and choose **Leave unchanged** to keep it running.

## Everyday use

```bash
vmbox                         # list boxes and their states
vmbox whoami                  # show your account and role
vmbox work                    # reconnect to a box
vmbox hibernate work          # stop compute, retain files
vmbox delete work             # permanently delete the box and its files
vmbox help                    # command reference
```

`vmbox delete` asks you to confirm the exact box name. Hibernation preserves files, but it cannot preserve live processes or an agent's current conversation. The web Chat keeps previous messages visible after a box wakes.

You can upload local Claude, Codex, OpenCode, or GitHub logins with `vmbox profiles upload` and select a saved profile when creating a box. Profiles are encrypted on the controller. Existing boxes keep their imported copy until you explicitly reapply a profile. Use `vmbox logout` to remove the CLI's saved controller login.

The controller's web UI has box workspaces with Desktop and TMUX views, Agent chat, and Grid for viewing several running boxes. Agent chat supports image attachments and direct contacts between boxes. Optional Blender and Foundry presets are selected when creating a box. See the [agent desktop guide](docs/AGENT-DESKTOP-IMPLEMENTATION.md) for tools, browser state, and secrets, and the [local prompt API](docs/LOCAL-AGENT-PROMPT.md) for sending a message from an app inside a box.

## Security and operation

Treat boxes as trusted workloads: agents can execute commands inside their containers, and the default image grants the box user passwordless sudo. Keep the controller behind HTTPS, protect its token and encryption key, and choose worker isolation appropriate to your host. See [controller operations](docs/CONTROLLER.md) and [shared worker isolation](docs/SHARED-WORKERS.md) before exposing an installation to other users.

Controller login does not replace an SSH key for legacy worker access or optional provider port forwarding. If a saved agent login expires, refresh it locally and upload it again.

## Development

[docs/README.md](docs/README.md) links the current setup and architecture guides. [docs/AGENT-GUIDE.md](docs/AGENT-GUIDE.md) maps the implementation and lifecycle rules. The API contract is in [docs/openapi.yaml](docs/openapi.yaml).

```bash
go test ./...
go vet ./...
bash tests/run.sh
npm run test:browser
```

Some integration tests require an explicitly disposable PostgreSQL database or container. The browser suite requires Chromium. See the test scripts for their environment variables before running tests that create resources.
