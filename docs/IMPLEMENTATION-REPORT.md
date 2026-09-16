# Controller-first implementation evidence

> Historical evidence from 2026-09-05. For the current product contract and
> production handoff, use `AGENT-GUIDE.md` and `NEXT-AGENT-TODO.md`.

2026-09-05. Implemented locally on the existing worktree; no commit or deployment.
Production services, worker processes, sessions and volumes were not changed.
The original Codex/startup/reconciliation drafts were preserved. Unrelated
`.worktrees/` content was not inspected or changed.

| Area | Result | Evidence / limits |
|---|---|---|
| Controller-only CLI | PASS (local) | Native SSH transport, exact session selection, updates/ack, provider administration and cached status. Compiled CLI dependency graph contains no Railway/Docker/Incus provider adapters. Standalone flag fails; bootstrap requires separately built operator binary. |
| Native session identity | PASS (local) | Real isolated tmux: empty inventory, multiple sessions, prefix collision, Unicode/space names, same-name recreation, wrong assignment, and restarted-server incarnation rejection. |
| Input/detach/reconnect | PASS (local PTY) | Real client PTY attached to tmux; independent raw program recorded unique text, QWERTZ Unicode, CR multiline input, Backspace, arrows, Ctrl-C/D/Z. Exact bytes matched, in order, without duplicates; detach/reconnect added no bytes and retained session. This is not remote SSH proof. |
| Updates / acknowledgements | PASS (PostgreSQL integration) | Baseline/unchanged/changed, user-separated checkpoints, stale acknowledgements, late empty probe rejection, verified exit and sibling preservation. Additive schema migration applied twice. No transcripts retained. Actual controller-process restart not tested. |
| Provider editing / roles | PASS (local) | Real PostgreSQL: omitted secrets preserved, stale revision rejected, legacy PUT cannot replace existing alias, target changes rejected, config secret fields rejected. Ordinary users denied native connection, native enabling and provider PATCH. Live provider permissions not tested. |
| Configuration-only web | PASS | Three browser tests: small self-contained assets, desktop editing, initialized 390×844 mobile editing. Default agent, revision-protected provider edit and capacity request checked. No terminal/message/task/group requests; no uncaught errors; no horizontal overflow. No real-phone keyboard claim. |
| Build / packaging | PASS | Go 1.26 `go test ./...` and `go vet ./...`; operator-tag CLI tests and bootstrap build; five shell/installer checks; gofmt and `git diff --check`. Installed client no longer bundles workers or standalone deployment source. |
| Production native SSH / recovery | BLOCKED pending approved rollout | New controller/runtime code is not deployed. Fresh-client SSH authentication, real transport interruption, controller-only restart and assignment rollover need disposable production/staging tests. |
| Real agents | NOT TESTED on new path | Claude, Codex, OpenCode individually and Claude+Codex simultaneously still require real prompt/answer tests after rollout. Existing startup-menu/ambiguous-delivery failures are not declared fixed in production. |

Artifacts: `internal/boxruntime/native_test.go`, `native_pty_test.go`,
`internal/controller/native_integration_test.go`, `internal/cli/native_test.go`,
`internal/transport/ssh_test.go`, `tests/browser/controller-ui.test.mjs`;
screenshots `/tmp/vmbox-config-desktop.png`, `/tmp/vmbox-config-mobile.png`.

The container's VCS stamping failed with `error obtaining VCS status: exit status
128`; verification succeeded with `GOFLAGS=-buildvcs=false`. No Git settings or
worktree contents were changed to bypass that tooling issue. Native Unix sockets
and Chromium required the execution environment's sandbox approval.

An initial PTY detach test lacked the worker's Ctrl-a binding; it was corrected to
configure that binding explicitly. The restarted-tmux test waits/retries only its
idempotent metadata setup while the old daemon releases its socket. Terminal
input/attachment is never automatically retried. Both final real-tmux tests pass.

## Deliberate limits / rollout

- Provider target fields are immutable even when unused; only image config and
  explicit secret rotation are editable in place. Deletion/default retargeting
  requires a reviewed resource migration. This is conservative, not a hidden
  automatic migration or a claim of full provider permission validation.
- Observations are bounded, on demand, partial when capped, and cannot prove agent
  completion or needs-input. Status labels cached/unknown observations explicitly.
- Existing running workers require explicit owner `vmbox sessions BOX --enable`
  after an approved controller rollout; it stages runtime and binds tmux without
  restarting worker compute. Controller login alone does not provision SSH keys.
- The unfinished earlier seven-case web-terminal audit is superseded, not passed.
  Existing histories/integration APIs remain; only web interaction was removed.
- Temporary PostgreSQL container and its synthetic database were removed after
  testing. Test artifacts remain; no existing production sessions were cleaned up.

Next: review [contracts/migration/rollback](CONTROLLER-FIRST.md), approve a staged
deployment, then test the remaining real SSH, controller recovery and agent gates
using disposable sessions. No rollout should be inferred from this local report.
