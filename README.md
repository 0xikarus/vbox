<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/vbox-logo-dark.png">
    <img src="docs/assets/vbox-logo.png" alt="vbox" width="420">
  </picture>
</p>

# vmbox

<p align="center"><strong>Supported Harnesses and Subscriptions</strong></p>

<p align="center">
  <a href="https://openai.com/codex"><img src="docs/assets/harness-codex.svg" alt="Codex logo" width="36" height="36"></a>&nbsp;&nbsp;
  <a href="https://claude.com/product/claude-code"><img src="docs/assets/harness-claude.svg" alt="Claude Code logo" width="36" height="36"></a>&nbsp;&nbsp;
  <a href="https://opencode.ai"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/assets/harness-opencode-dark.svg"><img src="docs/assets/harness-opencode-light.svg" alt="OpenCode logo" width="36" height="36"></picture></a>
</p>

<p align="center">Codex (ChatGPT) · Claude Code (Claude) · OpenCode (model API keys)</p>

Grok Bot inspired this project. I wanted to bring my own agent harness and host the boxes myself, so I built vmbox.

**Disclaimer:** It's slop, but works!

vmbox is a web workspace for persistent remote Linux boxes. Create a box in the browser, then use its desktop, tmux terminal, and agent chat. Run Codex, Claude Code, or OpenCode in the same box. A controller manages accounts, workers, and storage; you choose where to host them.

Boxes keep their files when hibernated. Running processes stop and start fresh when a box wakes. Closing a browser tab leaves a running box alone.

## Server and worker setup

1. Deploy the controller with PostgreSQL, HTTPS, and a matching `vmbox-runtime` binary. Set `DATABASE_URL`, `VMBOX_CONTROLLER_URL`, and `VMBOX_ENCRYPTION_KEY`, then bootstrap an owner account. See [controller operations](docs/CONTROLLER.md).
2. Add worker capacity. For a self-hosted Linux server, build or pull the worker image, run the supervisor, register its endpoint and token as a `shared-worker` pool, and set its slot count in the web UI. Follow the [Linux VPS guide](docs/LINUX-VPS-SETUP.md); [other worker providers](docs/PROVIDERS.md) are also available.
3. Open the controller URL in a browser, sign in with the owner token, and create a box. Its workspace has **Desktop**, **TMUX**, and **Chat** views; **Grid** shows several running boxes. Each running box needs a free worker slot. Hibernation frees compute while retaining its files.

Provisioning capacity can incur hosting charges. See the [agent desktop guide](docs/AGENT-DESKTOP-IMPLEMENTATION.md) for tools, browser state, and secrets, and the [local prompt API](docs/LOCAL-AGENT-PROMPT.md) for sending messages from apps inside a box.

## Optional CLI and local access

The CLI adds local tmux and VNC access, box management, and credential upload. It runs on Linux or macOS; on Windows, use a Linux environment such as WSL. Install Git, OpenSSH, and Go 1.26 or Docker, then:

```bash
git clone https://github.com/0xikarus/vmbox-service.git
cd vmbox-service
./install.sh
```

The installer places `vmbox` in `~/.local/bin`. Open a new terminal if it is not on your `PATH`. Connect using the token supplied by the controller owner at the hidden prompt:

```bash
vmbox connect https://YOUR-CONTROLLER
vmbox whoami
```

Upload local Codex, Claude, OpenCode, or GitHub credentials with `vmbox profiles upload`. The controller encrypts saved profiles; select one when creating a box in the web UI or CLI. Existing boxes keep their imported copy until you reapply a profile.

```bash
vmbox profiles upload
vmbox new work       # or create a box in the browser
vmbox work           # open or resume its tmux shell
vmbox desktop work   # open its desktop in a local VNC viewer
```

`vmbox desktop` needs a local VNC viewer such as TigerVNC. The browser's Desktop and TMUX views work without one. The CLI creation form lets you choose a pool, agent, saved login, and optional tools. Detach from tmux with **Ctrl-a, then d** and choose **Leave unchanged** to keep it running.

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

Use `vmbox logout` to remove the CLI's saved controller login. Agent chat supports image attachments and direct contacts between boxes. Optional Blender and Foundry presets are selected when creating a box.

Saved chat attachments share a 1 GiB account limit. Owners can review usage and
clear a box's attachments in **Chat → Box details**; message text remains.

## How it compares

| Feature | vmbox | Grok Bot | Muse |
| --- | --- | --- | --- |
| Bring your own agent harness | Codex, Claude Code, or OpenCode | Cursor-managed bot; other harnesses not documented | Muse's built-in agent; other harnesses not documented |
| Bring your own model key | OpenCode profiles support OpenRouter or Venice API keys | Cursor manages model selection; own key not documented | Own model key not documented |
| Bring your own connected apps | Browser logins and apps in the box | Connectors, plugins, and browser sites | Connected apps and browser sites |
| Install your own software | Linux packages and trusted setup scripts | Yes; manual installs are lost on computer recreation | Installation in its secure VM not documented |
| Choose where it runs | Self-host the controller and Linux workers | Cursor-hosted only | Meta-hosted |
| Persistent workspace | Separate volume per box | One computer shared by a user's bots | One secure VM per person |
| Browser and shell | Both | Both | Browser; shell not documented |
| Agent-to-agent coordination | Direct contacts with role-based permissions | Bots message each other and share group chats | Not documented |
| Scheduled routines | Not built in | Yes | Proactive goal work; a schedule feature is not documented |
| Hibernate while retaining files | Yes | Yes, with a durable disk | Not documented |

Comparison checked 29 September 2026 against the [Grok Bot overview](https://docs.x.ai/grok-bot/overview), [computer management](https://docs.x.ai/grok-bot/computers), [teams and hosting](https://docs.x.ai/grok-bot/teams-and-enterprises), [security FAQ](https://docs.x.ai/grok-bot/security-faq), and [Meta's Muse announcement](https://about.fb.com/news/2026/09/introducing-muse-personal-ai-agent/). "Not documented" means the linked material does not confirm the feature; it does not establish that the feature is impossible.

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
