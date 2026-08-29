# vmbox-service

Private tooling for persistent, tmux-backed development boxes on Railway.

## What is a box?

A box is a named tmux session inside a Railway service with a persistent `/data`
volume. The local `vmbox` command stores the stable Railway project, service, and
environment IDs plus the remote workspace for every box.

Deployment-instance IDs are intentionally not stored because Railway changes them
when it replaces a container. If replacement removes the tmux process, `resume`
recreates the session at the recorded persistent workspace; process state cannot
survive a container replacement.

## Install the local CLI

Requirements: Bash, the Railway CLI, an authenticated Railway account, and SSH.

```bash
./install.sh
source ~/.bashrc
```

The installer places `vmbox` in `~/.local/bin` and creates
`~/.config/vmbox/config` from `vmbox.conf.example`.

## Commands

```bash
vmbox ls
vmbox start research
vmbox start protocol /data/workspace/protocol
vmbox resume research
```

- `ls` lists running tmux boxes in the configured Railway service.
- `start` creates, records, and attaches to a box.
- `resume` attaches to the recorded box. If its container was replaced, the tmux
  session is recreated at the same persistent workspace.

Box records live under `${XDG_STATE_HOME:-~/.local/state}/vmbox/boxes` with mode
`0600`. They contain identifiers and paths only—never credentials.

## Deploy the Railway shell service

1. Create a Railway service from this private GitHub repository.
2. Attach a persistent volume at `/data`.
3. Deploy the included `Dockerfile` and `railway.json`.
4. Copy the project, service, and environment IDs into your local
   `~/.config/vmbox/config`.
5. Use `vmbox start <id>` to create the first box.

The image includes tmux, Git, Node.js, npm, Codex CLI, Claude Code, Forge, Cast,
Anvil, and Chisel. Persistent shell configuration is written under `/data/home`.

Authentication state belongs in the Railway volume or Railway variables. Never
commit API tokens, private keys, seed phrases, `.env` files, or agent credentials.
