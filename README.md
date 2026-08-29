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

## Install

Requirements: Bash, `jq`, the Railway CLI, and SSH.

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
vmbox start research
vmbox start research        # reconnects when it already exists
vmbox resume research       # fails when it does not exist
vmbox clean                 # review and type "clean"
vmbox clean --yes           # non-interactive
```

- `list` opens an Up/Down selector; Enter resumes the highlighted box and
  `q`/Escape exits. When no TTY is available it prints the regular table.
- `ls` prints every service in the configured project and environment together
  with its copy-paste `vmbox resume ...` command.
- `start` provisions a missing `vmbox-<id>` service or connects to an existing
  one. Partial provisioning is repaired on the next run.
- `resume` connects only when the named box already exists. It accepts both a
  box ID and the full name of an older, non-`vmbox-` service.
- `clean` deletes **every service** in the configured project and environment,
  not only services whose names begin with `vmbox-`.

The default target IDs live in `~/.config/vmbox/config`. Edit that file to use
another Railway project/environment or change `VMBOX_SERVICE_PREFIX`.

## VM image

Each provisioned service receives a persistent volume at `/data`. The image
includes tmux, Git, Node.js, npm, Codex CLI, Claude Code, Forge, Cast, Anvil,
and Chisel. Persistent home and workspace directories are `/data/home` and
`/data/workspace`.

Never commit Railway tokens, private keys, seed phrases, `.env` files, or agent
credentials.
