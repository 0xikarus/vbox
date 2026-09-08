# Box-local build job

API: `Execute(context.Context, io.Reader) error`; `Job`, `Report`, `Artifact`,
and `Envelope = resultinbox.Result`. The `vmbox-builder` command consumes a
private controller-staged Job on stdin. Version 1 Job fields are:

- `version`: 1; `attemptId`: 32 lowercase hexadecimal characters.
- `request`: typed `builder.Request` (Agent, Workspace, Prompt, Branch,
  BaseSHA, ResultPath, Images).
- `receiptPath`: a distinct sibling of ResultPath in a private attempt directory,
  outside the checkout. Internal `.job.*` names and `candidate.bundle` are reserved.
- `deliveryUrl`: HTTPS inbox endpoint, without credentials, query, or fragment.
- `deliveryToken`: canonical 32-byte base64url capability from
  `resultinbox.Issue(account, workID, attemptID)`. Account/work authorization is
  enforced by that durable inbox; the worker never receives an account token.

Input is capped at 200,000 bytes with strict unknown-field, duplicate-key,
trailing-data, UTF-8 and nesting checks. The trusted controller must stage the
binary, checkout, images, private attempt directory and stdin file; stdin must
not be placed in command arguments or agent input. Launch this wrapper as the
one-shot process whose completion permits hibernation.

A directory lock serializes invocations. A create-only, fsynced start marker
binds attempt ID, builder request and delivery URL via SHA256. A create-only,
fsynced receipt precedes callback delivery. A started attempt without a receipt
requires reconciliation and never reruns. An identical retry redelivers saved
evidence without invoking the agent; changed requests/destinations are refused.
Retain the same private attempt directory and job for retries. The receipt
contains no capability. Capabilities are replaceable without changing evidence.

Document is a typed version 1 Report containing requestHash, a typed
`builder.Result` in `build`, optional fixed errorCode, and optional artifact.
The envelope preserves the actual agent ExitCode/Signal and Truncated.
Semantic rejection retains actual exit zero and reports `build_rejected`.
Summary is wrapper-controlled; raw errors and process output are never copied
into reports. No terminal process evidence means no fabricated receipt.
`artifact_export_failed` preserves observed candidate evidence but provides
no artifact and must not be treated as transfer-ready success.

A successful candidate exports a bounded Git bundle delta from `request.BaseSHA`
through fresh bare metadata with exactly `refs/heads/candidate`, derived from
the observed builder CandidateSHA/HEAD, never agent prose or paths. The bundle
has a hard **64 MiB** limit including headers and pack data and a 30-second
export deadline. Oversize/failure never publishes a partial bundle. The fixed
private `candidate.bundle` is committed create-only and fsynced before receipt.
Artifact includes that relative filename, SHA256, byte size and `baseSha`, the
explicit prerequisite also declared in the bundle header. Fresh metadata marks
only that trusted baseline as shallow and exports `BaseSHA..candidate`; history
older than the baseline is neither needed nor exported. Missing candidate
ancestry above the baseline fails export. SHA-1
repositories are supported; other object formats fail export explicitly.

`ImportBundle(ctx, workspace, input, artifact, trustedBaseSHA, candidateSHA)`
accepts a trusted independently staged repository with HEAD exactly at the
controller's baseline. It snapshots at most 64 MiB, checks size and SHA256,
requires exactly the baseline prerequisite and sole candidate ref in the v2
header, verifies the bundle with Git, and fetches `refs/heads/candidate`.
The caller supplies trusted SHAs and exclusive repository access. A missing or
different baseline is rejected; no history deepening or network fetch is needed.
Import does not perform feature verification or check out the candidate.

HTTPS redirects are refused. Delivery retries at most three times, with a
20-second request timeout and 65-second overall window independent of agent
cancellation, so terminal cancellation evidence can still arrive before exit.
CLI exit 0 means the inbox acknowledged delivery, including rejected builds.
CLI exit 1 means execution/delivery needs reconciliation. It never means
verification succeeded. The endpoint must be the trusted durable result inbox;
an arbitrary server's HTTP acknowledgement cannot prove durable storage.

Remaining integration: controller staging/launch, capability issuance, timeout
budget including cancellation delivery, inbox report reconciliation and
credential cleanup; trusted artifact transfer and wiring the importer into an
independent verifier; verification/review policy. No controller, core or planjob
changes are included. The wrapper performs no push, GitHub write or transfer.
The agent remains same-UID code: private directories prevent incidental
disclosure, not malicious same-UID tampering. The controller owns box isolation,
trusted PATH/auth CLI and credential exposure.

Tests use TLS test callbacks and executable **fixtures named codex**, not live
Codex/authentication. They exercise builder.Run, actual temporary Git commits,
real depth-1 baseline clones, a Git feature commit, bundle import and exact
commit/content match, bounded incomplete export,
callback refusal followed by byte-identical receipt redelivery without rerun,
exit-zero semantic rejection, nonzero exit, oversized output, cancellation,
strict malformed input, redirects, unsafe paths and crash ambiguity.
The shallow round trip uses a controlled runner that creates and observes real
Git commits. `builder.Run` currently rejects this shallow fixture because its
separate fresh-metadata inspection also omits the shallow boundary; correcting
that package is outside this change's authorized scope.
