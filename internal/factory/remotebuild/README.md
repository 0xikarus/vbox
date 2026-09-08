# Trusted remote build adapter

`Runner.Start(ctx, Input)` returns `factory.Submission`; `Runner.Observe(ctx,
Input)` returns `remotebuild.Observation`. `Input` contains the persisted account,
approved work, current execution node (including its reserved build stage
attempt), and an explicit trusted `PreparedSourceSHA`. The runner is bound to one
operator-configured `AccountID`. This is an internal trusted API, not an HTTP
request model. The coordinator must load these records under its account-scoped
execution lease and reject stale snapshot versions when persisting results. The
adapter cannot establish database freshness from a supplied value alone.

The node must match `execution.New(work)` and the attempt ID produced by
`execution.Store.Reserve`. Only independent features are currently supported;
any feature with dependencies returns `ErrNotReady` before controller calls.
There is no dependency preparation/proof API in this adapter, so a supplied SHA
alone never proves dependency inclusion. An independent feature's prepared SHA
must equal its approved baseline. Repository resolution rechecks GitHub App
authorization for that exact SHA and obtains only a read token.

Use the existing `factory.ControllerClient`, GitHub App repository backend,
`resultinbox.Inbox`, private asset backend, and
`BuilderStage(staging.Stager{SSH: ssh, BinaryPath: builderBinary})`. The helper
forces `Role: "builder"`. Provisioning passes the saved agent/profile to
`EnsureBuilderBox` and requires the separate deterministic name
`factory-build-<stage-attempt-id>`.

Persist every pending box identity before calling Start again. The returned
state is the exact controller lifecycle state, including attaching, detached,
and hibernated. Once a box is bound, Start searches for the accepted fixed
wrapper task before provisioning, staging, or issuing capabilities. A bound task
that cannot be found requires reconciliation; it is never replaced. Retry the
same persisted attempt after uncertain submission. The controller idempotency
key and buildjob's durable start marker are additional duplicate-work barriers.

Private paths are fixed under
`/data/workspace/.vmbox-factory/attempts/<attempt-id>/`: `job.json`, `repo`,
`images/<asset-id>`, `build.json`, `receipt.json`, and `candidate.bundle`.
Images are read as bounded bytes and checked against saved manifests. The prompt
contains the approved feature, criteria, checks, and identity/asset bindings.
Credentials enter private staging/job delivery only. Task arguments contain only
the fixed `vmbox-builder` wrapper invocation; menus and process output are never
interpreted as results. The builder uses its existing one-shot agent adapter and
saved authentication profile.

Observe validates the process identity, scoped inbox envelope, buildjob request
hash, agent process evidence, and candidate/bundle bindings. Request hashing
matches buildjob's protocol, including the callback URL; retain that URL for the
attempt lifetime. `WrapperExitCode`/`WrapperSignal` are independent of
`Process.ExitCode`/`Process.Signal`. Unknown agent exits cannot finish; a missing
receipt returns `result_missing`. Controller/SSH errors never manufacture a
terminal result. `Finished` means a terminal build report (including a rejected
build), not a successful feature or independent verification.

`Report.Artifact` and `ArtifactPath` describe a reported bundle still on the
builder box. No bytes have been transferred or verified. A separate
authenticated transfer must retrieve the fixed file from the correctly scoped
box, enforce size/hash limits, inspect Git objects and ancestry, and pass the
candidate into independent verification/review. Do not mark a feature verified
from this observation.

## Remaining wiring

- Wire the execution worker to this API with account-scoped leases and CAS writes.
- Add persistence for a pending box-only binding: current `execution.Store.Bind`
  requires a task ID and cannot record this first provisioning response.
- Wire the private builder binary/stager and scoped inbox callback/capability
  lifetime. Expired capabilities require reconciliation; do not silently launch
  another attempt. Accepted tasks remain recoverable even after expiry.
- Implement trusted dependency preparation and proof before enabling dependent
  features, plus authenticated bundle transfer and independent acceptance.

No production wiring, configuration, publication calls, issues, or PRs are
changed by this package.

## Validation

`go test ./internal/factory/remotebuild` uses controller, repository, inbox, and
staging fixtures plus actual private attachment files. It covers pending box
persistence, exact source/profile/job paths, staging retry, lost submission and
hibernation recovery, stale/foreign identities, source/assignment changes,
dependency rejection, prompt/image/report/artifact bounds, corrupt attachments,
menu output, missing receipts, unknown exits, signals, separate wrapper exits,
and strict nested JSON. These fixtures do not claim a live remote build or
bundle transfer.
