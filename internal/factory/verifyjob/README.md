# Box-local verification job

`vmbox-verifier` consumes a private JSON Job on stdin. Public Go API:
`Execute(context.Context, io.Reader) error`, `Child(context.Context, io.Reader,
io.Writer) error`, `Job`, `Report`, and `Envelope = resultinbox.Result`.
Execute reexecutes its own binary with `--child`; embedders must implement that
mode as the supplied command does. Linux process groups, waitid and renameat2
are required.

Version 1 Job fields:

- `version`: 1; `attemptId`: 32 lowercase hexadecimal characters.
- `request`: `verification.Request`, with `Workspace`, `ExpectedSHA`,
  `OutputDir`, `Checks`. Checks use `argv`, `cwd`, `timeoutSeconds`.
- `receiptPath`: absolute clean filename inside OutputDir. `.job.*` is reserved.
- `deliveryUrl`: HTTPS resultinbox endpoint, without user info, query or fragment.
- `deliveryToken`: canonical unpadded base64url 32-byte resultinbox capability.

The trusted caller stages the binary, an exact clean candidate checkout, a
private OutputDir (0700), and private job stdin. Workspace and OutputDir must
be separate directories with no symlink components or traversal. Checks must
stay in Workspace; 1–64 checks, each with a 1–3600 second timeout, are accepted.
Input is bounded to 200,000 bytes, with strict fields, duplicate/case-alias keys,
UTF-8, nesting, and trailing-data validation. Candidate SHA must be a full
lowercase SHA-1 or SHA-256 object ID. `verification.Run` audits the exact clean
candidate before and after checks. This wrapper does not transfer source.

The parent locks the attempt directory, creates/fsyncs `.job.started` before
launch, and persists a create-only, fsynced receipt before callback. The marker
and receipt bind attempt ID, request and delivery URL. A changed request is
refused. Identical redelivery reads saved evidence without launching a child
or requiring the checkout still to exist. A start marker without a receipt,
including a launch failure or ambiguous crash, requires reconciliation and
never permits rerun. Keep the attempt directory and original Job for retries.
The capability is not persisted in the receipt and can be replaced for retry.

The parent launches the actual verifier child, with only the typed check
request on its private stdin. The child receives a fresh private HOME and an
explicit environment allowlist; no callback capability, job, provider or
controller environment credentials are forwarded. The child calls
`verification.Run`. Parent stdout/stderr never contain check output or raw
errors. Child stdout is a bounded JSON report pipe (180,000 bytes), and
stderr is discarded. Raw diagnostics and check logs remain in private local
verification artifacts. Report errors are replaced with fixed codes before
the report leaves the child.

The receipt/callback preserves three separate meanings:

- Envelope `exitCode`/`signal`: actual OS terminal status observed by the parent
  for the verifier child. Missing terminal evidence never creates a receipt.
- Document `verification`: check metadata, individual actual exits/signals,
  cancellation/timeouts, source audit, and private log filenames, SHA256,
  sizes and truncation. A normal failed check (e.g. exit 7) can have child exit
  0 because report production succeeded. A runner error has child exit 1.
  Abrupt child death can produce signal evidence with no report.
- Document `accepted`: local check acceptance only, requiring a normal child
  exit 0 and `verification.AllPassed`. It is not controller/policy approval.
  `errorCode=verification_incomplete` identifies missing/incomplete execution
  evidence. Envelope `truncated` describes the report pipe; individual log
  truncation remains on each check's stdout/stderr metadata.

Parent cancellation sends SIGTERM to the owned child group, allowing the child
context to cancel, kill/reap its check group, audit, and finish its manifest.
After 40 seconds the parent force-kills an unresponsive child. Group ownership
is retained until waitid observes termination and cleanup finishes, before
reaping the child. Cancellation evidence still gets an independent bounded
65-second callback window. HTTPS POST uses a 20-second request timeout, at most
three attempts, and 1/2-second retry delays. Redirects are refused; 4xx other
than 429 are terminal. HTTP response bodies are not loaded. CLI exit 0 means
callback acknowledged, including failed checks or child death; CLI exit 1
means delivery/execution needs reconciliation. Only the trusted durable inbox
can establish durable remote receipt.

Private verification directories retain fsynced manifests and capped 1 MiB
stdout/stderr files for each executed check, with digests and byte counts.
No log bytes are included in callbacks, and artifacts are not deleted.

Remaining transport/integration gaps: authenticated source staging and exact
candidate checkout; authenticated log/manifest retrieval with digest checks;
controller launch and capability issuance/rotation; inbox report reconciliation
and independent verification policy; retention and credential cleanup; a task
lifetime budget that includes checks, cancellation grace and delivery. No
source/artifact upload, provider integration, merge, deployment or controller
changes are included. Same-UID code is not a security sandbox: trusted check
selection and box isolation are required. An externally killed parent/child
can leave separately grouped checks running; box-level process containment
and reconciliation must resolve that ambiguity before cleanup or reuse.

Validation runs entirely inside the box. Tests compile the actual command,
create real Git candidates, execute real shell checks and use local TLS
callbacks. Coverage includes exit 0/7, cancellation, child SIGKILL, exact SHA
mismatch, private logs/truncation, credential environment isolation, callback
refusal/retry bounds, identical redelivery (also after checkout deletion),
changed requests, malformed/oversized JSON, paths/symlinks/permissions, and an
ambiguous durable start marker. Build and vet commands:

```
go test ./internal/factory/verifyjob ./internal/factory/verification ./internal/factory/resultinbox ./cmd/vmbox-verifier
go vet ./internal/factory/verifyjob ./cmd/vmbox-verifier
go build -o /tmp/vmbox-verifier-0908 ./cmd/vmbox-verifier
```

These passed on 2026-09-08 using `/data/workspace/toolchains/go/bin/go`.
The attempted race run could not start: this box's Go environment has CGO
disabled (`go: -race requires cgo`). No source-transfer worker task was rerun.
