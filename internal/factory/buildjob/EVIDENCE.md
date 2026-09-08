# In-box evidence, 2026-09-08

Integration follow-up to 43494aac49104896897ddd701c6a092adf2437da on
factory/build-job-0908. All changes are within internal/factory/buildjob;
cmd/vmbox-builder remains unchanged.

Passed in this box using /data/workspace/toolchains/go/bin/go:

- go test -count=1 ./internal/factory/buildjob ./internal/factory/builder ./internal/factory/resultinbox ./cmd/vmbox-builder
- go test -count=1 -v ./internal/factory/buildjob (after adding the missing-ancestry regression)
- go vet ./internal/factory/buildjob ./cmd/vmbox-builder
- go build -o /tmp/vmbox-builder-0908 ./cmd/vmbox-builder
- git diff --check

TestRealBundleAndReceiptRedelivery creates an origin with an older commit and a
baseline, then independently stages worker and verifier with real file:// Git
clones using --depth=1. Both clones have one reachable commit and lack the older
commit object. A controlled runner creates a real feature commit in the worker.
The job exports BaseSHA..candidate through fresh metadata whose sole shallow
boundary is the trusted BaseSHA, then delivers the report over a TLS callback.
The report artifact declares baseSha, SHA256 and size. After callback refusal,
receipt redelivery is byte-identical and does not rerun the feature creation.

ImportBundle verifies the exact trusted baseline HEAD, bounded bytes, SHA256,
one explicit baseline prerequisite and exclusively refs/heads/candidate, then
runs Git bundle verify and fetch in the independently staged verifier. The test
checks exact candidate SHA and content and absence of older/unrelated objects.
Wrong baseline, wrong candidate, absent baseline, different HEAD, truncation,
digest mismatch, missing/wrong prerequisites and extra advertised refs fail.
Header rejection tests recompute digest and size to exercise header validation.
A 10-byte export limit refuses publication; the production limit remains 64 MiB.
TestExportRejectsMissingCandidateAncestry deletes an intermediate commit object
and confirms failure without publishing an artifact.

TestGitRunCleansDescendants covers normal exit and timeout, including children
that close inherited pipes. Git uses the builder ownership pattern locally:
Waitid with WNOWAIT preserves the leader identity until group cleanup, and a
shared ownership gate prevents cancellation from signaling after reap.
Other existing tests cover executable fixtures, terminal exit/signal evidence,
truncation, malformed jobs, redirect refusal and crash/retry behavior.

Scope limitation discovered during testing: builder.Run's separate inspect
helper also creates fresh metadata without a shallow boundary; it rejected the
shallow feature fixture before export. That package is outside the authorized
paths and was not modified. The shallow regression therefore exercises real
Git feature creation and the actual job export/import with a controlled runner,
not the complete production builder.Run path. Its separate process tests pass.
This follow-up does not claim full production shallow-build readiness.

No live agent reruns, controller staging, production inbox, artifact transfer or
feature verification were performed. ImportBundle is available for controller
integration but is not wired into an out-of-scope verifier. No full repository
history is required by export or import. Previous evidence recorded that race
testing was unavailable because the box has no C compiler and cgo is disabled;
this follow-up did not rerun race tests.
