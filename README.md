<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/vbox-logo-dark.png">
    <img src="docs/assets/vbox-logo.png" alt="vbox" width="420">
  </picture>
</p>

# vbox

vbox provides a meta-harness and persistent environments for your agents. Create boxes in the browser and work with their desktop, tmux terminal, and agent chat. The controller manages accounts, workers, and storage; you choose where to host them.

<p><strong>Supported harnesses and subscriptions:</strong>
  <a href="https://openai.com/codex"><img src="docs/assets/harness-codex.svg" alt="" width="16" height="16"> Codex (ChatGPT)</a> ·
  <a href="https://claude.com/product/claude-code"><img src="docs/assets/harness-claude.svg" alt="" width="16" height="16"> Claude Code (Claude)</a> ·
  <a href="https://opencode.ai"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/assets/harness-opencode-dark.svg"><img src="docs/assets/harness-opencode-light.svg" alt="" width="16" height="16"></picture> OpenCode (model API keys)</a>
</p>

Grok Bot inspired this project. I wanted to bring my own agent harness and host the boxes myself, so I built vbox.

## Features

- Persistent box files and browser profiles
- Browser desktop, tmux terminal, agent chat, and Grid
- Images, annotated replies, pinned chats, and groups
- Box-to-box messaging with contact and role permissions
- Encrypted agent logins, trusted setup scripts, and tool presets
- Self-hosted workers and hibernation
- CLI management, local tmux, and VNC

## How boxes work

A running box uses a worker slot. Hibernation frees the slot and keeps the box's files, but stops its processes. When a saved agent session is available, chat offers to restore it after wake. Closing the browser tab leaves the box running.

## Get started

vbox needs a controller with PostgreSQL and HTTPS, plus at least one worker. Running capacity can incur hosting charges.

1. Deploy the controller and bootstrap an owner account using the [controller guide](docs/CONTROLLER.md).
2. Start a Linux worker and register its capacity. Follow the [self-hosted VPS guide](docs/LINUX-VPS-SETUP.md) or choose [another worker provider](docs/PROVIDERS.md).
3. Open the controller in a browser, sign in, and create a box. Choose its agent, saved login, worker pool, and optional tools.

The browser gives each running box **Desktop**, **TMUX**, and **Chat** views. The [agent desktop guide](docs/AGENT-DESKTOP-IMPLEMENTATION.md) covers tools and browser state; the [local prompt API](docs/LOCAL-AGENT-PROMPT.md) lets apps inside a box send messages.

## Optional CLI

On Linux or macOS, install Git, OpenSSH, and either Go 1.26 or Docker. On Windows, use a Linux environment such as WSL.

```bash
git clone https://github.com/0xikarus/vmbox-service.git
cd vmbox-service
./install.sh
```

The installer places `vmbox` in `~/.local/bin`. Open a new terminal if needed, then connect with the token supplied by the controller owner:

```bash
vmbox connect https://YOUR-CONTROLLER
vmbox profiles upload
vmbox new work
vmbox work
```

`vmbox profiles upload` imports Codex, Claude, OpenCode, or GitHub credentials into encrypted saved profiles. Existing boxes receive profile changes when you reapply the profile. Run `vmbox help` for all commands. `vmbox desktop work` needs a local VNC viewer; the browser desktop does not.

## Security and limits

Agents can run commands inside their boxes, and the default image gives the box user passwordless sudo. Keep the controller behind HTTPS, protect its token and encryption key, and choose worker isolation for your host. See [controller operations](docs/CONTROLLER.md) and [shared worker isolation](docs/SHARED-WORKERS.md) before opening an installation to other users.

Saved chat attachments share a 1 GiB account limit. Owners can clear a box's attachments in **Chat → Box details** without deleting message text.

## Development

The [documentation index](docs/README.md) links setup guides and the dated product comparison. The [agent guide](docs/AGENT-GUIDE.md) maps implementation and lifecycle rules; the [API contract](docs/openapi.yaml) documents endpoints.

```bash
go test ./...
go vet ./...
bash tests/run.sh
npm run test:browser
```

Some integration tests require a disposable PostgreSQL database or container. The browser suite requires Chromium; check the scripts before running tests that create resources.
