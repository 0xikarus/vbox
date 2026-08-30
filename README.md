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
vmbox status work             # show detached task status
vmbox wait work               # wait for detached task completion
vmbox help                    # full command reference
```

Creation uses one screen for tools, region, optional Codex/Claude and GitHub
credentials, and optional shared `AGENTS.md`/`CLAUDE.md` instructions. The
persistent volume reaches `Ready` before the first deployment.
Check `Save as reusable setup` to remember those selections, then reuse them
without the dialog using `vmbox new worker-b --reuse`.
Every box trusts `/data/workspace` and defaults Codex and Claude to unrestricted,
non-interactive permission modes while preserving existing agent configuration.

## Forward a task

```bash
vmbox work -- codex "review the contracts, fix issues, test, and commit"
vmbox work -- codex exec "run this task non-interactively"
vmbox work -- claude "fix active tickets, then commit"
vmbox worker-a --detach -- codex "fix ticket 123, test, and commit"
```

Forwarded commands record `running`, `completed`, or `failed` under persistent
`/data`. Use `vmbox status <name>` or `vmbox wait <name>`; agents can add a
human-readable update with `vmbox-report "message"`.


## Detach without stopping work

1. Press `Ctrl-b`.
2. Release both keys.
3. Press `d`.

vmbox then shows the box's accrued cost and offers to permanently delete the
Railway service and `/data`; pressing Enter keeps it running.
Reconnect with `vmbox resume work`. After powering down, files remain in
`/data`; use `codex resume --last` to reopen Codex history.

Credentials are opt-in and stored under `/data/home`. Configuration lives in
`~/.config/vmbox/config`.

See [RAILWAY.md](RAILWAY.md) for detailed operational and agent documentation.
