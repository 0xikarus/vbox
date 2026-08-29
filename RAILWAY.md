# Railway vmbox agent guide

This repository provisions disposable Railway development boxes. Run `vmbox
help` for the authoritative CLI guide.

## Daily workflow

```bash
vmbox ls
vmbox list
vmbox <box-id>
vmbox new <box-id>
vmbox start <box-id>
vmbox resume <box-id>
vmbox stop <box-id>
vmbox cost [box-id]
vmbox auth <box-id>
```

`vmbox <box-id>`, `new`, and `start` create `vmbox-<box-id>` when missing and
otherwise reconnect.
`list` is an interactive Up/Down picker; `ls` prints IDs and copy-paste
resume commands.
`stop` removes active compute while preserving the service and `/data`.
`resume` deploys a stopped box again. Power-down stops tmux and all running
processes; files and Codex history remain, so use `codex resume --last` after
reconnecting.

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

Selected files are streamed over Railway SSH to `/data/home`, never committed
or baked into the image. Treat the resulting box as an authenticated device.

## Persistent data and cleanup

The persistent home and workspace are `/data/home` and `/data/workspace`.
`vmbox clean` permanently deletes every service and every volume in the
configured project/environment, including all mounted `/data`. It asks for
the word `clean`; `vmbox clean --yes` skips that confirmation.

## Container notes

The shell runs as root, so `apt-get update && apt-get install -y <package>`
works without sudo. sudo and Bubblewrap are preinstalled for convenience in
new images. Manual package changes are ephemeral across container redeploys;
durable packages belong in the Dockerfile.
