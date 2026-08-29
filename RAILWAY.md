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
Anything after `--` is started directly in the tmux-backed session with stdin
forwarded. New boxes also offer a local GitHub CLI account selector. Nothing is
selected by default: press Space to opt in to one account, then Enter. Use
`vmbox github <box-id>` to resync GitHub authentication, permissions, Git
protocol, and commit identity later.
`list` is an interactive Up/Down picker; `ls` prints IDs and copy-paste resume
commands. `resize [box-id]` changes per-replica vCPU/RAM limits; omit the ID to
select a box with `[ ]`, then choose preset or custom limits.
`stop` removes active compute while preserving the service and `/data`.
`resume` deploys a stopped box again. `vmbox <box-id>` also reconnects whenever
that name already exists. Every connection prints CPU/RAM limits, status,
region, service ID, replica count, storage, private and current public egress
IPs, workspace, and detach/resume guidance. Power-down stops tmux processes;
files and Codex history remain, so use `codex resume --last` after reconnecting.

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
checkbox and is never synced merely because `gh` is installed. Treat an
authenticated box as an authenticated device.

## Persistent data and cleanup

The persistent home and workspace are `/data/home` and `/data/workspace`.
`vmbox clean` selects one or more boxes with `[ ]` checkboxes. Named cleanup
uses `vmbox clean <box-id> [...]`; `vmbox clean --all` targets all services and
active project volumes. `--yes` skips the final confirmation only. Pending
volume-deletion records are ignored.

## Container notes

Core packages include tmux, Git, `gh`, SSH, sudo, Bubblewrap, Node.js, and npm.
On first creation, select optional Codex, Claude Code, Bun, and Foundry tooling;
all four are checked by default. The shell runs as root, so `apt-get update &&
apt-get install -y <package>` works without sudo. Manual package changes are
ephemeral across redeploys; durable packages belong in the Dockerfile.
