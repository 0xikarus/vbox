<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/vbox-logo-dark.png">
    <img src="docs/assets/vbox-logo.png" alt="vbox" width="420">
  </picture>
</p>

# vbox

vbox is a self-hosted home for coding agents. Each box keeps its files and agent sessions, with chat, a desktop, and a TMUX terminal in the browser. A controller manages accounts, storage, and worker capacity; you choose where to run it.

Codex, Claude Code, and OpenCode are supported, but any harness can be used inside a box. Boxes can also use owner-approved MCP tools to message each other and work with each other and third-party websites.

## What you can do

- **Work across boxes:** pin chats, read box ↔ box messages, and keep both desktops visible above their conversation. The mascot and short activity phrase show each agent's current mood and work.
- **Chat with context:** send images, annotate them with pen, rectangle, ellipse, line, or arrow, and reply in threads. Mobile swipe gestures navigate chats; a local chat cache makes revisits quicker.
- **Manage capacity:** the Providers popup summarizes workers, slots, and host resources; the Providers page lets owners edit and delete provider configurations. Details shows RAM, swap, disk, activity, hibernation, and limits. Usage is available from the top bar.
- **Keep work between sessions:** hibernation frees a worker slot while retaining box files. Wake the box to resume; closing a browser tab leaves a running box alone.

## Chat heartbeats

In a box chat, select `/heartbeat` and fill in **count**, **interval** (minutes), and
**message** directly in the composer. Each tick sends that text as a normal user
chat message. The first tick runs after the interval; sleeping boxes pause the
timer until they are running again. Open **Details → Settings → Active Tools** to
see the remaining count and stop a chat heartbeat. Starting another replaces the
current chat heartbeat for that box. This owner control does not require the
agent’s MCP heartbeat permission.

## Disposable tasks

Use **Tasks** in the controller menu to queue a goal with a saved profile and
resource settings. At most four task boxes run per account. On completion the
agent saves its result and files, and the controller releases the worker slot.
The workspace and results expire after 24 hours by default. Follow-up work is a
new task.

Save reusable configurations under **Tasks → Task profiles** and create tasks
with `{"name":"job-123","goal":"Your goal","taskProfile":"your-preset"}`.
Profiles include model, resources, saved logins and tools; edits affect future tasks.

Third-party services use scoped tokens, anonymous profile IDs and simple
30-second polling. See [Tasks API](docs/TASKS-API.md) and the
[Python API example](scripts/examples/tasks_api.py).

## Quick start

You need a controller with PostgreSQL and HTTPS, plus at least one worker. Running capacity can incur hosting charges.

1. Deploy the controller and bootstrap an owner account using the [controller guide](docs/CONTROLLER.md).
2. Register a worker with the [Linux VPS guide](docs/LINUX-VPS-SETUP.md) or [another provider](docs/PROVIDERS.md).
3. Open the controller, sign in, add an agent profile, and create a box. Chat, desktop, and TMUX are available in its browser workspace.

During box creation, choose the desktop size (1024 × 640 by default or 1280 × 720). The optional Cloudflare WARP preset installs `warp-cli` and `warp-svc` in the box and reinstalls them after hibernation. It does not register or connect WARP. A connection needs suitable worker network permissions and working access to Cloudflare's edge; workers that block its UDP handshake cannot establish a WARP tunnel.

For terminal access on Linux or macOS, install Git, OpenSSH, and Go 1.26 or Docker, then:

```bash
git clone https://github.com/0xikarus/vmbox-service.git
cd vmbox-service
./install.sh
vbox connect https://YOUR-CONTROLLER
vbox new builder
vbox builder
```

The CLI is named `vbox`; `vmbox` is a compatibility symlink. Run `vbox help` for other commands.

To upload a local Chrome or Chromium browser session for later use in a box,
run `vbox profiles upload` and mark its profile row. The picker shows the local
profile path. See [browser profile imports](docs/BROWSER-PROFILES.md) for the
one-time browser helper setup and attaching a saved profile to a box.

## Documentation

Start with the [documentation index](docs/README.md). See [controller operations](docs/CONTROLLER.md), [worker isolation](docs/SHARED-WORKERS.md), the [agent desktop guide](docs/AGENT-DESKTOP-IMPLEMENTATION.md), and the [API contract](docs/openapi.yaml).
