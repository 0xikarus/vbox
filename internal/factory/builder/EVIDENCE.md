# Evidence — 2026-09-08, this box

Scope: only `internal/factory/builder/**`. Fetched `origin proposal/software-factory`; implementation branch `factory/feature-builder-0908` started at `3d574fa`.

## Actual installed Codex trial (not a controlled executable)

Codex CLI 0.153.0, normal saved authentication, no model override, no remote configured in the isolated temporary Git repository. Invocation went through `builder.Run`.

Task prompt:

> Approved tiny feature: fix math.js double(n) so it returns twice its numeric argument. Preserve the function name. Acceptance: requiring the module and calling double(0), double(3), double(-4), double(1.5) yields 0, 6, -8, 3. Make the code change, run a small exploratory check, and commit it. Do not add dependencies.

Baseline authored fixture source:

```js
exports.double = n => n;
```

Actual Codex-authored committed source:

```js
exports.double = n => n * 2;
```

Actual commit / observed HEAD / CandidateSHA: `cc2009da8646f469e2450bf47656d12638d51232`.
Observed branch: `factory/test`. Actual exit: 0. Signal: 0. Clean: true. Truncated: false.
Summary: `BUILD candidate: 1 files changed, 1 lines added, 1 removed (binary lines excluded). Independent verification and review required.`

The test independently required the committed working module with Node and compared numeric outputs for `[0,3,-4,1.5]` to `[0,6,-8,3]` using `node:assert/strict.deepEqual`. It did not compare agent prose or require a particular implementation sentence. The real trial passed in 57.885 seconds. The isolated checkout was removed by test cleanup; the commit identifier and source above record the actual result. This tiny acceptance inspection does not establish verification/review completion for future factory jobs.

Earlier trials: redundant `--sandbox workspace-write --approve-for-me` was rejected by the installed parser (exit 2); fixed to `--approve-for-me`. A subsequent Python fixture produced a candidate but the independent harness failed because this box lacks Python. The final fixture uses installed Node; that is the successful trial reported above. No model, auth or dangerous permissions workaround was used.

## Claude gap

Installed Claude Code 2.1.259 was invoked through the same live test with `BUILDER_LIVE_AGENT=claude`. Actual exit 1, signal 0, no candidate, unchanged clean baseline. `claude auth status` reported `loggedIn: false`, `authMethod: none`. This is an authentication-blocked failure, not a pass. Claude implementation behavior is covered by controlled invocation tests only. Claude images are unsupported.

## Local validation

Using `/data/workspace/toolchains/go/bin/go` inside this box:

- `go test ./...` — passed.
- `go build ./...` — passed.
- `go vet ./...` — passed.
- Controlled builder tests distinguish real commits from success prose; reject dirty/ignored baselines, wrong base, existing branch/result, unsafe paths and unsupported images; preserve nonzero exits/signals; reject wrong/detached/unrelated candidates; bound output; exercise cancellation of child processes, atomic no-replace persistence and external retry rejection.
- Controlled Codex image test verifies a real staged file is supplied with `--image`; this builder trial did not exercise live visual grounding.
- `go test -race ./internal/factory/builder` could not run: CGO is disabled and this box has no C compiler. Ordinary tests/build/vet passed.

No controller/shared/main/go.mod changes, other boxes, fleet work, delegated agents, PRs or deployment. Only the requested implementation branch is published by the outer task; the builder itself never publishes.
