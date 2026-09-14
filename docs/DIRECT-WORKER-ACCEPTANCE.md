# Direct-worker delivery audit

Current objective: every intended box runs an authenticated worker agent;
controller screenshots, input, terminal, file and runtime traffic use that agent.
Railway manages infrastructure lifecycle only. This audit is not a completion
claim or a substitute for the implementation plan.

## Verified locally on 2026-09-14

- Runtime routes resolve through the direct-worker provider. An enabled enrollment
  cannot silently fall back to Railway when the rollout flag is disabled or the
  worker is offline. The backing provider in the integration fixture rejects
  Railway execution, connection lookup, inspection and bootstrap.
- Real local TLS agent plus disposable PostgreSQL integration passes, including
  assignment rebinding, free-slot transitions, binary streams, credential/account
  boundaries, connection epochs and offline behavior.
- Controller, worker agent, protocol, transport and Railway provider package tests
  pass with local socket permissions. These package runs do not prove production
  readiness or live desktop model behavior.
- Completed operation journal results reconcile after an agent-process restart;
  incomplete claims remain ambiguous and commands are not replayed. Durable
  request identity retains account, box, slot, assignment and command fields.
- A network-disabled disposable desktop container and real local TLS agent pass
  terminal command-output assertions, pause/input rejection/resume, graphical
  input, 1280×800 PNG capture and socket reconnect preserving tmux identity. The
  backing provider rejects Railway calls. This exercises real vmbox-runtime,
  TigerVNC and xdotool. It also exercises owner HTTP screenshot authorization,
  independent agent-process restart and replacement of the controller server
  object behind a stable TLS listener. Sessions survive these events; the
  controller fixture explicitly expires its isolated lease rather than waiting
  for a real process crash and lease timeout.
- The same isolated controller → TLS agent → desktop fixture passes five owner
  thumbnail HTTP captures with PNG dimensions 320×200 and capture timestamps.
  Chromium workspace tests cover immediate preview refresh after desktop start,
  including a failed capture still in flight, and direct-worker connection text.
- A caller-supplied HTTP transport inherits the configured persistent request
  budget instead of silently using a process-local gate.
- Disposable PostgreSQL verifies shared quota usage/cooldowns and preservation of
  the learned conservative remote limit after headerless or larger-limit replies.

- Full `go test -race ./...`, `go vet ./...`, and disposable PostgreSQL tests
  passed on the isolated release snapshot after enrollment-order and
  replacement-target review fixes. The final missing-tmux-socket probe was also
  exercised successfully in the real, fresh desktop image.
- New allocation and creation enroll before runtime setup. Attaching enrollment
  uses one pinned legacy installation command, then proves empty tmux state and
  binds the assignment through the authenticated agent. Existing running migration
  preserves its verified session baseline. PostgreSQL tests exercise direct setup
  after activation with Railway runtime access forbidden.
- Replacement tests verify deployment target validation before credential
  rotation, previous credential/epoch rejection and assignment fencing. Isolated
  shell tests preserve journal storage across replacement installations.

## Live rollout on 2026-09-14

PR #73 merged as `48f92eac043a24aef71bd8aeafca513d1cbffed5` and the
controller deployed successfully. `VMBOX_DIRECT_WORKERS=1` is enabled with one
controller replica. The initial local management cap of 80 requests/hour was
exhausted during rollout; the operator raised it to 400, below the learned remote
allowance of 8,000. No remote cooldown was active at diagnosis.

The existing `testbox` migrated by sidecar installation and verified activation,
without a worker restart. Live checks returned two existing sessions, a
`controller-worker` terminal connection, and a 1280×800 PNG captured inside the
box. The user subsequently deleted `testbox`; it must not be recreated for
verification. Further lifecycle checks use the disposable box only.

The disposable box `direct-smoke-disposable-cefff417` exposed a separate startup
problem: its reserved slot used deprecated `europe-west4`. Only that test slot was
changed to the replacement EU region. The resulting compute replacement exposed
an initial-enrollment recovery gap: a credentialed but not yet enabled worker
remained pinned to the old deployment. PR #75 fixed runtime staging before
assignment binding; after its deployment this box reached `running`. A live
session listing, creation of the `vmbox` shell session, and native connection
lookup returned `controller-worker`. Two unique terminal-input requests returned
409 ambiguous, but later terminal snapshots contained both command output
markers and prompts. They were not replayed. The shell-input acknowledgement
fix has a regression test; production confirmation remains pending.

Desktop packages were enabled successfully on this disposable, but the user
started deleting it before desktop launch or a live screenshot. Its asynchronous
delete stalled in `delete-detaching-volume` after a Railway detach timeout, then
completed; the controller now returns 404 for that box. A second isolated box,
`thumbnail-ui-disposable-29c8b5e9`, was created without login profiles and
automatically reached `running` after enrollment and allocation. Its old base
required explicit desktop package enablement, which succeeded without a worker
restart. Direct HTTP captured a valid 320×200 PNG from its live desktop.

Real Chromium exposed a separate display failure: the thumbnail request returned
200, but the workspace Content Security Policy blocked the `blob:` image URL, so
the visible image remained broken. PR #77 added `blob:` to the interactive-page
image policy and deployed as `100ffd97499b553e08321b69445a570ee351f4fa`.
After that rollout, five consecutive Chromium refreshes displayed fully decoded
320×200 worker thumbnails. Five initial 409 capture responses during desktop
startup recovered to a visible image when the worker became ready. The controller
restart briefly reported the agent reconnecting; session survival across that
restart was not explicitly asserted. Deletion of this second disposable and its
volume was accepted with exact-name confirmation and remains asynchronous.

The shared Railway management budget reached its configured 400 requests per
rolling hour during this work. Aggregate diagnostics showed 400 local requests,
92 background, a learned remote allowance of 8,000, and no remote cooldown.
Volume-operation polling now backs off to 20 seconds in production; its effect on
the hourly total is not yet measured. An attempted controller cap increase was
rejected by automatic approval review, so the production cap remains 400.

## Required remaining evidence

- Verify subsequent allocation and recovery after a live compute replacement;
  initial enrollment and allocation reached running on the isolated thumbnail box.
- Verify actual Railway compute replacement and retained journal storage. Local
  credential/epoch tests do not substitute for a live provider replacement.
- Controller restart, independent agent restart and compute replacement tested as
  different events, with honest process-loss behavior.
- Complete isolated Railway validation and the remaining additive rollout checks.
  Existing worker processes and volumes must not be restarted or reassigned just
  to validate migration. Verify exact surviving session identities before switch.
- Every intended production worker selected for direct transport; confirm live
  ordinary usage while Railway management is unavailable. The live `testbox`
  migration above is verified; forced management-outage evidence is local only.
- Configure and validate the optional webhook receiver against the intended
  Railway project. Local PostgreSQL tests verify durable deduplication, claim
  recovery and bounded refresh hints. Shared quota tests cover foreground and
  background reservations under one total limit. Webhooks never authorize
  runtime fallback or destructive actions from stale inventory.

Use `docs/RAILWAY-DIRECT-WORKERS.md` for the full acceptance contract. Older
progress paragraphs can be stale; code and executed verification take precedence.
