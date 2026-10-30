# Standalone instances

Status: experimental implementation. Docker installation and the local runtime
work; this is not yet the complete release described in
[the implementation plan](STANDALONE-PLAN.md).

Each instance runs its own desktop, managed OpenCode, Claude or Codex conversation,
private MCP service, and SQLite-backed controller. The controller publishes
separate view, desktop-control, and chat capabilities. The fleet controller is
not required. Host management uses the existing Docker provider, locally or
through its SSH transport.

## Build and install

Install the CLI and prepare the standalone image in one command. The target
Docker engine must support Linux amd64 or arm64:

```bash
./install.sh --standalone
vbox standalone create my-box --mode container
```

For a remote Docker host using your SSH configuration:

```bash
./install.sh --standalone --host home-server
vbox standalone create my-box --mode container --host home-server
```

The installer builds `vbox-standalone:dev` on the selected engine. No manual
`docker build` or `docker run` is needed; the creation command starts the
instance. Creation from a repository checkout can also build a missing default
image automatically. Use `--image TAG` with the installer and creation command to choose
another image name. The CLI still requires Go 1.26 or a local Docker engine for
its own build, and the target must already have Docker installed. The installer
does not install a Docker daemon or start a standalone instance by itself.
Plain `./install.sh` installs the CLI without building a standalone image.

Run `vbox standalone create NAME` in a terminal for the configuration wizard.
It displays Docker CPU/RAM capacity and Linux RAM availability, load, free space
on the home filesystem, and KVM access. Select container mode explicitly while
VM installation remains under development. The profile rows show source paths
on the CLI machine, including for remote installations.

For a complete noninteractive Claude configuration:

```bash
vbox standalone create researcher --host home-server \
  --mode container --agent claude --cpus 2 --memory 6g \
  --profile claude=/path/to/.claude --image vbox-standalone:dev
```

The installer prepares the image ahead of time. If the default
`vbox-standalone:dev` image is missing and creation runs from the repository,
`create` builds it automatically on the selected Docker engine before registering
the instance. Custom image names must exist on that engine or in a registry.
Outside a repository checkout, use `./install.sh --standalone --host home-server`
to prepare the default image first. The default bind
address is host loopback. Use `--port 8088` to choose a host port, or omit it to
select an available port which is recorded explicitly and survives restarts.
Use `--listen 0.0.0.0` for LAN exposure. Configure public HTTPS separately.

Codex uses the same native runtime and portable profile import as fleet boxes:

```sh
vbox standalone create coder --mode container --agent codex \
  --cpus 2 --memory 4g --profile codex=/path/to/.codex-two
```

For OpenCode, supply the existing model API and model ID:

```bash
vbox standalone create coder --mode container --agent opencode \
  --cpus 2 --memory 4g --model-url http://MODEL-SERVER:8080/v1 \
  --model MODEL-ID --image vbox-standalone:dev
```

For a vision-capable model, add `--model-vision` (also available in the wizard).
This declares image input in OpenCode; unknown custom models otherwise default
to text-only. Existing model capabilities and provider settings are retained
across restarts. OpenCode uses the shared runtime’s native `--auto` mode, which
automatically approves requests while preserving explicit deny rules.

The model endpoint must be reachable from inside the workload. The installer
does not install an inference service. The current OpenCode configuration writer
supports JSON; imported JSONC registration remains unsupported.

The inference server and standalone instance can run on the same host. Use the
host's reachable LAN name/address in `--model-url`, rather than `localhost`
(which refers to the workload container). The instance's CPU/RAM limits apply
to its desktop and agent; the existing inference server has separate resources.

## Manage instances

```bash
vbox standalone list
vbox standalone status researcher
vbox standalone set researcher
vbox standalone set researcher --cpus 2 --memory 6g
vbox standalone links researcher
vbox standalone stop researcher
vbox standalone start researcher
vbox standalone start researcher --port 8088
vbox standalone delete researcher --yes
# Equivalent full teardown:
vbox standalone shutdown researcher --cleanup --yes
```

Resource edits inspect and verify the engine's effective limits. RAM values
without a suffix use MiB; `6g` means 6 GiB. Live memory shrinking is rejected.
Created instances have no additional swap allowance.

Changing the startup port recreates the compute container while retaining its
owned data volume, imported profiles, resource limits, and agent settings. An
occupied replacement port is rejected before stopping the instance. `stop` (or
`shutdown`) cleanly stops all workload processes and keeps the data. `delete
--yes` and `shutdown --cleanup --yes` remove the owned container, network, volume,
copied profiles, target manifest/log directory, and CLI record. Teardown also
cleans up owned volumes/networks left by an already-missing container. Shared
Docker software and image caches are not instance data.

The CLI stores a separate registry beside its normal configuration. Each target
gets a private manifest directory under
`~/.local/share/vbox-standalone/instances/ID` by default; `--storage` selects an
absolute target path instead. Workspaces use independently owned Docker volumes,
and instances use independently owned Docker networks. The manifest records
identity and resources; it does not contain imported credentials or link tokens.

Profile import copies allowlisted files privately into the stopped container
before its first launch. Imported Claude host hooks, status commands, plugins,
external MCP registrations and project paths are omitted. The runtime registers
the local vbox MCP and applies the existing managed-agent defaults. The agent
can access the credentials imported into its own instance.

The homepage reuses the controller’s chat styles, with no chat-list sidebar.
The conversation fills the workspace with the same message bubbles and multiline composer.
Open Desktop or Terminal from the chat header to show one panel with Desktop
directly above TMUX. Viewers connect only while that panel is open. Closing it
detaches the viewers and leaves the box running.
Both viewers use the same width. The desktop terminal mirror does not constrain
the web terminal’s column count or resize it when focus changes. On small screens
the combined panel fills the screen. The live-view link opens separately for read-only viewing.

Desktop, TMUX and chat require the instance password. Passwords have no minimum length or complexity requirement; an empty password is
also accepted. The creation wizard has a masked password field; for automation use `--password-file /private/path`.
The CLI hashes the password before copying it to the instance; the plaintext is
not stored in container environment variables, manifests or the CLI registry.
Without a supplied password, initial setup requires the private desktop link
from `vbox standalone links NAME`, or a browser that already used that link.
Opening the homepage alone does not authorize initial password setup.

The **shutdown** button stops the entire standalone instance after a bounded
grace period for saving runtime state. Files and the volume are retained. Start
it again with `vbox standalone start NAME`; full deletion remains a separate CLI
action. Standalone containers use Docker’s `on-failure` restart policy so a clean
shutdown stays stopped while a crash can restart.

Use **change password** in the top bar to supply the old and new passwords.
Changing the password invalidates other browser sessions, pending stream tickets,
and existing desktop/TMUX connections. Read-only live-view links remain valid.
Browser sessions last twelve hours; private capability links alone do not bypass
the password for controls or chat.

The wizard also offers Blender, Foundry and Proxy / IP (direct, Tor or WARP).
For automation use `--tools blender,foundry --proxy tor` or `--proxy warp`.
Tor starts SOCKS5 on `127.0.0.1:9050`; WARP uses its existing local proxy mode on
`127.0.0.1:40000` and attempts to register/connect. Applications must opt in to
these SOCKS5 proxies; the host and default container route are unchanged.
Tool packages are prepared as container root before the agent launches, and the
agent receives no sudo rights. Standalone Blender requires a native Blender 5.x distribution package on
amd64/arm64, plus the shared pinned Blender MCP installer and Python requests
package. The standalone image uses Debian sid so ARM64 has a native Blender 5
package; it is independent of the fleet image. Package versions can change with
the distribution, and startup refuses an older Blender major version. The GUI
and loopback-only Blender MCP server start before the agent. Scene creation and
rendering remain agent actions through Blender MCP. Rendering threads are capped
to the workload CPU allowance. Blender 5.2.2 LTS was verified on ARM64.

The service starts desktop, MCP, and the chosen managed agent automatically.
A `vbox session` desktop shortcut creates or opens the managed session through
the local controller. Transient startup failures retry automatically, and missing
managed sessions are recreated; queued chat is retained until startup succeeds.
It reuses existing native delivery receipts, durable runtime outbox handling,
screenshots/input, tmux snapshots, and Claude/OpenCode/Codex restoration. Chat history
persists in SQLite. Chat displays text and agent image attachments, with a full-size image preview.
Archived images use authenticated local routes; the public live-view capability
cannot read them. Rich question forms and owner attachment uploads remain unfinished.
Incoming envelopes are archived before acknowledgement.

## Verification and remaining work

A disposable ARM64 Ubuntu 24.04 host with Docker 29.1.3 was tested with a real
Claude profile, a hard 6 GiB RAM limit, 2 CPUs, and no additional swap. Evidence:

- Native Claude transcript recorded one requested screenshot tool call and one
  expected `chat_message` reply.
- Real Chromium connected to the read-only screen, interactive VNC desktop, and
  chat over HTTP. Chat also survived browser reload.
- Restart preserved the native Claude conversation ID and local chat history.
- CLI resource editing verified the requested 6 GiB and 2 CPU limits.
- Focused permission, idempotency, profile portability, memory-unit, and shared
  CLI/runtime/provider tests pass.

A separate ARM64 OpenCode instance was tested against an existing OpenAI-compatible
API on the same host using `qwen3.8-27b-abliterated`. Its native conversation
confirmed that exact model on provider `local`, completed `take_screenshot` and
`chat_message`, and delivered the expected reply to the standalone chat. Real
browser checks passed for all three dashboard views. Graceful stop/start retained
the port, chat history, and native OpenCode conversation. The workload was capped
at 6 GiB and 2 CPUs; no inference service was installed or reconfigured.

The opt-in `tests/standalone/host-matrix.py` also passed on three authorized
amd64 KVM hosts using separate disposable Docker instances (2 GiB, 1 CPU each).
Each completed real Claude screenshot/chat calls, dashboard browser checks,
shutdown/restart with an explicit stable port, and orphan-safe teardown after
the container was deliberately removed. All three test containers, volumes,
networks, instance directories, registry records, and test image tags were
removed. These verify Docker mode on those hosts, not VM provisioning.

The opt-in real-browser test is `tests/standalone/compact-browser.mjs`. It requires
an explicitly identified disposable standalone container and a browser debugging
connection from a browser running inside that workload. It does not print link
capabilities. See its required environment variables before running it.

VM provisioning, host-enforced LAN/metadata egress isolation, full chat attachments/questions, backup/restore, automated upgrades
and failure reconciliation remain implementation work. Docker mode shares the
host kernel and must not be described as VM isolation or certified hostile-agent
containment. A missing KVM/Incus privilege is reported; the CLI does not silently
substitute container mode for a requested VM.

The initial full serial fleet browser run reported 214 passes, five failures,
and one skip. Focused reruns resolved two Go-path errors and two timing failures.
The remaining stale contact-permission text assertion also fails against clean
HEAD. This is not a fully passing browser release gate; the standalone live-browser
smoke passes independently.

Thinking choices are discovered at startup from `/v1/models` metadata, falling
back to the configured model's `/props?model=...` chat template when it explicitly
lists allowed reasoning values. No values are inferred from model names. Unknown
capabilities hide the selector. The current pgx model declares `xhigh` (default),
`medium`, and `low`; `high` and on/off are not advertised. The choices populate
both standalone chat and OpenCode's native variants. A saved selection applies
to subsequent web-chat messages and does not interrupt the current generation.

On startup, standalone removes stale Chromium singleton symlinks left by an
older container hostname. It preserves locks for live processes using the same
profile and retains browser profile data.

When `/props` advertises `default_generation_settings.n_ctx`, standalone records
that context limit in OpenCode and reserves a bounded output budget (up to 8192
tokens). This lets OpenCode compact long tool/vision conversations before they
exceed the server capacity. Explicit imported model limits are preserved.
A retained Blender MCP pipx environment is repaired if a new image’s Python
interpreter can no longer launch it; workspace data and MCP configuration remain.


## GitHub CLI login

Import a selected local `gh` account alongside the agent profile:

```sh
vbox standalone create coding --mode container --agent claude \
  --profile claude=/path/to/.claude --profile github=github.com:YOUR_USER
```

The wizard lists discovered GitHub accounts for explicit selection. Exporting
with `HOST:USER` also supports tokens stored in the OS keyring. Alternatively,
use `--profile github=/path/to/.config/gh` for a portable `hosts.yml` containing
its active account's token. Only active HTTPS credentials are copied, privately;
local aliases, SSH keys, and unrelated accounts are excluded. The standalone
image includes `gh` and configures its Git credential helper at startup. Existing
images need rebuilding to gain this support. For reusable uploads and browser
selection, see the [hub guide](HUB.md).


With the Blender preset selected, managed `AGENTS.md` / `CLAUDE.md` instructions
name the detected installed Blender version and pinned Blender MCP version,
require the existing `blender` MCP server, and prohibit replacement installs.
Owner instructions are retained, and repeated setup updates one managed block.
