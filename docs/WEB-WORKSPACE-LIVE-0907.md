# Web workspace live test — 2026-09-07 (Europe/Berlin)

Tested the deployed controller, real Railway SSH transport, worker PTY/tmux,
Chromium/xterm and noVNC/Firefox. No backend responses or agent answers were
mocked. This tested the web-workspace goal, not the older three-agent test plan.

Started on `2da00a9`, controller deployment
`89401618-3d28-4972-8136-6fbab70865ab`. The startup fix `7649c4a` subsequently
deployed successfully as `2b5fd109-8ea2-4740-a18f-2d9c04622483`.

## Results

| Case | Result | Evidence |
| --- | --- | --- |
| Real browser input | PASS | Desktop and initially 390×844 mobile each delivered exactly 77 expected bytes: unique Unicode text, Enter, Backspace, four arrows, Ctrl-C/D and bracketed multiline paste. Independent file read over SSH matched; no duplicates. |
| Screen synchronization | PASS, bounded | Independently captured tmux and browser both displayed changing unique markers, line replacement and cursor text; cursor coordinates matched `0,3`. A later real PTY output marker appeared within 896 ms, including SSH overhead; one sample, not a latency percentile. |
| Session isolation | PASS, same box | Two real sessions. Two old stream frames deliberately delayed 1.2 seconds did not overwrite the new screen. Twenty input bytes reached only the selected sibling; the original recorder file was unchanged. |
| Recovery | MIXED | Desktop/mobile page reload and explicit reconnect after emulated network interruption preserved the recorder PID and bytes. A separate desktop reload timed out at 45 seconds; a fresh attempt returned to the same shell and VNC PIDs. Cause of that intermittent timeout remains unproven. |
| Desktop/mobile controls | PARTIAL | Terminal fullscreen entered/exited in both viewports; programmatically selected text and scroll position remained stable during output. No uncaught browser errors. Ctrl-a d did **not** produce a disconnected UI state within 15 seconds. |
| Real desktop | PASS, desktop viewport | Browser Enable installed packages in about 20 seconds. Production noVNC displayed Firefox; address-bar/input controls navigated to `about:robots`, visually confirmed. VNC PID 3258 and Firefox PID 3262; `/proc/net/tcp*` showed no TCP listeners. Reconnect retained VNC PID. |
| Hibernate/resume | PASS | Mobile browser hibernate completed in 33.9 seconds, released the slot and retained the same volume. Browser resume completed in 37.4 seconds and recovered a unique workspace file on that same volume. Processes are not expected to survive hibernation. |

## Failures and changes

- Initial creation/runtime staging encountered “no running deployment instance”
  and recovered via reconciliation. It was not a first-attempt clean creation.
- The controller-only restart exposed prolonged HTTP 502 downtime. Source
  started HTTP only after synchronous initial reconciliation; logs showed a
  Railway provider timeout before the eventual listening message. `7649c4a`
  moves that ordered initial pass into the background. Regression coverage
  verifies HTTP health/authentication responses while startup work is blocked.
  Full Go tests/vet passed, the fix deployed, and live `whoami` succeeded.
- Independent worker boot ID, tmux/VNC PIDs and recorder SHA-256 were identical
  before restart and after controller deployment. No worker restart was issued.
- The existing worker image still renders the old `nobold]` footer fragment.
  Box/slot/state/connection values are populated. Updating the controller runtime
  does not automatically replace the worker's tmux configuration file.
- Ctrl-a d's missing disconnect indication needs a focused stream-exit test.
  Do not infer session destruction or successful detach from the timeout.
- The desktop harness initially used unavailable `ss`, then had a JavaScript
  quoting error in its replacement diagnostic. Those harness failures were fixed;
  the later VNC/socket observations above are from the corrected check.

## Coverage limits

Synthetic browser typing/paste events do not prove a physical phone keyboard,
OS clipboard or touch interaction. Selection/scroll checks used xterm's API,
not physical mouse/touch selection. Full-screen clearing occurred on the sibling
recorder, but there is no exhaustive cell-by-cell screen-diff proof. Network
interruption used browser emulation, not a real radio/Wi-Fi outage. No second
production box was driven for cross-box isolation. Mobile VNC interaction,
long idle periods and sustained output/backpressure remain unverified.

These were the first-pass failures, not an all-green run. The stream-exit and
mobile follow-up below supersedes the corresponding open verification items.
The unrelated Codex startup/task-reconciliation drafts remain excluded.

### Stream-exit follow-up

A real local subprocess test reproduced an input-pipe deadlock: the child exited,
but `OSRunner.RunAttached` did not return while the browser-side writer remained
open. The controller used `io.Pipe`, causing `os/exec` to wait on a blocked stdin
copy goroutine. Switching this transport to `os.Pipe` lets the child inherit its
stdin descriptor directly. The regression failed before the change and passed
afterward; full Go tests/vet also passed. It deployed as `4248b0a` and was then
verified in production as described below.

### Final production follow-up

Used only new disposable box `web-final-0907`
(`16625725-d168-4d4d-8764-fa2900000000`), on a pre-existing healthy free slot.
The owner's `ttf` box was not operated on.

- **PASS:** Ctrl-a d produced a disconnected browser state in 1.318 seconds;
  the original shell PID 186 survived.
- **PASS:** three full page reloads returned to that same shell PID in
  5.361, 9.582 and 6.701 seconds, without creating replacement shells.
- **PASS:** desktop enable/start through the 390×844 touch-enabled browser
  completed in 24.187 seconds.
- **PASS:** tapping Address bar and using the desktop keyboard control navigated
  the real Firefox to `about:robots`. Both normal and fullscreen screenshots were
  visually inspected; controls stayed within the mobile viewport.
- **PASS:** mobile VNC fullscreen entered/exited, and reconnect retained VNC
  PID 3345. No uncaught browser exceptions occurred.
- Initial cold resume reported `Failed to fetch`, while independent controller
  inspection showed the same box had reached `running / restored`. Reconnecting
  to that existing operation worked; no replacement allocation or worker restart
  was issued. The precise network failure remains unproven, not relabeled as a
  harness bug. Network errors now explain that the operation may still be running
  and offer explicit reload/reconnect guidance without replaying input.
- A successful detach previously displayed an unwarranted runtime-enable hint.
  Stream-close feedback now says the stream ended and offers reconnect, without
  guessing that the runtime is mismatched.

Final artifacts: `/tmp/vmbox-final-0907/results.json`, `mobile-firefox.png`,
`mobile-fullscreen.png`, and `cleanup.json`. The focused harness is
`/tmp/vmbox-final-0907.mjs`. The JSON results describe the successful follow-up;
the initial cold-resume failure is preserved in this report.
Cleanup permanently deleted only this disposable box and test volume in 46.9
seconds. Desired capacity remained four, with three free slots and one occupied
user slot; the test assignment was gone.

## Requirement audit

| Requested behavior | Authoritative evidence |
| --- | --- |
| Click a box into its own workspace page | Production `/boxes/ID` pages; browser navigation regression test checks the box link and shell-reuse request. |
| Same lifecycle as `vmbox BOX` | Both use allocation and `sessions/interactive` with `agent: shell`, `reuseShell: true`; live cold resume and hibernate/resume recovered the retained volume. |
| Faithful interactive tmux in the browser | Independently verified received bytes, changing screen/cursor state, session isolation, resize, reconnect, detach and process identity. |
| Real optional VNC desktop and browser | Production package enablement, private Unix-socket VNC, visible Firefox navigation, and retained VNC process across reconnect. |
| Usable on mobile/on the go | Initially 390×844 terminal and VNC runs, reachable controls, touch-emulated address-bar interaction, input, fullscreen and explicit network recovery. Physical device/clipboard limitations remain as stated above. |
| Persistent workspace, viewer disconnect is not deletion | Worker process and input identities survived page and controller restarts; hibernation retained the exact volume and unique workspace file. |

These checks establish the requested feature, not perfect availability: transient
provider/network failures can require explicit reconnect. This report does not
claim the underlying sporadic network failures have been eliminated. The existing
worker-image footer cosmetic issue and physical-device coverage remain separate
limitations, not proof of data loss or loss of the persistent workspace.

## Scope and artifacts

Only disposable box `web-proof-0907`
(`7ba129d4-2633-485c-8266-fd0800000000`) was created and driven. A healthy free
slot was available, so no extra capacity was added by the test. The owner changed
desired capacity to four during testing; that choice is preserved. No commands
were issued to operate on the owner's other boxes.

Cleanup completed in 51.9 seconds: the disposable box and its test-only volume
were permanently deleted. Final controller inventory showed four healthy free
slots and no remaining test assignment. Local evidence artifacts were retained.

Local evidence is in `/tmp/vmbox-live-0907/`: `initial.json`, `terminal.json`,
`mobile.json`, `desktop.json`, `reconnect.json`, `isolation.json`, `lifecycle.json`,
the before/after restart identity files and screenshots. `desktop.json` retains
the original reload timeout; `reconnect.json` records the successful follow-up.
`cleanup.json` records final disposable-resource cleanup.

Harnesses: `/tmp/vmbox-live-0907.mjs`, `/tmp/vmbox-proof-checks.mjs`,
`/tmp/vmbox-reconnect-0907.mjs`, `/tmp/vmbox-isolation-0907.mjs` and
`/tmp/vmbox-lifecycle-0907.mjs`. They are scoped to this disposable box, not a
general unattended production test suite. Credentials were read privately from
ignored local configuration; none are included in the report or artifacts.
