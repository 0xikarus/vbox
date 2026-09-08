# General task runtime

`Runner` implements `taskflow.Runner` for plan, work and synthesis attempts. It
uses controller boxes, private SSH staging, saved Codex/Claude login profiles and
the existing independent `resultinbox` table. It requires no repository, commit,
clone, GitHub grant or publication API.

## Root integration API

```go
func New(Config) (*Runner, error)
func ImageCapabilities() map[string]bool // codex:true, claude:false

type Config struct {
    Controller Controller // *factory.ControllerClient implements this
    Inbox      Inbox      // *resultinbox.Inbox implements this
    Assets     factory.AssetBackend
    SSH        transport.SSH
    BinaryPath string     // trusted local Linux vmbox-task-runner binary
    CallbackURL string    // externally reachable dedicated HTTPS /result route
    Stage func(context.Context, Input) error // optional fixture override
}

func (*Runner) Start(context.Context, taskflow.Input) (taskflow.Submission, error)
func (*Runner) Observe(context.Context, taskflow.Input) (taskflow.Observation, error)
```

Build `./cmd/vmbox-task-runner` inside the box. Supply the resulting binary's
absolute path; the runtime stages it itself. Initialize/migrate the existing
`resultinbox.Inbox` with the operator's stable secret and DB, then mount its handler
on a dedicated callback route, for example:

```go
mux.Handle("/v1/factory/task-results/",
    http.StripPrefix("/v1/factory/task-results", inbox.Handler()))

runner, err := taskflowruntime.New(taskflowruntime.Config{
    Controller: controllerClient,
    Inbox: inbox,
    Assets: privateAssets,
    SSH: sshTransport,
    BinaryPath: taskRunnerBinary,
    CallbackURL: publicHTTPSBase + "/v1/factory/task-results/result",
})
// Handle err, then assign runner to taskflow.Service.Runner and
// taskflowruntime.ImageCapabilities() to taskflow.Service.Images.
```

The callback route authenticates exclusively with its account/work/attempt-scoped
inbox capability. It must be reachable over TLS without the gateway's user bearer
requirement. Do not put the controller token, gateway token or inbox signing secret
in the job. Capabilities retain the inbox's original deadline (30 minutes by
default); issuance retries do not extend it.

`Controller` uses exactly `EnsureTaskBox(ctx, account, workID, attemptID, boxID,
agent, profile)`, `SubmitTaskRunner(ctx, account, boxID, attemptID)`,
`FindTaskRunner(ctx, account, boxID, attemptID)`, `Connection` and `Process`.
The base commit already implements these on `factory.ControllerClient`.

Persist a pending submission's BoxID before calling Start again. Once BoxID is
known, Start searches for accepted work before Ensure, capability issuance or
staging. Accepted work is recovered even after grant expiry. Staging resolves the
current SSH assignment and checks it again immediately before submission.
Controller identities are `task-ATTEMPT`, `task-box:ATTEMPT`,
`task-resume:ATTEMPT`, and `task-run:ATTEMPT`. The submitted command is exactly:

```text
exec /data/workspace/.vmbox-tasks/bin/vmbox-task-runner < /data/workspace/.vmbox-tasks/attempts/ATTEMPT/job.json
```

Input.Workflow is serialized into the job as the immutable snapshot supplied by
the dispatch claim, including conversation, plans, actual prior outputs and failed
attempts. Planning requests the next revision; work selects its approved immutable
assignment; synthesis receives the actual snapshot and judges criteria. The
service owns dependency scheduling, leases, admission, approvals and explicit
retry IDs. There is no runtime cancellation API or timeout-triggered redispatch.

## Execution and evidence

The wrapper reads a bounded typed private job on stdin, takes an attempt lock,
fsyncs a create-only start marker, and launches a separate trusted child with only
task context. The child applies Linux Landlock ABI 3+ before launching the installed
CLI. Its allowlist permits system binaries, its temporary workspace and ordinary
saved-login home, but denies private job/receipt file reads and other processes'
`/proc` contents. The wrapper disables dumpability to protect its capability in
memory and stdin. No callback/controller/API-key environment is inherited by the
child. Authentication and model selection remain the installed CLI's saved config;
the adapter passes no model option. No new login is attempted.

Codex uses `exec --skip-git-repo-check`, a stage-specific output schema and
`--output-last-message`. Claude uses `-p --output-format json --json-schema` and
its dedicated `structured_output` result. Both use read-only task tools and bounded
output, with a ten-minute agent deadline. Text tasks need no source checkout.
Codex accepts validated PNG/JPEG images; Claude images are explicitly unsupported.
Capabilities describe adapters, not successful authentication or provisioning.

The final document is strictly validated as `taskflow.Result` for its stage:
plan requires the exact revision, nonempty summary and either clarification
questions or 1–20 unique assignments with criteria and an acyclic dependency graph;
work returns text only; synthesis returns text and an allowed verdict. Unknown or
duplicate keys, trailing data, null required arrays and malformed results fail.
Exit zero alone is not semantic acceptance.

The inbox envelope is `resultinbox.Result`; its document is exported `Report`:

```json
{"agentEvidence":true,"result":{"text":"Actual output"},"failure":""}
```

When `agentEvidence` is true, envelope exit/signal is the actual CLI process status.
On startup/isolation failure, the wrapper can instead deliver its trusted child's
actual terminal status with `agentEvidence:false`; Observe exposes **no agent exit
or signal** and reports a startup/evidence failure. Wrapper success never replaces
agent status. Malformed output and authentication/process failures are reported as
fixed diagnostics without leaking CLI logs.

The wrapper fsyncs the receipt before callback and stays alive until the inbox
acknowledges durable acceptance, preventing ordinary auto-hibernation during a
delivery outage. Restarting the same wrapper redelivers that receipt without agent
execution. A start marker without receipt refuses execution and requires
reconciliation. Forced termination, machine loss or an expired capability may
require operator recovery; the runtime does not fabricate a result or issue a new
grant for accepted work. An exited controller process without a callback returns
terminal `result_missing` with no agent status. A receipt cannot imply completion
while the controller process is still running.

The staging program is a source-free adaptation of existing staging safeguards:
bounded framed stdin, SHA-256 manifests, private permissions, no symlink ancestors,
create-only reservations, atomic publication and idempotent same-manifest retries.
Receipt/path/process primitives are adapted from planner/planjob/buildjob because
their unexported implementations and schemas are specific to those jobs. Those
packages are unchanged.

## In-box verification

```sh
PATH=/data/workspace/toolchains/go/bin:$PATH go test -v ./internal/taskflowruntime ./cmd/vmbox-task-runner
```

Tests compile the real wrapper and run real synthetic CLI processes through
Landlock and TLS callbacks. They cover dedicated Codex/Claude outputs, exit 23,
SIGTERM, malformed result, startup failure, denied job reads, stripped credential
environment, durable receipt before callback, delivery-only restart and refusal to
rerun an ambiguous attempt. These fixtures do not claim model execution.
Separate process fixtures verify deadline SIGKILL evidence, PNG attachment input
and explicit rejection of Claude images.

Additional fixtures execute the actual SSH staging shell and transport quoting,
reject corruption and changed manifests, exercise the real ControllerClient over
TLS, and use the real Inbox handler with SQL transaction expectations for new
work IDs and attempt scope. SQL fixtures are not a live PostgreSQL integration.

A small installed Codex smoke invocation in this box returned dedicated structured
JSON with actual exit 0 using the normal saved login and no model flag. It only
confirmed CLI/auth/output availability; it was not an end-to-end controller task or
a live Claude/image trial. Unprivileged mount namespaces were denied in this box;
the tested production boundary is Landlock. The race build could not run because
this box has CGO disabled and no C compiler installed.
