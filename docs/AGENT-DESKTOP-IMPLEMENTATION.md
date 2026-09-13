# Agent desktop MVP

The existing per-box workspace is the agent view. An agent owns one box; deleting
that box deletes its secrets, imported browser state and chat notes through foreign
keys. No Chief of Staff or separate messaging deployment is required.

## Startup and interaction

Desktop-enabled workers start X11 and a visible xterm attached to the same managed
tmux session used by the UI. Codex, Claude, OpenCode and shell have launch paths;
OpenCode is also supported by Run once and saved login profiles. Reopening a viewer
reuses the managed session. Closing a viewer leaves processes running. Wake restores
files/layout with a fresh agent conversation, without replaying old prompts.

Direct per-agent messages use the current controller messaging backend. A steer
sends Escape before pasting to a managed agent. `/silent` saves a note without
starting or waking the box. Existing message IDs retain retry protection. An
explicit Interrupt button targets the managed terminal. Client-specific handling
of Escape still depends on the client's current dialog/state.

The controller provisions a rotating assignment-bound credential through private
stdin. The runtime stores it in a mode-0600 file, never an MCP argument. It only
permits this box's secret operations; it is not an owner/controller API token.
Reconnecting refreshes this configuration, including after reassignment.

## Desktop tools

`vmbox-runtime desktop-mcp` provides:

- `desktop_screenshot`: PNG image content captured inside the worker.
- `desktop_move`, `desktop_click`, `desktop_drag`, `desktop_scroll`,
  `desktop_type`, `desktop_key`: bounded coordinates and literal input.
- `secret_ensure`, `secret_request`, `typeSecret`: opaque references and status.

Capture reads the worker's mode-0600 Unix VNC socket. There is no public VNC listener
and no UI-canvas capture. Full screenshots are 1280×800; worker-generated previews
are at most 320×200. Preview requests check account and assignment, never allocate
or start a desktop, and mark retained images stale when capture fails.

Movement uses cubic Bézier curves from the observed cursor position to the exact
target. Dragging holds/release the mouse button. An assignment-wide input lock
serializes managed actions. Human takeover sets a shared pause marker before
waiting for the current action; actions check it during movement and typing.
Screenshot reads remain available while paused. Tool operations have deadlines.
MCP cancellation notifications are not yet implemented; takeover cancels managed
input through its shared runtime marker.

Registration uses the clients' MCP CLI for Codex/Claude and merges OpenCode JSON
configuration while preserving existing entries. Existing OpenCode JSONC files are
left untouched with an actionable registration error; automatic JSONC editing is
not supported. `vmbox-runtime desktop-register AGENT` allows explicit registration.

## Chromium and private state

The profile lives at `$HOME/.config/vmbox/chromium` on the persistent box volume.
The browser's debugging endpoint binds only to loopback and uses an ephemeral port.
Cookie/storage import goes through the live browser, not direct profile database
writes. Uploads are encrypted for one account/box/import reference. Apply does not
wake a box. Deleting an upload removes its saved source, not already-applied browser
sessions. Use Chromium's own data-clearing controls to sign out/reset a profile.

The worker Docker image explicitly sets `VMBOX_CHROMIUM_NO_SANDBOX=true` because
common container hosts disallow Chromium's nested namespaces. This uses
`--no-sandbox`: the dedicated box is the isolation boundary, and a browser exploit
can access that worker user's files. Hosts supporting Chromium's sandbox should
set the variable to `false`. There is no silent fallback on sandbox failure.

Version 1 import example, with synthetic values:

```json
{
  "version": 1,
  "origins": [{
    "origin": "https://example.com",
    "cookies": [{"name": "session", "value": "test-value", "path": "/", "httpOnly": true, "expires": 2000000000}],
    "localStorage": [{"name": "preference", "value": "test-value"}]
  }]
}
```

Origins must be exact HTTPS origins. Cookies are exact-host and Secure; no domain
cookies. `expires` is optional Unix seconds. The import limit is 1 MiB / 32 origins.
IndexedDB, sessionStorage, unknown fields and unsupported formats are rejected.
Multi-origin application is not atomic: an error reports possible partial state
and retains the source for explicit retry. Native browser profile persistence also
retains Chromium's other supported storage between sessions.

## Secrets and private requests

References bind an encrypted value to account, box and HTTPS origin.
`secret_ensure(key,purpose="new_account_password")` durably creates an idempotent
password reference before use. Default length is 24; optional length/alphabet
constraints are validated. A retry retains the original value. Creation/filling
leave the entry pending; “Mark accepted by site” explicitly confirms metadata.

For existing credentials, `secret_request(key)` creates a private UI card. The user
supplies or cancels it outside chat. Fulfillment saves the password and sends only
a reference/status to the same active task. Submitting a credential never wakes a
sleeping box. It cannot silently replace an existing named secret.

`typeSecret(key)` resolves after the tool call and transports the value only over
private stdin to the worker. The CDP bridge checks the focused, visible, editable
password field's exact HTTPS origin, object identity and focus again before entry.
It neither submits nor copies to clipboard. Wrong origins, changed focus, stale
assignments and takeover reject the operation. Only status returns to the model.
Cross-origin frames and ambiguous password targets fail closed.

This prevents accidental disclosure through managed tool arguments/results and
chat; it does not isolate secrets from a malicious agent with unrestricted shell,
profile/CDP/X11 access or the page itself. No stronger confidentiality is claimed.

## Inactivity

New boxes default to four hours. Existing boxes keep automatic hibernation disabled
until the owner configures it in the workspace. Zero disables it. The controller
checks the policy, current assignment and task state under the release transaction.
Active managed tasks, private handoffs, human takeover, unknown foreground jobs or
an unavailable idle observer prevent automatic stop. X11 input and tmux activity
reset inactivity; screenshots and HTTP viewer heartbeats do not.

This is conservative: an interactive task marked active continues to prevent
automatic hibernation until cancelled. Arbitrary detached shell jobs are not a
complete workload registry; disable automatic hibernation for unmanaged background
work. The existing Hibernate button is the explicit shutdown operation and retains
the workspace volume. Compute/volume billing remains provider-specific.

## Verification and deployment status

Local evidence includes the full Go suite, real Chromium UI tests, disposable
PostgreSQL migrations/scope/encryption/request tests, real Chromium cookie and
localStorage persistence across restart, and a two-container X11 smoke test with
no VNC viewer. The latter covers visible session reuse, real typing into the
managed shell, drag/scroll, screenshots/thumbnails, takeover, cross-assignment
rejection, box-specific cursors and MCP PNG image exchange.

| Client | Implemented | Credential-free verification |
| --- | --- | --- |
| Codex | Interactive/Run once, MCP registration | Real CLI entry enabled; runtime MCP image exchange |
| Claude | Interactive/Run once, MCP registration | Real CLI reports desktop MCP connected |
| OpenCode | Interactive/Run once, MCP registration/profile import | Real CLI reports desktop MCP connected |
| Shell | Managed visible session | Real xterm/tmux identity and graphical keyboard input |

The disposable Debian Chromium 152 fixture measured aggregate process RSS of
1,260 MiB with a blank tab and 1,828 MiB with six synthetic tabs. RSS double-counts
shared pages; these are observations, not memory limits or a production capacity
recommendation. Real sites/model clients require workload-specific measurements.

Authenticated model vision tasks and provider deployment have not been exercised
by these local fixtures. Existing users' workers/volumes were not restarted or
modified. The tests use synthetic credentials, private disposable profiles and
explicitly named disposable containers.

Run `go test ./...` and `npm run test:browser`. Opt-in browser integration uses
`VMBOX_TEST_CHROMIUM`; disposable PostgreSQL uses `VMBOX_TEST_DATABASE_URL`.
`tests/desktop/Dockerfile` builds a fixture from an existing worker image plus a
freshly compiled runtime. Run `python3 tests/desktop/smoke.py IMAGE` and
`python3 tests/desktop/clients.py IMAGE`; both remove their test containers.
