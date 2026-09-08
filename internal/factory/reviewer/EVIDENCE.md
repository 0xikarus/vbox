# In-box evidence — 2026-09-08

Prepared `/data/workspace/vmbox-service`, fetched `proposal/software-factory`, and
created `factory/independent-review-0908` from `0e0c9bd`. All repository changes
are under `internal/factory/reviewer/`. All compilation and tests ran in this box.

Toolchain: official Go 1.26.0 linux/amd64 archive, SHA-256 checked against Go's
`https://go.dev/dl/?mode=json&include=all` release metadata before extraction:
`aac1b08a0fb0c4e0a7c1555beb7b59180b05dfc5a3d62e40e9de90cd42f88235`.
CLIs: `codex-cli 0.153.0`, Claude Code `2.1.259`. No model override, auth replacement,
bypass flags or credentials were supplied to reviews.

## Actual prompt and source

Exact review policy is the `instructions` constant in run.go. The actual prompt
then includes the independently observed candidate SHA, base SHA, JSON containing
the approved feature and full independent verification report, followed by the
actual Git diff and all committed candidate contents. No builder narrative is an
input. The live fixture's feature context was:

> Compute a discounted price for an integer percentage from 0 through 100.
> Discount(100,20) must return 80; zero percent preserves price and 100 percent
> returns zero.

The actual passing check was `/usr/bin/test -f price.go`, cwd `.`, five-second
limit, executed by `verification.Run` with actual exit 0 and complete log hashes.
It deliberately proves only file existence, making substantive review necessary.

Actual entire candidate source (`price.go`, line 4 contains the defect):

```go
package price

// Discount returns the price after a percentage discount.
func Discount(price, percent int) int { return price + price*percent/100 }
```

The committed baseline used subtraction; the actual diff changed `-` to `+`.
Both commits were created in a fresh local Git repository by `TestLiveReview`.

## Actual Codex outcome

Live rerun, 08:19:29–08:19:37 UTC, actual exit **0**, signal 0; no timeout,
cancellation or truncation. SHA before and after:
`33bcaacfb96e4aa3f53d85df5f6b60b10332f616`; source clean and unchanged.
Prompt SHA-256:
`845b08aa4fcafe6c08fb9d05049bb687045c8a5b0e79ab72bf2461bf68ecf707`.

Actual structured summary:

> Rejected: the implementation increases the price instead of applying a discount.
> The independent check establishes only that price.go exists.

Actual blocking finding, inspected directly:

> price.go:4: Discount adds price*percent/100. Discount(100,20) therefore returns
> 120 instead of 80, and Discount(100,100) returns 200 instead of zero, violating
> the acceptance criteria.

`approved=false`, `Accepted=false`, Go error nil. The review correctly identifies
source arithmetic and concrete failing inputs despite a passing shallow check.
This is a real authenticated Codex response, not a shell fixture or wording match.

## Actual Claude outcome

08:17:31–08:17:32 UTC: installed CLI launched, actual exit **1**, signal 0,
`Unavailable=true`, no review, `Accepted=false`. Missing saved authentication was
reported as unavailable. Source SHA `b39e9c41a8de6c0cbb98a6cd611bd8cdac3f3a9b`
remained unchanged. A passing Go test here records the expected unavailability;
it does **not** mean Claude approved or successfully reviewed anything.

## Bounded validation and gaps

Passed in this box:

- `go test -count=1` for reviewer, planner, verification and execution.
- `CGO_ENABLED=1 go test -race -count=1` for those four packages.
- `go vet ./internal/factory/reviewer`.
- Real Codex defect review (twice); actual Claude missing-auth attempt.

Synthetic CLI tests cover positive and negative structured reviews, actual exits
7 and 0, SIGTERM, missing/malformed/oversized/truncated results, Claude envelopes,
credential environment exclusion, candidate mutation/HEAD change, private
create-only persistence, cancellation, deadline and owned-child cleanup with an
unrelated sibling surviving. Direct tests cover strict JSON, input paths,
verification mismatch, ignored repository Git hooks and source input limits.

Gaps: authenticated Claude success, large/non-text repositories, linked worktrees,
submodules, controller integration and PostgreSQL integration were not established.
Database-dependent execution tests retain their existing opt-in skip behavior.
The adapter assumes trusted saved CLI configuration and exclusive source ownership;
it does not claim containment of hostile code or same-user credential isolation.
Full live test logs remain in `/data/workspace/reviewer-{codex,claude}-live.log`;
temporary source fixtures are cleaned by Go tests. No candidate program was run,
pushed or published by a reviewer.

## Review follow-up — 2026-09-08

Follow-up to `eb6aaa9` on `factory/independent-review-0908`, confined to this
package. Fetched `origin/proposal/software-factory` at `8a8fd84` and inspected
`internal/factory/builder/git.go`: the reviewer now keeps the copied index for the
staged audit, removes it before `read-tree`, and audits raw bytes with a fresh
index. Tests cover both assume-unchanged and skip-worktree hiding modifications
before launch and during a synthetic reviewer run, while preserving source flags.

`Request.VerificationWorkspace` is separately supplied by the trusted coordinator.
Tests accept an independent verifier path absent from this box and reject missing,
relative or unclean roots, reviewer-box cwd substitution, wrong subdirectories,
absolute approved cwd and traversal. Artifact/box provenance remains the
coordinator's responsibility; the adapter checks exact approved relative cwd
resolution and candidate binding.

The source prompt now includes actual base-to-candidate diff, changed text on both
sides, and both revisions' path/mode/type/Git-object-SHA/size inventories with
explicit omission descriptions. It does not load unchanged blob contents. A
synthetic CLI prompt-capture regression uses an unchanged 2 MiB PNG-like binary
asset and 1.4 MiB text file, plus the tiny price.go arithmetic defect. It verifies
the reviewer receives the actual old/new diff, defective full changed text,
acceptance criteria, asset SHA and omission labels. This establishes prompt
transport, not a new model-quality trial.

Changed binary/non-UTF-8, added/deleted over-limit blobs and aggregate overflow
return `ErrIncomplete` before CLI launch, with no review or acceptance and a
persisted explicit failure. Existing read-only source tools remain available for
additional context. The combined inventories, changed contents and diff still
have a 512 KiB cap; each Git command output and each changed blob also have a
512 KiB cap. Path-count/output limits and the 30-second audit deadline can still
reject very large repositories. See README.md for the full current limits; the
large/non-text repository gap above describes the original live-trial revision.

Validation in this box with `/data/workspace/toolchains/go/bin` on PATH:

- `go test -count=1 ./internal/factory/reviewer` passed.
- `go vet ./internal/factory/reviewer` passed.
- `git diff --check` passed.
- Optional `CGO_ENABLED=1 go test -race -count=1 ./internal/factory/reviewer`
  could not build: C compiler `gcc` is unavailable in the current box. No new
  successful race run is claimed.

The completed authenticated live trial was not rerun. Its historical results and
prompt digest above apply to the original revision. Saved authentication, model
selection, CLI permissions, credential environment exclusion and bounded owned
process cleanup are preserved by this follow-up.
