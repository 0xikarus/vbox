# Railway vmbox agent guide

This repository provisions disposable Railway development boxes. Run `vmbox
help` for the authoritative CLI guide.

## Daily workflow

```bash
vmbox ls
vmbox list
vmbox start <box-id>
vmbox resume <box-id>
vmbox cost [box-id]
vmbox auth <box-id>
```

`start` creates `vmbox-<box-id>` when missing and otherwise reconnects.
`list` is an interactive Up/Down picker; `ls` prints IDs and copy-paste
resume commands.

## Leave Codex running

Inside the tmux session:

1. Press `Ctrl-b`.
2. Release both keys.
3. Press `d`.

Reconnect with `vmbox resume <box-id>`. Do not type `exit` when you want
Codex, Claude, Forge, or another process to keep running.

## Authentication

On first creation, vmbox detects Codex and Claude credential profiles without
hardcoded profile names. The picker supports Codex plus Claude together and
allows a custom file path with `a`. Nothing is selected by default.

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
