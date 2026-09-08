# Box-local evidence — 2026-09-08

Base: c846166. Branch: factory/planner-runtime-0908.
All code, tests and evidence changes are under internal/factory/planner/.

Installed CLI help inspected: codex-cli 0.153.0; Claude Code 2.1.259.
Go was absent from PATH; installed Go 1.26.8 under /tmp/planner-toolchain from
https://go.dev/dl/, verified archive SHA256 against the official release listing.
No dependency manifests changed. No credentials read or printed.

Passing final local commands:

```
/tmp/planner-toolchain/go/bin/go test ./internal/factory/... -count=1
/tmp/planner-toolchain/go/bin/go vet ./internal/factory/planner
/tmp/planner-toolchain/go/bin/go build ./internal/factory/planner
```

Factory/store external integration tests retain their own opt-in requirements;
this is not evidence that a database integration environment was exercised.
Controlled executable tests cover argv/prompt transport, successful byte-exact
persistence, actual nonzero exit and signal handling, group cancellation, invalid
and oversized documents/envelopes, unsafe paths, unsupported inputs, restrictive
schema fields, and atomic no-replace persistence. These are fixtures, not agents.

Actual installed CLI run (separate opt-in tests):

```
PLANNER_LIVE_CODEX=1 PLANNER_LIVE_CLAUDE=1 \
 /tmp/planner-toolchain/go/bin/go test ./internal/factory/planner -run TestLive -v -count=1
```

Codex image grounding: PASS, 11.94 seconds, exit 0, signal 0, 981-byte valid
agent-produced factory.PlannerResult document, no truncation. Test generated
600x300 PNG pixels: red square left, blue circle right on white. Prompt asked for
visual description without supplying these facts, and used attachment.png.
Actual agent response:

> The image shows a solid red square on the left and a solid blue circle on the right, against a white background. Their centers are horizontally aligned, with a wide gap between them. Neither shape has a visible outline.

This run used normal configured authentication/model and the adapter's read-only
flags. It did not parse tmux, substitute a fixture, or bypass permissions.

Claude text: FAIL, exit 1, signal 0. A separate invocation with the same permission
and structured-output flags classified bounded CLI diagnostics in memory:
authenticationFailure=true, unsupportedFlag=false. Diagnostics were not printed.
Claude structured-output success remains unproven in this box; Claude image input
is explicitly rejected. The combined live command therefore correctly exited 1.

Race detector: unavailable, `-race requires cgo`; no C compiler installed. Attempt
to install gcc/libc6-dev via apt failed with permission denied before installation.
This gap is not reported as a pass. Ordinary tests, vet and build passed.

Remaining integration work is outside ownership: wrap synchronous Run with the
core Start/Observe lifecycle, persist attempt identity/idempotency, select/provision
saved profiles, authorize/stage assets, and map trusted completion evidence.
ResultPath is deliberately create-only per attempt; integrator should resolve any
requirement to overwrite/reuse a path before wiring this adapter. CLI configuration
and same-UID processes remain trusted; box containment and disk quotas are external.
