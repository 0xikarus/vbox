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
