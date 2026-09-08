# In-box evidence, 2026-09-08

Base: b279887, origin/proposal/software-factory. Implementation and tests are
limited to internal/factory/buildjob and cmd/vmbox-builder.

Passed in this box using /data/workspace/toolchains/go/bin/go:

- go test ./internal/factory/buildjob ./internal/factory/builder ./internal/factory/resultinbox ./cmd/vmbox-builder
- go vet ./internal/factory/buildjob ./cmd/vmbox-builder
- go build -o /tmp/vmbox-builder-0908 ./cmd/vmbox-builder

The buildjob suite runs builder.Run against a temporary executable fixture named
codex. This is not a live Codex invocation or authentication check. It creates
real Git commits and bundles, fetches the bundle into an independent temporary
repository, and checks exact candidate commit and file content. An unrelated
root commit/ref is absent from the imported object database. SHA256 and size
match the published bundle. A 10-byte test cap rejects export without publishing
a partial artifact; the production cap is explicitly 64 MiB.

TLS callback refusal retains a receipt; a second invocation resends identical
bytes without running the builder. Other tests cover no terminal evidence/crash
ambiguity, changed requests, redirects, unsafe receipts, malformed/oversized
jobs, actual exit-zero semantic rejection, exit 7, truncated process output,
and a cancelled real subprocess whose actual signal reaches the callback.

Race testing could not run: this box has no C compiler and cgo is disabled.
No live controller staging, production inbox database integration, artifact
transfer, or independent verification was performed by this change. Existing
resultinbox unit tests pass; this does not claim a live account-scoped delivery.
