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

## Required remaining evidence

- Verify automatic first enrollment, activation and recovery on a disposable
  Railway allocation, beyond the local database/transport tests.
- Verify actual Railway compute replacement and retained journal storage. Local
  credential/epoch tests do not substitute for a live provider replacement.
- Controller restart, independent agent restart and compute replacement tested as
  different events, with honest process-loss behavior.
- New image/controller build, isolated Railway validation and additive rollout.
  Existing worker processes and volumes must not be restarted or reassigned just
  to validate migration. Verify exact surviving session identities before switch.
- Every intended production worker selected for direct transport; confirm live
  ordinary usage while Railway management is unavailable. No production migration
  or release of this direct-worker implementation has been performed yet.
- Configure and validate the optional webhook receiver against the intended
  Railway project. Local PostgreSQL tests verify durable deduplication, claim
  recovery and bounded refresh hints. Shared quota tests cover foreground and
  background reservations under one total limit. Webhooks never authorize
  runtime fallback or destructive actions from stale inventory.

Use `docs/RAILWAY-DIRECT-WORKERS.md` for the full acceptance contract. Older
progress paragraphs can be stale; code and executed verification take precedence.
