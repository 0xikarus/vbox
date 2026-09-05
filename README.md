# vmbox

Persistent remote boxes, controlled by a provider-agnostic CLI and a mandatory
controller. Use an interactive terminal or submit a one-shot command. The optional
lightweight web UI only configures resources; work happens through the CLI.

## Everyday use

Run **`vmbox`** to see your controller context and box states. It never opens a
dialog, prompts for credentials, starts compute, or connects to a shell.
Use **`vmbox menu`** for the optional arrow-key picker: Enter opens a shell,
or choose **Create a box**. The picker excludes deleting boxes; the overview
still shows them. Esc cancels without opening or creating anything.

```bash
vmbox                         # show context and box states
vmbox menu                    # optional box picker / create action
vmbox new work                # configure, create, then connect
vmbox work                    # return to its shell (wake if needed)
vmbox work --session          # pick another existing tmux session
vmbox task work               # pick Codex, Claude, or a shell one-shot
vmbox hibernate work          # stop compute, retain files
vmbox help                    # short guide; help --all for full reference
```

Inside the shell, start `codex`, `claude`, or your own command. To disconnect
without stopping it, press **Ctrl-a, then d**, and choose **Keep running**.
Bare `vmbox` is read-only in both terminals and scripts; `vmbox help` works offline.

## Install and connect

Go 1.26 or Docker builds the CLI. OpenSSH is required for native attachment.
No Railway, Docker or Incus client/token is needed for ordinary CLI operations.

In a terminal, missing controller configuration starts a connection guide. Missing
authentication offers hidden input for that invocation; tokens are not saved in
the CLI config. Scripts never prompt: configure the controller context and token
environment first. The controller/context banner is hidden unless you put
`--verbose` before the command.

```bash
./install.sh
vmbox context add team --controller https://YOUR-CONTROLLER
# Supply VMBOX_CONTROLLER_TOKEN through secure environment configuration.
vmbox ls --json
vmbox helper1
```

Controller login does not provision SSH credentials. Configure an SSH agent or
`VMBOX_SSH_IDENTITY_FILE`; native attachment requires the account owner role.
For an existing worker that has not enabled native sessions, run
`vmbox sessions helper1 --enable` once. This stages the matching runtime without
restarting worker compute or replacing existing sessions.

## Interactive mode

```bash
vmbox helper1                    # wake if needed; reconnect to the persistent shell
vmbox helper1 codex              # start interactive Codex without the picker
vmbox helper1 claude             # start interactive Claude without the picker
vmbox helper1 shell              # start a plain persistent shell
vmbox helper1 --start-cli 'claude' # new shell: run this command, then stay in shell
vmbox sessions helper1 --json
vmbox helper1 --session          # full-screen session picker: ↑/↓, Enter, Esc
vmbox helper1 --session NAME     # attach to this exact session and remember it
```

The controller remembers the shell session per logical box, across CLI clients.
Plain `vmbox BOX` reuses that shell or creates it if missing, without an agent
picker. Start Claude, Codex, or any other installed program yourself inside it.
If hibernation is already in progress, opening queues a durable resume request:
the controller finishes the safe unmount, then allocates and reconnects. It does
not interrupt an unmount. Progress shows the actual phase, such as
`hibernating · detaching-volume (resume queued)` or `restoring-tmux`.
Reconnecting to a running shell preserves its programs; it does not restart them.
Use `--session` to select other existing sessions; none are deleted and bare
`--session` never creates a session. Explicit agent overrides start a new session.
`--start-cli COMMAND` also creates a new shell, runs the command once, and leaves
a usable shell when it exits. Ordinary reconnects never replay this command.
Attachment validates the current session identity and box assignment.
Agents are optional: a box can contain shells and multiple agent sessions at once.
Normal `claude` and `codex` commands keep their interactive interfaces and menus.
You can run other installed tools, including OpenCode, from a shell.

Detach with **Ctrl-a, then d**, and choose **Keep running** at the CLI exit prompt.
Disconnecting keeps the worker and tmux processes running. Persistent interactive
sessions prevent one-shot tasks from automatically hibernating their box.
`vmbox hibernate helper1` is a separate explicit action: it saves workspace state,
stops live processes, and retains the disk. Restoration is not process survival.
Shell-first sessions restore as shells, without relaunching their previous agents
or replaying `--start-cli`.

## Creating a box

`vmbox new NAME` opens one persistent form for provider, location, disk size,
optional saved Claude/Codex login profiles, and an optional startup command.
Use ↑/↓ or Tab to move, ←/→ to change options, and Enter to edit text.
Profile uploads happen only after **Create**. Errors retain your entered values.
By default creation finishes by connecting, exactly like `vmbox NAME`.
The form also offers **Leave running** and **Leave hibernated**.

```bash
vmbox new research
vmbox new research --no-dialog --start-cli 'claude'
vmbox new batch --no-dialog --no-profiles --hibernate --json
vmbox new service --no-dialog --detach --start-cli './start-service.sh'
```

Scripts must explicitly use `--detach` (leave running) or `--hibernate`;
`--allocate` remains an alias for leave running. Actual lifecycle phases appear
inside the form; `--verbose` adds request IDs and retry counts.

## One-shot mode

Live lifecycle evidence and coverage limits: [shell-first verification](docs/SHELL-FIRST-VERIFICATION.md).

```bash
vmbox task helper1               # ↑/↓ agent selector, then task prompt
vmbox task helper1 codex --prompt "Fix the failing tests and verify the change" --json
vmbox task helper1 claude --prompt "Review the code and summarize your findings" --json
vmbox task helper1 shell --prompt "make test" --json

vmbox task-status helper1 TASK_ID
vmbox task-output helper1 TASK_ID
vmbox task-status helper1         # list one-shot tasks
```

The controller queues the command, allocates/restores the box if necessary, and
runs it in `/data/workspace`: `codex exec`, `claude -p`, or `bash -lc`. Existing
agent authentication, model and permission settings are retained. Codex permits
a workspace outside a Git repository using `--skip-git-repo-check`. There is no
automatic update-menu handling or interactive screen parsing in this path.

The agent tracks the progress and meaning of the work in its prompt. vmbox only
tracks **process execution**, including start/finish timestamps, the actual exit
code, and combined stdout/stderr:

- `exited` with exit code `0`: the process succeeded—not independent proof that
  the agent completed the requested work correctly.
- `exited` with a nonzero code: the exact process failure code is retained.
- Signal termination records the signal separately, with a null exit code.
- `launch_failed` or `unknown`: no fabricated exit code. Connection loss does not
  imply completion, and ambiguous execution is never automatically replayed.

Output/status commands do not wake compute. Status is readable by default;
task-output prints the captured text. Add `--json` for structured results.
The first 1 MiB of
combined output is retained on the worker and copied into controller storage;
truncation is explicitly reported. Running output is a periodically collected
snapshot, not a guaranteed live stream. Task results contain command output, so
avoid printing credentials in your commands. Results persist until their logical
box is deleted; deleting its volume/logical box also removes task history.

After exit, including nonzero exit, the controller automatically hibernates only
when there are no pending/unknown tasks, legacy active tasks, or live tmux sessions.
Automatic preparation refuses other live workspace processes instead of killing
them. Results are stored before releasing compute. Interactive siblings stay up.

For safe submission retries, supply the same `--idempotency-key KEY`; conflicting
reuse is rejected. `--session NAME` optionally names the one-shot tmux session,
which normally disappears when the process ends. Use task-output for retained
output. `--agent AGENT` remains an alias for the positional agent argument.
OpenCode one-shot execution is not implemented. Old API `/tasks` clients retain
their interactive behavior; new CLI one-shot tasks use `/process-tasks`.

`vmbox updates helper1 --json` separately reports changes in **live tmux snapshots**,
not task completion. It does not replace `task-status` or `task-output`.

## Controller administration

`vmbox providers` lists readable provider aliases. Bare `providers show` and
`providers default` offer an arrow-key picker. With no providers, the guide asks
for controller-defined configuration fields, accepts secret JSON from a secure
environment variable or hidden input, and confirms before saving. Missing default
selection is guided before resuming the original creation command.

Interactive box creation asks for a location from the controller's existing fleet
and remembers it per provider alias. `--region ID` overrides the picker; creation
fails if that region has no healthy free initialization slot. This does not create
new regional fleet capacity. The selected region is retained for direct and queued
restores; a box waits for matching capacity instead of moving regions. Regional
latency probing is not currently available.

```bash
vmbox providers schema
vmbox providers create railway primary --config-file railway.json --secret-env PROVIDER_SECRET_JSON
vmbox providers default railway primary
vmbox providers update railway primary --config-file image-edit.json --json
vmbox boxes update helper1 --default-agent codex
vmbox fleet slots set 2
```

Provider secrets stay encrypted on the controller; public reads never return
them. Updates are revision-protected and preserve omitted secrets. Retargeting
or deleting provider aliases requires explicit resource migration.
Standalone mode, provider-specific client contexts and web terminal sharing are
removed. Existing resources, credentials and legacy local bundles are preserved.

Read [controller-first contracts and migration](docs/CONTROLLER-FIRST.md),
[controller operations](docs/CONTROLLER.md), and [OpenAPI](docs/openapi.yaml).

## Saved agent login profiles

```bash
vmbox profiles                         # named Claude/Codex profiles, no secrets
vmbox profiles save codex work --from /path/to/codex-profile
vmbox profiles save claude personal --from /path/to/claude-profile
vmbox new research --profile codex=work --profile claude=personal
vmbox new clean-box --no-profiles
```

Profiles are encrypted under the controller account, with encryption bound to
the application and profile name. Saving an existing name fails instead of
overwriting it. Only supported profile files are uploaded (512 KiB total limit).
The list API returns metadata only; there is no plaintext export endpoint.

Interactive creation offers saved profiles, explicit local upload, or skip for
each application. Noninteractive creation without `--profile` provisions no agent
credentials. To upload from a script, save the local profile first, then select
it by name. Only selected profiles are copied into the new persistent volume;
creation recovery remembers those selections. Account owners manage and provision
saved profiles. Expired upstream logins still need renewal; saving a profile does
not establish that its authentication is valid.

## Deleting a box

`vmbox delete-volume BOX` requires typing the exact box name. It queues permanent
deletion and returns promptly; it does not claim the volume is already gone.
The controller finishes flush, detach, provider deletion and slot cleanup in the
background, and resumes interrupted attempts after restart. `vmbox` and
`vmbox status BOX` show the precise phase and any failure. Failed attempts retry
with a delay; an unexpected attached volume stops deletion rather than deleting
someone else's storage. Fleet size is unchanged; the cleaned slot becomes free.

Coworker MCP, inter-agent adapters and their CLI/web controls have been removed.
Ordinary multi-box shell access, saved login profiles and one-shot tasks remain.
Historical coworker data is retained only for safe cleanup; credentials are revoked.

## Build and verification

```bash
go build ./cmd/vmbox
go build ./cmd/vmbox-controller
go test ./...
go vet ./...
npm run test:browser
bash tests/run.sh
```

Native tests use a real isolated tmux/PTY when available. Database integration
requires `VMBOX_TEST_DATABASE_URL` pointing at disposable PostgreSQL. Browser
tests cover configuration, not real mobile keyboards or production agent replies.
Worker images remain controller/operator infrastructure; the installer ships
neither a standalone deployment bundle nor provider tooling.

The two-mode rollout was tested with real Claude/Codex responses, shell exit codes
0/7, independent worker output comparisons, and persistent interactive sessions.
Idle-hibernation guards were tested with real local tmux and disposable PostgreSQL.
A full live Railway allocate → execute → auto-hibernate test still needs an unused
disposable slot; the existing occupied boxes were intentionally preserved.
