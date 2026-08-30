# vmbox-service

Disposable Railway development boxes with persistent `/data` and tmux.

## Install

Requires Bash, `curl`, `jq`, Railway CLI, and SSH.

```bash
gh repo clone 0xikarus/vmbox-service
cd vmbox-service
./install.sh --workspace-token
source ~/.bashrc
```

## Quick start

```bash
vmbox work                    # create or reconnect
vmbox list                    # interactive picker
vmbox ls                      # list boxes
vmbox stop work               # stop compute; keep /data
vmbox resume work             # redeploy and reconnect
vmbox clean work --yes        # delete box and volume
vmbox cost                    # current-period costs
vmbox help                    # full command reference
```

Creation uses one screen for tools, region, optional Codex/Claude and GitHub
credentials, and optional shared `AGENTS.md`/`CLAUDE.md` instructions. The
persistent volume reaches `Ready` before the first deployment.

## Forward a task

```bash
vmbox work -- codex "review the contracts, fix issues, test, and commit"
vmbox work -- codex exec "run this task non-interactively"
vmbox work -- claude "fix active tickets, then commit"
vmbox worker-a --detach -- codex "fix ticket 123, test, and commit"
```

## Detach without stopping work

1. Press `Ctrl-b`.
2. Release both keys.
3. Press `d`.

Reconnect with `vmbox resume work`. After powering down, files remain in
`/data`; use `codex resume --last` to reopen Codex history.

Credentials are opt-in and stored under `/data/home`. Configuration lives in
`~/.config/vmbox/config`.

See [RAILWAY.md](RAILWAY.md) for detailed operational and agent documentation.
