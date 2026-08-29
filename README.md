# vmbox-service

Private tooling for disposable Railway development boxes with persistent data.

## How boxes work

Each box is a separate Railway service named `vmbox-<box-id>`. There do not
need to be any services beforehand. `vmbox start <box-id>` discovers an
existing service or creates one, attaches a `/data` volume, deploys the VM
image, waits for it to become ready, and opens a persistent tmux-backed Railway
SSH session.

Container replacements lose processes but preserve `/data`. Starting an
existing box reconnects to the service; `resume` requires the box to exist.
`vmbox stop <box-id>` removes the active deployment while preserving the
service and volume. Resuming a stopped box deploys the installed bundle again.
Running tmux and agent processes do not survive a power-down.

## Install

Requirements: Bash, `jq`, the Railway CLI, and SSH. The installer updates the
Railway CLI automatically. If its self-updater leaves an old global package
pinned, the installer refreshes `@railway/cli` with Bun or npm and verifies
cost reporting support.

```bash
gh repo clone 0xikarus/vmbox-service
cd vmbox-service
./install.sh --workspace-token
source ~/.bashrc
```

The token prompt does not echo. The workspace token is stored as
`RAILWAY_API_TOKEN` in `~/.config/vmbox/credentials` with mode `0600`; it is
never written to the shell profile or repository. Railway SSH and service
provisioning require an account/workspace token or an interactive Railway
login; a project token is insufficient for SSH key management. The installer
tests access to the configured project before replacing a stored credential.

The installer adds `vmbox` to `~/.local/bin`, copies the deployment bundle to
`~/.local/share/vmbox/service`, creates `~/.config/vmbox/config`, and adds one
managed PATH entry to Bash or Zsh. It does not overwrite existing config.

```bash
./install.sh --no-shell-update
./install.sh --uninstall
```

Uninstall removes the local CLI, deployment bundle, and stored token. It does
not delete Railway services; use `vmbox clean` first if that is intended.

## Commands

```bash
vmbox                       # print help
vmbox help
vmbox list                  # interactive picker; Enter resumes
vmbox ls                    # script-friendly table
vmbox cost                  # current-period project/service costs
vmbox cost research         # cost for one box
vmbox auth research         # choose local agent profiles to upload
vmbox github research       # sync a selected local gh account
vmbox research              # create if missing, otherwise connect
vmbox new research          # same as above
vmbox start research        # same as above
vmbox new research -- claude "fix active tickets, then commit"
vmbox research -- codex "review and fix the contracts"
printf '%s' "prompt" | vmbox research -- claude -p
vmbox resume research       # fails when it does not exist
vmbox stop research         # remove deployment, preserve /data
vmbox clean                 # review and type "clean"
vmbox clean --yes           # non-interactive
```

- `list` opens an Up/Down selector; Enter resumes the highlighted box and
  `q`/Escape exits. When no TTY is available it prints the regular table.
- `ls` prints every service in the configured project and environment together
  with its copy-paste `vmbox resume ...` command and tmux detach reminder.
- `cost` shows accrued current-period totals split into CPU, memory, volume,
  egress, and backups.
- Append `-- COMMAND [ARG...]` to any box-opening form to run that command
  directly in its tmux session. Arguments retain their boundaries and stdin is
  forwarded, so pipes work as expected.
  On a new box, interactive credential selectors use `/dev/tty`, leaving piped
  prompt input untouched for the forwarded command.
- `vmbox <id>`, `new`, and `start` provision a missing `vmbox-<id>` service or
  connect to an existing one. Partial provisioning is repaired on the next
  run. For a newly created box it auto-detects Codex profile directories
  matching `~/.codex*` and Claude profile directories matching `~/.claude*`,
  prioritizing `CODEX_HOME` and `CLAUDE_CONFIG_DIR`. The opt-in multi-select
  allows one profile per tool, so Codex and Claude can be uploaded together.
  Nothing is copied by default; press `a` to specify another profile directory.
- A chosen Codex profile uploads `auth.json`, `config.toml`, and named
  `*.config.toml` profile files. A chosen Claude profile uploads only
  `.credentials.json`, `settings.json`, and `.claude.json` when present. MCP
  definitions in those machine-readable configs are included; Markdown files,
  histories, caches, sessions, and databases are intentionally excluded.
  Every uploaded file is verified by checksum. When a login is included,
  vmbox also confirms that the corresponding CLI recognizes it inside the box.
  `vmbox auth <box-id>` reopens the same profile picker for an existing box.
- New boxes also offer an explicit opt-in selector for locally authenticated
  GitHub CLI accounts. Nothing is selected by default: use Space to select one
  account and Enter to continue. The chosen token is streamed into the box,
  `gh auth setup-git` configures Git
  access, its existing repository/org permissions are preserved, and Git commit
  name/email are derived from the selected GitHub account. Run
  `vmbox github <box-id>` to resync GitHub separately.
- `resume` connects only when the named box already exists. It accepts both a
  box ID and the full name of an older, non-`vmbox-` service. If the box is
  powered down, it deploys the bundle again before connecting.
- `stop` removes the active Railway deployment while retaining the service and
  persistent volume. CPU/memory usage stops, but volume storage can still incur
  cost. After resuming, use `codex resume --last` to reopen persisted Codex
  history; tmux processes cannot survive a deployment removal.
- `clean` deletes **every service** in the configured project and environment,
  then deletes **every active persistent volume**, including mounted `/data`.
  Railway records already pending deletion are ignored, and `--yes` never opens
  an interactive volume selector.

The default target IDs live in `~/.config/vmbox/config`. Edit that file to use
another Railway project/environment or change `VMBOX_SERVICE_PREFIX`.

## VM image

Each provisioned service receives a persistent volume at `/data`. The image
includes tmux, Git, GitHub CLI, Node.js, npm, Codex CLI, Claude Code, Forge, Cast, Anvil,
Chisel, Bubblewrap, and sudo. The shell runs as root, so sudo is optional.
Persistent home and workspace directories are `/data/home` and
`/data/workspace`.

## Leaving and resuming

To leave Codex running inside tmux:

1. Press `Ctrl-b`.
2. Release both keys.
3. Press `d`.

Reconnect with `vmbox resume <box-id>`. Do not type `exit` unless you intend
to stop the running shell/session. This guide appears after installation, in
`vmbox help`, in `vmbox ls`, and before every connection.

Never commit Railway tokens, private keys, seed phrases, `.env` files, or agent
credentials. Selected profile files are streamed over Railway SSH to fixed
paths under `/data/home`; directories use mode `0700` and files use mode
`0600`. They are never added to the Docker image or Git repository.
