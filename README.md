# vmbox

Persistent remote Linux boxes. Connect to a shell, run Claude, Codex, or OpenCode,
or submit a one-shot task. The controller manages providers, compute and storage;
your CLI connects to the controller, not directly to provider APIs.

Developing or continuing this repository? Read the [agent architecture and testing
guide](docs/AGENT-GUIDE.md) for implementation entry points and lifecycle rules.

Agent desktop startup, MCP tools, private browser imports, secrets and inactivity
configuration are documented in [the desktop MVP guide](docs/AGENT-DESKTOP-IMPLEMENTATION.md).

## Quick setup

When creating a box, select **Enable desktop** under Optional tools to prepare a
desktop and Chromium browser without Blender. Selecting Blender requires this
option. The desktop selection is retained across restores. Controller and Grid
viewers still prefer an available desktop, with TMUX available manually; leaving
the option unchecked does not disable a desktop already included in the worker.

### 1. Install

You need Git, OpenSSH, and either Go 1.26 or Docker on your laptop.
On Windows, follow the [Windows setup](#windows-setup-wsl) below instead.

```bash
git clone https://github.com/0xikarus/vmbox-service.git
cd vmbox-service
./install.sh
```

The CLI installs to `~/.local/bin`. Open a new terminal if `vmbox` is not found.

### 2. Connect to your controller

Ask your controller administrator for its HTTPS URL and your login token.
If you are setting up the controller itself, start with the
[operator setup guide](docs/CONTROLLER.md).

```bash
vmbox context add team --controller https://YOUR-CONTROLLER
vmbox context use team
vmbox whoami
```

When asked, paste the token into the hidden prompt. The CLI verifies and saves it
locally for future commands. `vmbox logout` removes this saved login.
An exported `VMBOX_CONTROLLER_TOKEN` overrides the saved token; unset it if stale.
Enrolled workers use your controller token for terminal and desktop access.
A registered SSH key is still needed for legacy workers and optional Railway
port forwarding; load it into an SSH agent or set `VMBOX_SSH_IDENTITY_FILE`.
Native shell access requires the account owner role.

### 3. Configure capacity (controller owner, first time only)

Skip this step if your administrator already configured the fleet.

```bash
vmbox providers                  # inspect existing providers
vmbox providers create           # guided setup, if none exists
vmbox providers default          # choose the default provider
vmbox fleet location             # choose location before provisioning slots
vmbox fleet slots set 1          # provision one compute slot; incurs provider costs
vmbox fleet status              # wait for healthy capacity
```

Choose location on an empty fleet; this command does not migrate existing boxes.
Each running box needs a compute slot. A hibernated box retains its disk but frees
its compute slot. Creating a workspace also needs a healthy free slot temporarily.

### 4. Save logins (optional)

Log in to Claude, Codex or GitHub locally first, then run:

```bash
vmbox profiles upload
```

The table lists discovered accounts and their source paths. Use **↑/↓** to move,
**Space or Enter** to select profiles, then select **Upload** and press Enter.
**Add entry** accepts an undiscovered path or GitHub account. Names are derived
from account identity; no manual naming is required. Nothing is uploaded until
you submit. You can also select local logins while creating a box.

### 5. Create and connect

```bash
vmbox new work
```

Choose provider, location, disk size and any saved/local logins. Leave the startup
command blank for a shell. **Create and connect** opens the box when ready.
Run `claude`, `codex`, `opencode`, or any shell command inside it.

Disconnect with **Ctrl-a, then d**. Leave the box running to preserve its processes.
Reconnect with `vmbox work`; hibernation preserves files, not running programs.

The web workspace's **Agent chat** starts the selected managed agent when no
reusable session exists. OpenCode receives that first message through its native
startup prompt so it appears in the visible TUI. Later messages enter the running
client through Codex queue, Claude channel, or OpenCode's loopback API. OpenCode
sessions auto-approve permission asks by default unless their configuration
explicitly denies them. Chat messages accept pasted,
dropped, or selected PNG/JPEG/GIF images; agent replies can include images too.
Agent choice requests render as radio buttons or checkboxes. The managed
`vmbox-desktop` MCP supplies the structured `chat_message` and `chat_ask`
tools; `chat_message` works with or without a reply reference. The
same MCP exposes the desktop tools (`desktop_screenshot`, `desktop_click`,
`desktop_type`, `desktop_key`), so agents can operate the box's computer.

Agents can also address each other when the owner grants a contact edge.
`get_contacts` lists the boxes this box may message, and `chat_message`/`chat_ask`
accept an optional `contact`; the message is delivered into that box's same
native conversation and appears in the chat app attributed to its sender. A box
with the `manager` role (`vmbox new NAME --role manager`) may address every
non-protected box automatically; a worker only has explicit edges. The owner
edits the graph per box in the workspace page or the chat Details drawer and can
mark a box protected. Changes apply immediately and the controller authorizes
every send, so the model cannot widen its own reach.

## Windows setup (WSL)

Use Ubuntu inside Windows through WSL. The current installer supports Linux and
macOS, not native PowerShell. Your boxes still run remotely; you do not need a
second controller or Railway fleet.

1. Open **PowerShell as Administrator** and run:

   ```powershell
   wsl --install -d Ubuntu-24.04
   ```

   Restart if asked, then open **Ubuntu** from Start and create its local username
   and password. See [Microsoft's WSL instructions](https://learn.microsoft.com/en-us/windows/wsl/install).

2. Run the remaining commands **inside Ubuntu**, not PowerShell:

   ```bash
   sudo apt update
   sudo apt install -y git gh openssh-client golang-go
   gh auth login
   ```

   Sign in with a GitHub account that has access to this private repository, then:

   ```bash
   gh repo clone 0xikarus/vmbox-service
   cd vmbox-service
   GOTOOLCHAIN=auto ./install.sh
   source ~/.bashrc
   ```

   Go downloads the compiler version required by the project automatically.
   See [Go toolchain selection](https://go.dev/doc/toolchain).

3. Connect to the existing controller (replace the URL):

   ```bash
   vmbox context add team --controller https://YOUR-CONTROLLER
   vmbox context use team
   vmbox whoami
   ```

   Enter your controller token at the hidden prompt. It is verified and saved
   inside Ubuntu for reuse. Enrolled workers use this token for terminal and
   desktop access. Legacy SSH access and port forwarding require a registered
   private key inside Ubuntu, through an SSH agent or
   `VMBOX_SSH_IDENTITY_FILE`. Do not upload or share your private key.

4. Once the controller is connected:

   ```bash
   vmbox                 # list boxes and useful commands
   vmbox new work        # create and connect
   vmbox work            # reconnect later
   ```

   Disconnect with **Ctrl-a, then d**, then choose **Leave unchanged** to keep
   programs running. Saved controller profiles are already available; Windows-local
   Claude/Codex/OpenCode logins are not automatically discovered inside Ubuntu. Use local
   Linux logins or existing saved profiles. This WSL path has not been verified
   end-to-end on a Windows machine in this repository's current test run.

## Common setup problems

- **Cannot authenticate:** run `vmbox logout`, unset a stale exported
  `VMBOX_CONTROLLER_TOKEN`, then run `vmbox whoami` to log in again.
- **No healthy free compute slot:** inspect `vmbox fleet status`. Hibernate an
  unneeded box or have the owner add capacity. Check that the region matches.
- **SSH permission denied on a legacy worker or port forward:** check your SSH
  agent/key; controller login does not authenticate Railway SSH.
- **Agent login expired:** refresh the login locally, then run
  `vmbox profiles upload` again. Saved profiles are snapshots, not live sync.

More detail: [controller administration](#controller-administration),
[interactive sessions](#interactive-mode), [one-shot tasks](#one-shot-mode).

## Everyday use

Run **`vmbox`** to see your boxes, their states and a short command helper.
It does not open a box picker, start compute, or connect to a shell.
If authentication is missing, a terminal can prompt for your controller token.

```bash
vmbox                         # show context and box states
vmbox whoami                  # show authenticated account, user, and role
vmbox new work                # configure, create, then connect
vmbox work                    # return to its shell (wake if needed)
vmbox task work               # pick Codex, Claude, OpenCode, or a shell one-shot
vmbox hibernate work          # stop compute, retain files
vmbox delete work             # permanently delete box and files; asks for confirmation
vmbox help                    # short guide; help --all for full reference
```

Inside the shell, start `codex`, `claude`, `opencode`, or your own command. To disconnect
without stopping it, press **Ctrl-a, then d**, and choose **Leave unchanged**.
Bare `vmbox` is read-only in both terminals and scripts; `vmbox help` works offline.
Use `vmbox whoami --json` for machine-readable identity. This requires a controller
with the `/v1/whoami` endpoint and does not display credentials.

The web admin panel’s **Profiles** section shows an expandable account → application
→ profile tree. Owners can delete saved profiles (with confirmation)
and select them when creating a box. Uploads happen only through the CLI. Saved credentials are encrypted,
never exported, and immutable: upload refreshed credentials under a new name.
Profiles are account-wide, not assigned to individual users; existing boxes are
unchanged. Browsers cannot discover local logins automatically; use the CLI for that.
Run `vmbox profiles upload` to detect and upload local Claude/Codex/OpenCode/GitHub logins
in a table without creating a box or allocating compute. Space or Enter toggles
each profile's upload checkbox; **Add entry** adds a custom path or GitHub account. There is no
name prompt: names use the account email/username/ID when available, otherwise
the source directory, with a suffix for existing names. For scripts,
use `vmbox profiles save APPLICATION NAME --from SOURCE` (`SOURCE` is a local
directory for Claude/Codex/OpenCode, or `HOST:USER` for GitHub).
In the creation dialog, press Enter on a login row to expand its profile list inline.
Saved profiles reuse controller credentials; selecting a detected Local login uploads
it when you create the box. Custom local path is only for an undetected location.
GitHub defaults the saved profile name to the selected username (with a suffix if taken).
Use ↑/↓ and Enter to select; `d` followed by `y` deletes a saved profile.
Deletion is permanent and may interrupt pending creations referencing that profile.

## Install and connect

Go 1.26 or Docker builds the CLI. OpenSSH is required for legacy worker
attachment and Railway port forwarding.
No Railway, Docker or Incus client/token is needed for ordinary CLI operations.

In a terminal, missing controller configuration starts a connection guide. Missing
authentication offers hidden input and saves a verified token in a separate local
0600 file under the CLI config directory, scoped to context and controller URL.
This includes bare `vmbox`. Run `vmbox logout` to delete that saved token (not revoke
it on the server). Environment tokens take precedence when accepted; use
`unset VMBOX_CONTROLLER_TOKEN` to clear an exported token too. In an interactive
terminal, a rejected environment token falls back to the saved login before
asking again, so a stale shell export cannot hide your saved replacement.
If neither works, the CLI asks for a replacement and saves it after a successful
retry; permission denials and server outages do not prompt.
Scripts never prompt: configure the controller context and token
environment first. The controller/context banner is hidden unless you put
`--verbose` before the command.

```bash
./install.sh
vmbox context add team --controller https://YOUR-CONTROLLER
# Run vmbox whoami and enter your token when prompted.
vmbox ls --json
vmbox helper1
```

Enrolled workers carry native attachment through the authenticated controller
connection. For a legacy worker, configure an SSH agent or
`VMBOX_SSH_IDENTITY_FILE`; native attachment requires the account owner role.
For an existing worker that has not enabled native sessions, run
`vmbox sessions helper1 --enable` once. This stages the matching runtime without
restarting worker compute or replacing existing sessions.

## Interactive mode

### Copy and paste

- Drag over text and release to copy it into tmux's buffer.
- For keyboard selection: **Ctrl-a s**, move to the start, **Space**, move to
  the end, then **Enter** to copy.
- **Ctrl-a v** pastes the tmux buffer. Applications requesting bracketed paste
  receive the paste as a block, rather than individual typed lines.
- To use your laptop clipboard instead, hold **Shift** while dragging, then use
  your terminal's copy/paste shortcuts (usually **Ctrl-Shift-C/V** on Linux,
  **Cmd-C/V** on macOS). Terminal shortcuts vary.

Tmux also exports copies via OSC 52 when the terminal supports and permits it.
Some terminals do not; use local selection in that case. These managed bindings
require the updated worker tmux configuration; installing the CLI alone does not
update an already-running worker's configuration.

### Desktop from the CLI

```bash
vmbox desktop helper1 --enable  # First time: install desktop packages on the worker
vmbox desktop helper1           # Resume and open the same desktop in a local window
```

Install a TigerVNC-compatible `vncviewer` locally (on Debian/Ubuntu:
`sudo apt install tigervnc-viewer`), or pass `--viewer /path/to/vncviewer`.
The viewer needs a working graphical display; run the command from your normal
desktop terminal. Windows users run the CLI and Linux viewer inside WSL with GUI
support, rather than using a native Windows CLI installer.

The controller manages wake-up and desktop startup. The CLI then opens a
single-viewer, loopback-only VNC connection through the enrolled worker agent.
Legacy workers use SSH. Neither path requires a local provider token or SDK.
Closing the viewer closes this local connection, **not** the remote desktop or
box. Use `vmbox hibernate helper1` when you want to release compute.

For a different viewer, use `vmbox desktop helper1 --no-viewer` and open the
printed `vnc://127.0.0.1:PORT` address locally. Ctrl-C closes the tunnel. Only the
first viewer connection is accepted; rerun the command to reconnect. As with
an ordinary localhost tunnel, use this only on a trusted local machine.

`--enable` is explicit consent to install optional packages on that worker; a
replacement worker may need it again. Without it, missing components produce
an error rather than an automatic package installation.

### Web workspace

Click a box name in the controller to open `/boxes/BOX_ID`. The page wakes the box
through the allocation queue and opens an enabled desktop automatically. Otherwise
it reconnects to the same persistent shell used by `vmbox BOX`. Desktop and TMUX
tabs switch between both views; the managed shell is prepared before either
viewer attaches. The separate workspace page loads its own terminal and desktop assets; the
configuration panel remains lightweight.

Terminal input is streamed to a real tmux PTY. Mobile controls provide Esc, Tab,
arrows and common Ctrl keys. Closing/reloading the page disconnects the viewer,
not the session; reconnect does not replay input. Hibernate explicitly stops
processes and releases compute while retaining workspace files. Copy and Paste
buttons move text between the browser clipboard and either viewer. A taskbar
inside the remote desktop lists open windows: click to raise/restore or minimize,
and scroll to switch windows. It appears in every VNC viewer, including Grid.
A connection row above the Desktop/TMUX tabs shows viewer state, VNC round-trip
latency to the box, and separate HTTP latency to the controller. Measurements use
the existing VNC stream and controller health endpoint, without Railway API polling.
Desktop ping is unavailable until a supporting desktop is connected.

On the box page, expand **CPU, RAM & connection** to load and edit the current
slot's CPU/RAM limits or resolve its SSH address. These owner-only reads run on
demand. Saving limits does not request a worker restart; configured limits may
need a later restart to take effect. Limits stay with the compute slot when a
workspace moves elsewhere. Lowering RAM can terminate applications.
The connection section generates a loopback-only SSH port-forward command for a
chosen application port. Railway's SSH gateway is not a public worker IP; public
HTTP domains/TCP proxies must be configured separately. Use your registered SSH
key and refresh the address after a worker replacement.

Desktop enablement installs the panel; reconnecting starts it alongside existing
applications without restarting VNC or the box.

New default worker images include a desktop, which opens automatically in the
interactive web workspace. TMUX remains available in its tab and through the CLI.
The desktop has Chromium, Terminal and Files launch icons, plus Blender when installed.
Applications launch when you select them. New interactive shells use `DISPLAY=:99`
for this shared screen; this does not itself give agents screenshot or mouse tools.
Operators can build a shell-only image with `VMBOX_DESKTOP=false`.

On older workers, choose **Enable desktop packages**, then **Start /
reconnect desktop**. Enablement installs TigerVNC, Openbox and Chromium on the
current Debian-compatible worker without restarting it. Older or custom shell-only
replacement images may need enablement again. VNC is available only through a private Unix
socket and the owner-authenticated controller stream, never a public VNC port.
Chromium may require reduced sandbox protection when the provider restricts user
namespaces; the worker image controls that setting.

Browser login uses an eight-hour, HttpOnly, same-site cookie backed by controller
memory. Logout invalidates it; controller restart requires login again without
stopping worker processes. Enrolled worker streams use the authenticated agent;
legacy provider streaming remains available until those workers migrate.

Rebuild bundled assets with `npm ci && node scripts/build-web.mjs`. noVNC and
xterm licenses are retained alongside the bundles. The feature is deployed, but
production worker streaming is not yet validated; see `docs/WEB-WORKSPACE.md`
for evidence and remaining verification.

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
You can also run any installed agent or tool directly from a shell.

Detach with **Ctrl-a, then d**, and choose **Leave unchanged** at the CLI exit prompt.
Disconnecting keeps the worker and tmux processes running. Persistent interactive
sessions prevent one-shot tasks from automatically hibernating their box.
`vmbox hibernate helper1` is a separate explicit action: it saves workspace state,
stops live processes, and retains the disk. Restoration is not process survival.
Shell-first sessions restore as shells, without relaunching their previous agents
or replaying `--start-cli`.

## Creating a box

`vmbox new NAME` opens one persistent form for provider, location, disk size,
optional saved Claude/Codex/OpenCode login profiles, and an optional startup command.
The form automatically discovers saved controller profiles and local Claude/Codex/OpenCode
credential files in default and alternate profile directories, including
`CODEX_HOME` and `CLAUDE_CONFIG_DIR`. Login rows show saved/local counts; use
Enter to expand the inline profile list. Active local profiles are labeled.
Config-only directories are excluded. Detection does not verify login expiry or
upload credentials: **Skip** remains the default, and uploads occur only on Create.
For another directory, choose the custom local path option and edit its path.
Use ↑/↓ or Tab to move, ←/→ to change options, and Enter to edit text.
Profile uploads happen only after **Create**. Errors retain your entered values.
By default creation finishes by connecting, exactly like `vmbox NAME`.
The form also offers **Leave running** and **Leave hibernated**.
New boxes created in the web controller or through the API also start automatically;
API callers can send `"allocateWhenReady": false` to leave one hibernated.

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
vmbox task helper1 opencode --prompt "Implement the change and run its checks" --json
vmbox task helper1 shell --prompt "make test" --json

vmbox task-status helper1 TASK_ID
vmbox task-output helper1 TASK_ID
vmbox task-status helper1         # list one-shot tasks
```

The controller queues the command, allocates/restores the box if necessary, and
runs it in `/data/workspace`: `codex exec`, `claude -p`, `opencode run --auto`,
or `bash -lc`. Existing agent authentication, model and permission settings are retained. Codex permits
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
OpenCode one-shot execution uses `opencode run --auto`; explicit OpenCode deny
rules still take precedence. Old API `/tasks` clients retain their interactive
behavior; new CLI one-shot tasks use `/process-tasks`.

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

Fleet placement is controller-managed. In the web UI, open **Capacity → Slot
location → Load available locations** to choose and save a fleet region. This
requires an empty fleet and does not migrate existing slots or workspaces.
`vmbox fleet location` opens a region
picker populated from the provider API; scripts can use
`vmbox fleet location set REGION`. Railway discovery is project-scoped and
requires permission to query `regions`; authorization failures are shown, not
replaced by a guessed list. The configured project token may not allow this query.
The controller validates the selected region against the same live catalogue.

Changing placement currently requires **no logical boxes and zero desired and
actual slots** for that provider alias. For an already-empty fleet, run
`vmbox fleet slots set 0`, wait for `vmbox fleet status` to show zero actual slots,
select its location, then scale back up. Existing boxes/volumes are never migrated
by this command. Do not delete workspaces just to change placement. A saved region
is retained across scaling and controller restarts; new slots use it explicitly.

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
Standalone mode and provider-specific client contexts are removed. Existing
resources, credentials and legacy local bundles are preserved. The optional
web workspace is described above, including its verification limits.

Read [controller-first contracts and migration](docs/CONTROLLER-FIRST.md),
[controller operations](docs/CONTROLLER.md), and [OpenAPI](docs/openapi.yaml).

## Saved agent login profiles

Creation also offers GitHub accounts discovered by `gh auth status`, including
keychain-backed logins. Only the selected account is exported after Create;
Skip remains the default. To save one explicitly:
`vmbox profiles save github work --from github.com:YOUR-USER`, then create with
`--profile github=work`. The controller encrypts this token per account/profile
and provisions a private `~/.config/gh/hosts.yml` for the box user.

Malformed or expired saved Claude/Codex/OpenCode profiles are rejected before a slot or
volume is reserved. Refresh locally with the corresponding client's login command,
save under a **new profile name**, then select that profile and retry creation.
Existing saved profiles are immutable snapshots, not a live sync of local logins.

After transfer, creation checks the selected logins as the unprivileged box user.
Claude/Codex/OpenCode checks include a brief one-shot provider request (small agent usage
charges may apply); GitHub checks the selected account with the API and configures
its HTTPS git credential helper. Failures stop creation rather than claiming the
box is authenticated. CLI output and credential contents are not included in errors.

```bash
vmbox profiles                         # named Claude/Codex/OpenCode profiles, no secrets
vmbox profiles save codex work --from /path/to/codex-profile
vmbox profiles save claude personal --from /path/to/claude-profile
vmbox profiles save opencode openrouter --from /path/to/opencode-profile
vmbox new research --profile codex=work
vmbox new clean-box --no-profiles
```

Profiles are encrypted under the controller account, with encryption bound to
the application and profile name. Saving an existing name fails instead of
overwriting it. Only supported profile files are uploaded (512 KiB total limit).
The list API returns metadata only; there is no plaintext export endpoint.

Interactive creation offers one saved Claude, Codex, or OpenCode profile, or no
profile. Selecting one also selects that application's harness; a box cannot
carry competing agent profiles. Noninteractive creation without `--profile`
provisions no agent credentials. To upload from a script, save the local profile
first, then select it by name. The selected profile is copied into the new
persistent volume and creation recovery remembers it. Account owners manage and
provision saved profiles. Expired upstream logins still need renewal; saving a
profile does not establish that its authentication is valid.

## Managed agent instructions

Reusable **instruction presets** are named Markdown guidance sets stored on the
controller account, separate from login credentials. Owners manage them in the
controller UI (**instruction presets**) and in the chat app (**Presets**); any
user may select one when creating a box. Presets are trusted user-authored agent
guidance — never secrets, never setup scripts, and uploaded Markdown is never
executed.

At creation, choose **Account default preset / none**, an explicit **None**, a
named preset, or a custom Markdown copy. The selected Markdown is copied into
the box as an immutable **snapshot** with preset/version provenance. Editing or
deleting a preset later never changes boxes that already copied it.

Existing boxes change only through an explicit **Instructions…** action
(controller box list and chat box menu): it previews the current snapshot, lets
you apply None, a preset, or edited/custom Markdown, and reports whether the
running box accepted it. A running box is updated in place; a stopped box keeps
the selection pending and applies it during its next start. Agents never restart
automatically: a new conversation or a restarted agent process reads the new
instructions, while an already-running session keeps what it loaded.

One canonical per-box file holds the guidance:
`~/.config/vmbox/instructions.md`. It is linked into each agent's global
instruction slot:

| Agent (verified versions) | Slot | Notes |
| --- | --- | --- |
| Codex CLI 0.154 | `~/.codex/AGENTS.md` | global instructions merged into the model-visible prompt |
| Claude Code 2.1 | `~/.claude/CLAUDE.md` | user memory; project `CLAUDE.md` files keep their normal precedence |
| OpenCode 1.18 | `~/.config/opencode/AGENTS.md` | config-directory instructions; project `AGENTS.md` files keep their precedence |

Links are tracked in a vmbox ledger. A pre-existing file at one of those paths
is never overwritten or appended to: vmbox reports it as a conflict and that
agent keeps its own file. Repository-owned `AGENTS.md`/`CLAUDE.md` files are
never written. Applying **None** removes only vmbox-owned links. Snapshots live
in the controller database and are re-applied idempotently on hibernation
resume, worker replacement, and shared-worker recovery.

### Editing the login profiles imported into a box

Owners can replace the single saved agent profile imported into an existing box
(controller box list → **Credentials…**). The profile application becomes the
box harness. Credentials are written through the same verified channel used at
creation, with the assignment locked and volume checked; contents are never
displayed. Files belonging to the other harnesses are removed. On a running box,
stale agent sessions and tasks are closed so the next message starts the selected
harness with the new credentials; shells remain open. A stopped box queues the
selection and performs the same reconciliation on its next start. **Re-sync**
reapplies both saved instructions and the selected profile. Clearing the profile
removes the portable agent credential files without changing the chosen harness.

## Deleting a box

The controller UI also has a **Delete** button in each box row. Confirm the named
box to stop its processes and permanently delete its workspace volume. Other
boxes and shared compute services are kept. Deletion phases update automatically;
provider failures stay visible. If progress cannot be checked, use **Refresh**.

`vmbox delete BOX` works without connecting first and requires typing the exact
box name. It queues permanent
deletion and returns promptly; it does not claim the volume is already gone.
The controller finishes flush, detach, provider deletion and slot cleanup in the
background, and resumes interrupted attempts after restart. `vmbox` and
`vmbox status BOX` show the precise phase and any failure. Failed attempts retry
with a delay; an unexpected attached volume stops deletion rather than deleting
someone else's storage. Fleet size is unchanged; the cleaned slot becomes free.

Coworker MCP, inter-agent adapters and their CLI/web controls have been removed.
Ordinary multi-box shell access, saved login profiles and one-shot tasks remain.
Historical coworker data is retained only for safe cleanup; credentials are revoked.

## Optional tools

Select **Blender** in the controller's box creation tool list (CLI:
`--tool blender`) to install Blender, automatically enable desktop components,
and install the third-party Blender MCP bridge and add-on.
Opening an interactive box in the web workspace automatically starts and attaches
its desktop; manual desktop controls remain available for recovery. Launch `blender`
from its terminal, start its MCP server from the Blender add-on panel, and start a
new Codex, Claude or OpenCode session to use the registered MCP tools.
Grid prefers enabled desktops. Newly enabled
Blender presets install the checksum-verified official Linux x64 Blender 5.1.2
release; Blender MCP is pinned to 1.9.1. Existing legacy Blender presets keep their
distribution version, including after hibernation; live boxes are not upgraded.
Dedicated and shared workers use the same image-bundled Blender and MCP versions.
Shared boxes have distinct workspace-specific MCP ports; no manual port selection
is needed. New boxes automatically use available capacity in the least occupied
pool; expand **Placement** in the creation form to override the pool.
The preset is
restored after hibernation and adds download, disk and RAM usage. MCP telemetry is
disabled, safe mode is enabled, and its Blender socket listens only on loopback.
Existing `blender` MCP client entries are preserved. GPU rendering remains unverified.

Select **Foundry** when creating a box. In the CLI
form, use Space or Enter to toggle its checkbox. It includes `forge`, `cast`,
`anvil`, and `chisel`; no separate selections are necessary.

For scripted use, pass `--tool foundry` to `vmbox new NAME` or
`vmbox task BOX shell --prompt 'forge --version'`. The preset downloads a pinned,
SHA-256-verified Linux release before your command starts. Installation can take
a few minutes and requires about 120 MiB of downloads plus workspace disk space.
Installed tools live on the persistent home volume and survive hibernation.

The default worker image preinstalls Python 3 (`python` and `python3`), pip,
venv, pipx, uv/uvx, Node.js 22 with npm/npx, and Bun. These are available without
selecting a preset or downloading them when a box starts. Use `uv venv` or
`python -m venv .venv` for project dependencies rather than changing system Python.
uv is pinned to 0.12.15 and its official image digest; Python uses Debian's
maintained packages. This applies to newly built worker images, not an in-place
upgrade of running workers or existing shared hosts.

Tool presets are optional: unchecked presets add no installation or download. Operator
worker images may already contain tools; unchecking a preset does not remove them.
Installation failures are
reported instead of running a task without its requested tools. Existing custom
executables are never overwritten. Installing Anvil does not start it or expose
its RPC port; start and forward it explicitly when needed.

## Grid view

Open **Grid** to view all running interactive boxes automatically. The default
uses two columns and enough rows for every box. Choose a fixed layout to limit
visible tiles; unavailable boxes are replaced by the next available box. Inventory
refreshes every 15 seconds, and failed connections have a 30-second retry cooldown.

Each tile prefers an enabled desktop and offers a Desktop/TMUX selector. TMUX
reuses the primary interactive session, or creates a persistent shell if none
exists. Input and clipboard controls belong to the selected tile.

Boxes with unfinished one-shot tasks are excluded. Sleeping
boxes stay asleep. Next box, reducing the layout, logout, and closing the page
only disconnect viewers; remote work keeps running. Mobile screens stack tiles.

## Custom tooling

Expand **Add custom tooling** in box creation and enter trusted Bash
install commands, for example `sudo apt-get update && sudo apt-get install -y ripgrep`.
The commands run inside the worker before the task, with a five-minute limit.
Use package managers or your own installer; never paste credentials. Installation
failure prevents the task from starting and is reported as `setup_failed`, not a
successful task exit. Logs are in `~/.config/vmbox/tool-setup.log`.

For persistent boxes, the saved recipe runs again on resume, including replacement
compute. Make it safe to run repeatedly. One-shot workspaces are deleted after use.
CLI forms include the same optional field; scripts can use
`vmbox new NAME --setup-script 'INSTALL COMMANDS'` or
`vmbox task BOX shell --setup-script 'INSTALL COMMANDS' --prompt 'COMMAND'`.

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
