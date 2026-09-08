# Scoped GitHub App repository client

`New(Config)` copies a trusted controller-account → installation-ID allowlist.
Repository IDs are canonical positive decimal GitHub REST IDs. Unknown accounts
fail closed. An installation may be shared only by explicitly listing it for each
controller account. Changing the allowlist requires constructing a new client.

Every operation obtains fresh visibility with metadata-only installation tokens
and follows bounded, same-endpoint pagination. No visibility or tokens are cached.
`Resolve` and `RepositoryToken` then request exactly one `repository_ids` entry,
with `metadata:read` and `contents:read`; only `RepositoryToken(..., true)` requests
`contents:write`. They recheck the restricted token's repository list before use
or return. Revoked installations, removed repositories, insufficient grants and
upstream failures fail closed. GitHub remains authoritative for concurrent
revocation after the last check, including use of a token already returned.

`Resolve` supports a branch, tag or commit SHA; an empty ref selects the current
default branch. Invalid ref syntax and non-40-hex commit responses are rejected.
No workflow, pull-request or administration write permissions are requested.
Installation tokens expire as reported by GitHub; missing tokens or less than a
minute of remaining validity are rejected. Callers must honor the returned expiry
and protect returned credentials. No automatic retries are made: `APIError`
exposes HTTP status codes for caller-owned bounded backoff (including 429/5xx).
Errors omit upstream bodies and transport error strings to avoid secret leakage.

The HTTP client is copied, its cookie jar disabled, and all redirects refused,
including same-host redirects. Pagination URLs are validated but never followed
directly; only the page number is used to construct the next request. Credentials
are only sent in Authorization headers. Custom transports are trusted configuration.
BaseURL defaults to HTTPS api.github.com; HTTPS enterprise API roots are supported
with a compatible REST version. HTTP is allowed only for literal loopback test hosts.

## Official REST references

Verified 2026-09-08:

- [JWT authentication](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app): RS256, Bearer auth, issuer App ID, backdated iat, expiry within ten minutes.
- [Create installation access token](https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app): POST /app/installations/{installation_id}/access_tokens, repository_ids and explicit permissions. Omitting repository_ids is used only for internal metadata discovery.
- [Installation repositories](https://docs.github.com/en/rest/apps/installations#list-repositories-accessible-to-the-app-installation): GET /installation/repositories using installation auth, per_page up to 100.
- [Pagination](https://docs.github.com/en/rest/using-the-rest-api/using-pagination-in-the-rest-api): Link rel="next" determines continuation.
- [Get commit](https://docs.github.com/en/rest/commits/commits#get-a-commit): GET /repos/{owner}/{repo}/commits/{ref} with contents read access.

Requests use Accept: application/vnd.github+json and the documented
X-GitHub-Api-Version: 2026-03-10.

## Validation limits

HTTP tests use local fixtures and generated RSA keys. They establish client
behavior and cryptographic signing, not live GitHub App acceptance. Live proof
still requires a registered App ID/private key, explicit test account installation
allowlists, granted repositories (including a private repository), contents read
and optional write grants, and controlled ref/repository/installation revocation.
No live credentials were supplied or exercised. This package does not wire the
client into service startup or change deployment configuration.

Validation in the implementation box with `/data/go/bin/go` (Go 1.26.8 linux/amd64):

- `go build ./...`: passed.
- `go vet ./...`: passed; package vet also passed after the final test addition.
- `go test ./internal/factory/githubapp -count=1 -cover`: passed, 90.1% statement coverage.
- `go test ./...`: failed in cmd/vmbox-controller and internal/controller due to
  conflicting ServeMux patterns `GET /` and `/v1/factory/`, and in internal/boxruntime
  at TestIdleHibernateRealTmux. All three failures were reproduced with
  `go test ./cmd/vmbox-controller ./internal/controller ./internal/boxruntime`
  in an untouched `git archive 104d38c` export in the same box.

## Issue publication primitives

`PublishMasterIssue`, `PublishFeatureIssue`, and `PublishIssueComment` require a
trusted `WriteAuthority{AccountID, RepositoryID, IssuesWrite: true}`. This is an
explicit controller authorization assertion, not an HTTP request DTO or evidence
of plan approval. Each invocation rediscovers account visibility, requests a fresh
token with exactly one repository ID and `metadata:read` plus `issues:write`,
checks the returned issues permission and expiry, and rechecks single-repository
visibility. Existing `RepositoryToken` contents tokens are not used for publication.

Master and feature methods accept a trusted durable `factory.Work`: the latest
plan must match `ApprovedPlanRevision`, the work's immutable base SHA, and an
input revision before the work revision, and pass `Plan.ValidateApproval`.
Approval alone never authorizes writes. The master includes the approved plan
specification. Features include acceptance criteria, exact checks, files, base
SHA, a master issue number, and references for every declared dependency.
The caller supplies master/dependency numbers from successful publications in
this same repository and publishes dependencies first. These are ordinary issue
body references, not GitHub sub-issue or dependency API mutations. Comment writes
check that the numeric target is an issue rather than a pull request.

Every method takes `PublicationOperation{Key, ReconcileOnly}`. Keys are 1–200
bytes, nonblank, and unique across all three kinds within a repository. Bodies
are limited to 60,000 bytes before the marker; titles to 256 bytes. Reserved
marker text in caller content is rejected. The machine marker contains SHA-256
hashes of the operation key and the deterministic payload (including kind and
comment target), never the raw key. Operation keys must not contain secrets;
hashing is not encryption. Returned `Publication` contains only an ID and issue
number (zero for comments), not untrusted upstream URLs or credentials.

Before any creation, both repository-wide issue and comment collections are
scanned, including closed issues. A unique matching marker, body, title, kind,
and target recovers the existing result. Changed payloads, edited bodies/titles,
moved targets, matching markers on PRs, or multiple matches return
`ErrPublicationConflict`. Each collection is limited to 100 pages of 100 results;
incomplete, malformed, foreign-endpoint, or nonsequential pagination fails
without a write. Link URLs are never followed directly. Redirects are refused
by the existing HTTP client. These scans use REST listings, not search indexing.

After a failed or malformed POST response, the client attempts one complete
reconciliation scan and never repeats that POST. A recovered match is success;
an unresolved result includes `ErrPublicationUncertain` (and a redacted
`APIError` status when available). Reconciliation respects the caller's context;
cancellation can leave the outcome unknown. `ReconcileOnly: true` scans without
creating anything and reports uncertainty if no match is found.

The caller MUST serialize publication for each repository across processes and
accounts sharing it. Markers are not concurrency locks or exactly-once storage.
Before the first attempt, durably record the key, immutable payload, and an
attempt-in-progress state; persist the returned result. After a crash or unknown
outcome, use only `ReconcileOnly: true` until the original result is found or an
operator resolves the ambiguity. Never infer permission for another POST from
an absent marker: visibility delay, deletion, marker removal, or concurrent edits
can hide an earlier write. Keep a durable key/payload/result ledger to reject
reuse even after remote marker deletion. The package does not implement that
caller ledger, publication orchestration, approval storage, or worker scheduling.
Matching markers are not proof of authorship and may be forged by repository
writers. No live issue writes have been performed.

Additional primary references checked 2026-09-08:

- [Create issue](https://docs.github.com/en/rest/issues/issues#create-an-issue): installation authentication requires Issues write permission.
- [Create issue comment](https://docs.github.com/en/rest/issues/comments#create-an-issue-comment): Issues write is sufficient; Pull requests write is unnecessary here.
- [Repository issues](https://docs.github.com/en/rest/issues/issues#list-repository-issues): request all states; identify PRs by `pull_request`.
- [Repository issue comments](https://docs.github.com/en/rest/issues/comments#list-issue-comments-for-a-repository): repository-wide listing supports created/ascending pagination and returns `issue_url`.

Publication validation on the 1d28045-based feature branch, inside this vmbox
with Go 1.26.8:

- `go build ./...`, `go test ./...`, and `go vet ./...`: passed. The historical
  failures documented above for the previous implementation were not observed.
- Package fixtures cover all three primitives, dropped connections after commit,
  truncated JSON and 500 responses after commit, uncertain absent results,
  conflicts, approval/dependency validation, permissions, revocation, extra
  repository scope, pagination recovery/exhaustion, and blocked redirects.
- `go test -race ./internal/factory/githubapp -count=1` could not run: this box has
  CGO disabled and neither gcc nor clang installed. No toolchain was installed.
- Live GitHub App acceptance, caller serialization/ledger integration, and real
  publication remain unverified and require separate authorization/integration.
