# In-box verification

Exact exported entry point:

```go
func Run(ctx context.Context, req Request) (Report, error)

type Request struct {
    Workspace, ExpectedSHA, OutputDir string
    Checks []factory.Check
}
```

`Workspace` must be a canonical standalone Git checkout with a real `.git`
directory. `ExpectedSHA` is a full lowercase commit ID. `OutputDir` must already
exist outside the checkout. Traversal and symlink directory paths are rejected.
Each call creates a unique mode-0700 attempt directory containing mode-0600 logs
and an atomically renamed, synced `manifest.json`. Input validation can fail
before an attempt exists.

`Report` exports `SourceSHA`, `FinalSourceSHA`, `ExpectedSHA`, `SourceClean`,
`EvidenceDir`, `Checks []CheckReport`, `AllPassed`, and `Error`.
`CheckReport` exports `Argv`, canonical `Cwd`, `TimeoutSeconds`, nullable
`StartedAt`/`FinishedAt`, `Executed`, nullable `ExitCode`, `Signal`, `Timeout`,
`Cancel`, `Stdout`, `Stderr`, and `Error`. JSON uses `sourceSHA`, `finalSourceSHA`,
`expectedSHA`, `start`, `finish`, `exitCode`, and otherwise lower camel case.
`Log` exports `File` (relative filename), `SHA256` (digest of retained bytes),
`Bytes` (retained byte count), and `Truncated`.

Checks run sequentially with direct argv, no implicit shell. The child receives
an allowlisted environment, a controlled executable search path (the building Go
toolchain's bin, `/usr/local/bin`, `/usr/bin`, `/bin`), and no inherited publishing
credentials or user configuration. Each check gets a fresh writable private HOME
and XDG cache beneath its evidence directory, so build tools can create caches
without reading the worker's credential-bearing home. An explicitly approved shell executable is
still an executable; callers own approval of its argv. Deadline is the earlier
of context cancellation/deadline and the approved 1–3600 second timeout.

Each process has its own group. Cancellation kills only that group. Linux
`waitid(WNOWAIT)` retains the leader PID until group cleanup finishes, preventing
a cleanup kill from targeting a recycled group ID. Exit status comes from the
actual process state; signaled and unexecuted processes have null exit codes.
Both streams drain continuously while retaining at most `LogLimit` (1 MiB) each.
Truncation does not replace or obscure the real exit status. Every retained log
is checked against its digest again before finalizing the manifest.

Git inspection uses `/usr/bin/git` with a fresh private Git directory, controlled
configuration and empty templates. It copies refs/index, reads source objects,
and never loads checkout-local configuration, hooks, replacement refs, or
filters. It checks staged differences, then uses a fresh index and overriding
attributes to check raw worktree bytes and untracked/ignored files. HEAD and
cleanliness are inspected before and after every executed check. A source
failure stops remaining checks; their execution and exit fields remain empty.
Post-check inspection runs even after caller cancellation, with a 30-second
bound. Ordinary nonzero exits/signals/truncation produce a report with
`AllPassed=false`; invalid requests, source failures, cancellation/timeouts,
start failures, or evidence failures also return an error. Only complete,
untruncated evidence for every check exiting zero against the expected clean
source can set `AllPassed=true`.

## Validation and limits

Real tests create isolated Git repositories and spawn executable processes
inside the current box. Coverage includes success, exit 7, SIGTERM, deadline,
explicit cancellation, owned descendant cleanup and sibling survival, exact
argv (including shell metacharacters), tracked/untracked/staged modifications,
changed valid HEAD, unsafe paths, oversized stdout/stderr with both zero and
nonzero exit, deleted evidence, unique attempts, digests, and malicious Git
configuration/inherited environment. No agent-generated results stand in for
execution.

Validated on 2026-09-08 using `/data/workspace/toolchains/go/bin/go`: repository
`go test ./...`, `go vet ./...`, and `go build ./...` passed. Race instrumentation
could not run: cgo is disabled and no C compiler is installed in this box.

This Linux runner is not a hostile-code sandbox. Same-user concurrent filesystem
mutation, modify-and-restore attacks between inspections, and descendants that
escape their process group need isolation beyond this API. Check programs can
read files accessible to their OS user; clearing the environment is not an OS
credential boundary. Callers must supply an exclusively owned checkout and
trusted output parent. Linked worktrees, submodules, split indexes, and SHA-256
repositories are not supported and fail verification. Logs/manifests are local
evidence, not signed attestations. There are no controller, publishing, deployment,
fleet, or box-provisioning changes.
