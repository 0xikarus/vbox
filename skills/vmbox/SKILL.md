---
name: vmbox
description: Use the vmbox service to connect to controller-managed remote boxes, work in persistent tmux shells or graphical desktops, submit and inspect one-shot tasks, and import Claude, Codex, OpenCode, or GitHub login profiles.
---

# Using vmbox

vmbox manages persistent remote Linux workspaces through a central controller.
The controller owns provider credentials, compute allocation, storage, and saved
login profiles. Ordinary clients need controller authentication and a separate
SSH identity for native connections—not a Railway token or provider SDK.

Use the user's chosen controller and box. Start with `vmbox ls --json` and
`vmbox whoami --json`; these do not wake compute. Bare `vmbox` prints boxes and
a short helper. Check `vmbox help --all` for additional installed commands.
Access does not imply permission to upload credentials, add paid capacity,
hibernate sibling work, or delete data.

## Install and authenticate

From a checkout of `https://github.com/0xikarus/vmbox-service`:

```bash
./install.sh
vmbox context add team --controller https://YOUR-CONTROLLER
vmbox context use team
vmbox whoami
```

Installation needs Git, OpenSSH, and Go 1.26 or Docker. The binary installs to
`~/.local/bin`. Windows uses Ubuntu in WSL, not a native PowerShell installer.

Ask the administrator for the controller URL and token through a secure channel.
In a terminal, the CLI prompts for missing/rejected authentication with hidden
input, verifies the token, and saves it locally for this context and URL.
`vmbox logout` clears that local login, not the server token.
`vmbox context list` shows configured contexts.

Scripts never prompt: configure context/authentication beforehand. A securely
supplied `VMBOX_CONTROLLER_TOKEN` can be used. Accepted environment tokens take
precedence over saved logins; unset stale exports when troubleshooting. Never
print tokens or place their values in commands, reports, or versioned files.

Native shell/desktop access requires the account owner role and a registered
SSH key. Load it in an SSH agent or set `VMBOX_SSH_IDENTITY_FILE` to its path.
Controller login does not provision SSH credentials.

## Create and configure a box

```bash
vmbox new work
```

The form selects provider, location, disk size, optional saved/local logins, and
an optional startup command. Leave the command blank for a shell. Creation
normally connects automatically. Enter on a login row expands profile choices
inline. Saved profiles reuse controller credentials; selecting a Local profile
uploads a snapshot on Create. Skip imports nothing.

For automation, explicitly choose the post-creation state:

```bash
vmbox new batch --no-dialog --no-profiles --hibernate --json
vmbox new work --no-dialog --no-profiles --detach --json
vmbox new work-with-login --no-dialog --detach --profile codex=ACCOUNT_NAME --json
```

Even creating a hibernated box temporarily needs a healthy free slot in a
compatible region. Inspect `vmbox fleet status` if unavailable. Configure
providers/capacity only when requested: `vmbox providers`, `vmbox providers create`,
`vmbox providers default`, `vmbox fleet location`, and
`vmbox fleet slots set COUNT`. Increasing slots incurs costs. Choosing a location
does not migrate existing boxes.

## Import login profiles without creating a box

Log in locally with the relevant tool, then:

```bash
vmbox profiles upload
vmbox profiles list --json
```

The upload table discovers Claude, Codex, OpenCode, and GitHub accounts. Arrows move;
Space/Enter toggles each profile; select Upload to submit. Add entry accepts an
undiscovered directory or GitHub account. Names derive from account identity
when available, otherwise the source directory, with suffixes for collisions.
Discovery alone neither uploads credentials nor proves login validity.

For non-interactive import, explicit save still requires a name:

```bash
vmbox profiles save claude ACCOUNT_NAME --from /path/to/claude-profile
vmbox profiles save codex ACCOUNT_NAME --from /path/to/codex-profile
vmbox profiles save opencode ACCOUNT_NAME --from /path/to/opencode-profile
vmbox profiles save github GITHUB_USER --from github.com:GITHUB_USER
```

Upload only selected accounts. Saved profiles are encrypted, account-wide
snapshots, not continuous synchronization. Refresh expired logins locally and
upload a fresh profile; existing boxes are not automatically updated. Owners
can delete saved profiles from the web Profiles tree or creation picker. Web
uploading is unsupported. Confirm deletions: pending creations may reference them.

## Persistent shell and tmux

```bash
vmbox work                       # wake if needed; attach to remembered shell
vmbox sessions work --json        # inspect existing sessions
vmbox work --session              # existing-session picker
vmbox work --session NAME         # select exact session and remember it
```

Run `claude`, `codex`, `opencode`, or ordinary commands inside the shell. Plain reconnect
reuses the remembered session without restarting programs. Prefer it over
creating extra sessions. When explicitly needed,
`vmbox work --start-cli 'COMMAND'` creates a new persistent shell, runs the command
once, and leaves a shell afterward. Reconnect never replays that command.
Interactive attachment needs a terminal/PTY; use one-shot tasks for unattended
agent execution instead of scripting a full-screen TUI.

- Detach: Ctrl-a, then d; choose Leave unchanged to keep programs running.
- Scroll/select: Ctrl-a s; Space starts selection, Enter copies.
- Paste tmux buffer: Ctrl-a v. Mouse drag/release also copies with managed bindings.
- Local clipboard: Shift-drag to select locally, then use terminal copy/paste
  shortcuts. OSC 52 export depends on terminal support and permissions.

These bindings require managed worker tmux configuration; a local CLI update
alone does not replace an already-running worker's configuration. After a
dropped connection, inspect/reconnect rather than replaying uncertain input.

## Graphical desktop and web access

```bash
vmbox desktop work --enable       # explicitly install optional worker packages
vmbox desktop work                # resume/reconnect graphical desktop
vmbox desktop work --viewer /path/to/vncviewer
vmbox desktop work --no-viewer    # print local address for another viewer
```

Use a local TigerVNC-compatible `vncviewer` and graphical display. On Ubuntu,
install with `sudo apt install tigervnc-viewer` when requested. Windows uses WSL
with GUI support; this path has not been live verified. A headless agent must
not claim a visible desktop merely because a tunnel started.

The controller manages resume/startup; the CLI carries VNC over private SSH to
a single-client localhost listener. No public VNC port is needed. With
`--no-viewer`, connect to the printed address and keep the command running.
Viewer exit or Ctrl-C closes the tunnel, not the remote desktop or box. Rerun to
reconnect. Use localhost tunnels only on a trusted local machine.

Clicking a box in the web controller opens its workspace with the persistent
tmux shell and the same desktop. Choose Enable desktop packages, then Start /
reconnect desktop. Replacement workers may need enablement again. Closing the
tab only disconnects. Browser streaming currently requires Railway's supported
transport; do not assume equivalent GUI support for every provider.

## One-shot tasks and actual exit codes

```bash
vmbox task work                   # interactive agent/prompt selector
vmbox task work codex --prompt 'Fix the tests and verify the change' --idempotency-key UNIQUE_TASK_KEY --json
vmbox task work claude --prompt 'Review the implementation and report findings' --json
vmbox task work opencode --prompt 'Implement the change and run its checks' --json
vmbox task work shell --prompt 'cd /data/workspace/PROJECT && make test' --json
vmbox task-status work TASK_ID --json
vmbox task-output work TASK_ID
```

The positional agent or `--agent codex|claude|opencode|shell` selects `codex exec`,
`claude -p`, `opencode run --auto`, or `bash -lc`. Tasks start in `/data/workspace`, waking the box when
necessary. Submission returns a task record, not completed work. Save its ID;
inspect status/output with bounded polling and report unfinished tasks honestly.
Reads do not wake compute. Reuse a stable unique idempotency key for the same
submission when retrying an ambiguous response; don't enqueue duplicate work
with a fresh key just because a request timed out.

Put desired work, progress tracking, and verification in the agent's prompt.
vmbox tracks process execution: real exit code, signal, timestamps, and bounded
combined output. `exited` with code 0 means process success, not verified task
correctness. Nonzero codes are preserved; signals have a separate field and null
exit code. `unknown`/`launch_failed` are not successful completion. Inspect output
and work products; do not bypass authentication or change models to manufacture
success. Captured output may be truncated and must not contain secrets.

After completion the controller hibernates only when no sibling sessions,
pending/unknown tasks, or other protected work prevent it. Do not force hibernate
just because one task exited.

## Lifecycle and recovery

```bash
vmbox status work --json
vmbox hibernate work
vmbox delete work
```

Disconnect preserves processes. Hibernate releases compute and retains files,
but stops processes: restoring a shell is not restoring its running agent.
Opening a hibernating box queues resume after safe unmount, rather than cancelling
the unmount. Show the actual phase and observe with a bounded wait.

Delete permanently removes the box, volume/files, and task history; confirm the
exact target and user intent. A 502 or SSH/network failure is not evidence of bad
credentials, task completion, or safe deletion. Inspect state before retrying
mutations; never repair capacity by deleting unknown slots or volumes.

For deeper operator setup, consult the repository README and
`docs/CONTROLLER.md`; do not introduce provider-specific client dependencies into
ordinary box workflows.
