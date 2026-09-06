# Web workspace implementation

Status: deployed in `4b9079b`; production terminal/VNC validation now has real
end-to-end evidence, with remaining failures and coverage gaps documented in
[the September 7 live report](WEB-WORKSPACE-LIVE-0907.md).
The release includes the requested CLI authentication/profile improvements.
Existing task-reconciliation drafts remain uncommitted and were excluded.

## Evidence collected, 2026-09-06

- Full Go tests/vet and four browser configuration/navigation tests pass.
- `tests/browser/terminal-live.mjs`: real Chromium → WebSocket test adapter →
  runtime PTY → private tmux recorder. Desktop and initially mobile 390×844
  input matches independently read bytes (unique text, Unicode, Enter,
  Backspace, arrows, Ctrl-C); close does not replay input or remove the session.
  Screenshots inspected at `/tmp/vmbox-browser-terminal-amiXaA/`.
- Real desktop container `vmbox-desktop-proof-0906`: TigerVNC handshake and
  visible Firefox framebuffer through noVNC; desktop process identity survives
  viewer disconnect and repeated desktop-start. Screenshot inspected at
  `/tmp/vmbox-desktop-live.png`. Reconnect test exposed and fixed tmux `-A`
  incorrectly attempting a terminal attach.
- Runtime/browser tests bypass production HTTP authorization and provider SSH;
  they do not prove the production controller stream works end-to-end.
- Production deployment `1489bd07-e7b2-4eb8-b7e5-51747020c9e5` succeeded for
  `4b9079b3aae30f24ae65a162012a16cc37225421`. Live 390×844 browser login,
  HttpOnly/Secure/Strict cookie, page reload without re-entering the token,
  workspace links and logout invalidation all passed, with no browser errors.
- Further real tmux checks passed: browser resize changes actual window
  dimensions; reconnect preserves the recorder process and does not replay input.
  Artifacts: `/tmp/vmbox-browser-terminal-0z2o0G/`.
- Desktop navigation through the Address bar button and native text-entry control
  reached Firefox's `about:robots`; screenshot visually verified at
  `/tmp/vmbox-desktop-navigation.png`. This is not a physical mobile-keyboard test.
- Both production slots are occupied (`analyze-tracker`, `tt2`). Approval for a
  temporary third slot/test box has been requested; neither user box was stopped.

Remaining: full controller-authenticated stream integration and production
validation; network interruption, scroll/selection and cross-session isolation
tests; package enablement validation; final cleanup/report. Temporary production
capacity must not be added until approved.

## Required behavior

- Clicking a logical box opens its own addressable workspace page.
- Opening the workspace resumes it through the same controller lifecycle as
  `vmbox BOX`, displays actual state/phase, and reuses its persistent shell.
- Terminal mode supports interactive tmux applications, resizing, Unicode,
  paste, control keys, scrolling and reconnection on desktop and mobile.
- Desktop mode starts an optional graphical environment with a browser and
  renders it through noVNC. Native VNC transport remains private.
- Closing the page disconnects the viewer, not the workspace. Explicit hibernate
  releases compute without deleting the volume. Processes do not survive compute
  hibernation; workspace files do.

## Existing integration points verified in source

- `POST /v1/logical-boxes/{id}/sessions/interactive` with `agent: shell` and
  `reuseShell: true` is the default CLI persistent-shell path.
- Session inventory and remembered primary-session APIs already exist.
- Terminal snapshot/input APIs exist, but snapshot polling is not a substitute
  for a faithful terminal emulator and bidirectional PTY stream.
- Providers support streamed execution; Railway resolves deployment SSH targets
  and uses direct OpenSSH. New streams must retain assignment fencing.
- The worker image has tmux but no desktop/VNC/browser packages.
- The admin UI currently holds its bearer token in memory only.

## Implementation sequence

1. Add a separate workspace page and box links. Share authentication through a
   short-lived server-side browser session or equivalent scoped stream tickets;
   do not put bearer tokens in URLs or persistent browser storage. Keep the
   administration page small and load terminal/desktop assets only in workspace.
2. Extract/reuse the CLI lifecycle contract for wake, transitional-state polling,
   shell reuse and explicit hibernate. Bound waits, show state/phase and offer
   retry without duplicating resume or session-creation requests.
3. Add owner-authorized, same-origin-checked streaming transport. Bind each stream
   to account, box, assignment generation and selected session. Close streams on
   reassignment or authorization expiry; bound connections, buffers and idle time.
4. Connect a locally bundled terminal emulator to a real tmux PTY. Resize the PTY,
   preserve byte ordering and never replay input after reconnect. Mobile controls
   include Esc, Tab, Ctrl and arrows plus keyboard/paste support.
5. Add an opt-in desktop runtime with a lightweight window manager, browser and
   loopback-only VNC server. Run as the workload user with persistent HOME. Bridge
   its fixed local endpoint through authenticated provider transport; accept no
   arbitrary client-supplied host or port. Bundle noVNC locally with its license.
6. Add desktop connect/reconnect/fullscreen/touch controls and clear installation,
   startup and unsupported-runtime errors. Existing workers need an explicit,
   non-destructive desktop enablement path; do not restart occupied compute merely
   to update an image.

## Completion evidence

- Unit tests: authorization, origin/ticket validation, account/session isolation,
  generation fencing, stream cleanup and lifecycle transitions.
- Real tmux browser test: independently recorded input bytes; changing screen,
  cursor and scrollback; resize; close/reopen; no replay or duplicate sessions.
- Real VNC browser test: launch browser inside remote desktop, interact through
  noVNC, reconnect to the same running desktop; no public VNC listener.
- Desktop and initially mobile 390×844 browser runs: reachable controls,
  selection, scrolling, fullscreen, no uncaught errors. Distinguish emulation
  from physical mobile keyboard/clipboard coverage.
- Production validation on a named disposable box, preserving existing boxes,
  followed by documentation and an explicitly scoped deployment. Record actual
  success, failures and remaining gaps; mocked endpoints alone cannot prove this.
