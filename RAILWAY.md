# Railway vmbox agent guide

This repository provisions disposable Railway development boxes. Run `vmbox
help` for the authoritative CLI guide.

## Daily workflow

```bash
vmbox ls
vmbox list
vmbox <box-id>                     # create or reconnect
vmbox new <box-id>
vmbox start <box-id>
vmbox resume <box-id>
vmbox stop <box-id>
vmbox cost [box-id]
vmbox resize [box-id]
vmbox auth <box-id>
vmbox github <box-id>
vmbox clean                        # checkbox multi-selector
vmbox clean <box-id> [--yes]
vmbox clean --all [--yes]
vmbox new <box-id> -- claude "fix active tickets, then commit"
```

Project-wide cost output combines Railway's historical deleted-service rows
into one `deleted services (N)` total; active boxes remain separate.

`vmbox <box-id>`, `new`, and `start` create `vmbox-<box-id>` when missing and
otherwise reconnect.
Anything after `--` starts directly in the tmux-backed session with stdin
forwarded. To task Codex, run `vmbox <box-id> -- codex "your task"`; use
`codex exec` instead for a non-interactive one-off run.
Missing and partially created boxes show one checklist for components, region,
agent profiles, GitHub, and Markdown. Up/Down moves, Space selects, and Space or
Enter activates the final `[ Provision box ]` row. Enter does nothing on other
rows, and `q` cancels without provisioning. Selected local files upload only
after the box is healthy. Use `vmbox auth` or `vmbox github` to resync later.
`list` is an interactive Up/Down picker; `ls` prints IDs and copy-paste resume
commands. `resize [box-id]` changes per-replica vCPU/RAM limits; omit the ID to
select a box with `[ ]`, then choose preset or custom limits.
`stop` removes active compute while preserving the service and `/data`.
`resume` deploys a stopped box again. `vmbox <box-id>` also reconnects whenever
that name already exists. Each tmux connection opens a short specs/IP/detach
popup inside the session, and the persistent status bar shows box name,
CPU/RAM, and region. Power-down stops tmux processes; files and Codex history
remain, so use `codex resume --last` after reconnecting.

## Leave Codex running

Inside the tmux session:

1. Press `Ctrl-b`.
2. Release both keys.
3. Press `d`.

Reconnect with `vmbox resume <box-id>`. Do not type `exit` when you want
Codex, Claude, Forge, or another process to keep running.

## Agent profiles and authentication

On first creation, vmbox detects Codex and Claude profile directories without
hardcoded profile names. The picker supports one Codex plus one Claude profile
together and allows a custom directory with `a`. Nothing is selected by
default. The selected profile's login and portable configuration files are
uploaded, including MCP settings from Codex `config.toml` or Claude
`.claude.json`/`settings.json`. Histories, caches, sessions, and databases are
excluded. Every file is verified by checksum, and uploaded logins are checked
with the agent's authentication-status command inside the box. Run
`vmbox auth <box-id>` to reopen the picker later.

On first creation, another opt-in picker accepts any readable `.md` path. The
selected file is checksum-verified and copied to `/data/workspace/AGENTS.md`
and `/data/workspace/CLAUDE.md`, giving Codex and Claude the same instructions.
Nothing is selected automatically.

Selected credential files are streamed over Railway SSH to `/data/home`, never
committed or baked into the image. GitHub authentication is a separate opt-in
checkbox and is never synced merely because `gh` is installed. Ambient Railway
`GH_TOKEN` and `GITHUB_TOKEN` values are removed from the container and tmux;
only the explicitly selected stored GitHub CLI account is used. Treat an
authenticated box as an authenticated device.

## Persistent data and cleanup

The persistent home and workspace are `/data/home` and `/data/workspace`.
Before the first deploy, vmbox waits for Railway to report the service's `/data`
volume as `Ready`. It then verifies the live mount and redeploys once only as a
fallback if Railway started the container without it.
`vmbox clean` selects one or more boxes with `[ ]` checkboxes. Named cleanup
uses `vmbox clean <box-id> [...]`; `vmbox clean --all` targets all services and
active project volumes. `--yes` skips the final confirmation only. Pending
volume-deletion records are ignored.

## Container notes

Core packages include tmux, Git, `gh`, SSH, sudo, Bubblewrap, Node.js, and npm.
On first creation, select optional Codex, Claude Code, Bun, and Foundry tooling;
all four are checked by default. Choose `Confirm selection` after toggling tools.
Then select US West, US East, Europe West, or Southeast Asia and confirm the
location. `VMBOX_DEFAULT_REGION` controls the preselected and non-interactive
default. The shell runs as root, so `apt-get update && apt-get install -y
<package>` works without sudo. Manual package changes are ephemeral across
redeploys; durable packages belong in the Dockerfile.
