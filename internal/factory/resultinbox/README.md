# Durable planner result inbox

Independent PostgreSQL package. It does not change the factory controller,
worker launcher, root, or main. Callers must wire issuance and delivery into their
worker lifecycle; this package alone does not install callbacks in workers.

Exact exported API:

```go
func New(db *sql.DB, secret []byte, ttl time.Duration) (*Inbox, error)
func (i *Inbox) Migrate(ctx context.Context) error
func (i *Inbox) Issue(ctx context.Context, account, workID, attemptID string) (string, error)
func (i *Inbox) Accept(ctx context.Context, token string, body []byte) error
func (i *Inbox) Get(ctx context.Context, account, workID, attemptID string) ([]byte, error)
func (i *Inbox) Handler() http.Handler
func Decode(body []byte) (Result, error)
```

`Result` fields are `Version int`, `AttemptID string`, `ExitCode *int`,
`Signal int`, `Document json.RawMessage`, and `Truncated bool`, with JSON names
`version`, `attemptId`, `exitCode`, `signal`, `document`, `truncated`.

Use an application-owned `*sql.DB` (for example with pgx stdlib) connected to a
trusted PostgreSQL database/search_path. Migrate creates only
`factory_result_inbox_v1`. The caller owns database connection lifetime.
Configure a persistent, cryptographically random secret of at least 32 bytes.
New copies it. HMAC-SHA256 with a versioned domain and length-prefixed identifiers
produces a pseudorandom 256-bit capability encoded as unpadded base64url. The table
stores only SHA256(capability), identifiers, expiry, and accepted result bytes/time;
it never stores the issued plaintext capability. The package does not log inputs
or expose database error messages. Keep bearer headers out of proxy/access logs.

Issue must be called with the authenticated account and trusted work/attempt IDs.
Retrying after a caller restart recovers the same token using the same secret.
Issuance is atomic and cannot extend an existing expiry, including after secret
or TTL changes. Zero TTL defaults to 30 minutes; explicit TTL must be positive
and at most 30 minutes. Expired attempts return ErrExpired; create a new attempt
ID to retry the work. Secret changes return ErrSecretChanged for existing attempts.
Do not delete/reuse attempt rows to reissue them. Keep the secret stable for the
lifetime of attempts that need recovery.

The worker should capture the actual agent process status, build this envelope,
POST it to the configured TLS endpoint, and await a 204 before auto-hibernating:

```json
{"version":1,"attemptId":"attempt-id","exitCode":0,"signal":0,"document":{"summary":"worker report"},"truncated":false}
```

This example is a synthetic fixture, not evidence of an agent execution. Required
fields are version (exactly 1), attemptId, exitCode, and truncated. ExitCode must be
an integer in 0..255, or null with a POSIX signal number in 1..64 (e.g. `"signal":15`).
An exit code and nonzero signal cannot both be supplied. Document is optional
inert JSON; the package never opens paths or fetches URLs. The entire UTF-8 body,
including whitespace, is limited to 409600 bytes (400 KiB). Unknown/duplicate
envelope fields and trailing JSON are rejected. The attempt must exactly match
the capability's stored attempt. Exit zero is an unverified process report,
**not verified completion**. Verification/approval remains a caller concern.

Acceptance locks the capability row, validates the attempt/deadline, stores the
exact bytes, and commits before acknowledging. Concurrent different callbacks
produce one durable winner. Preserve the serialized bytes for retries: identical
accepted bytes succeed even after expiry (lost response recovery); different
bytes return ErrConflict. New submissions after expiry fail. Get returns original
bytes after expiry too; a foreign account, absent attempt, or pending result all
return ErrNotFound. Get must be called with the caller's authenticated account;
this library does not authenticate account strings.

Handler exposes only POST /result, with Authorization: Bearer <attempt-token>.
It has no controller credential or read endpoint. Statuses: 204 success/retry,
400 invalid report, 401 invalid capability, 409 conflicting result, 410 expired,
413 oversized body, 503 storage failure. Configure server timeouts and TLS in the
hosting application. Deliver the token privately to its worker, never as a URL.
A worker that cannot confirm delivery must retry identical bytes before hibernating;
this package cannot guarantee delivery if the worker hibernates before acknowledgement.
Use PostgreSQL durable commit settings (do not disable synchronous_commit).

Validation commands, run inside this box:

```sh
go test ./internal/factory/resultinbox -count=1 -v
go test -race ./internal/factory/resultinbox -count=1
go vet ./internal/factory/resultinbox
go build ./...
VMBOX_FACTORY_TEST_DATABASE_URL='postgres://.../isolated_test_db' go test ./internal/factory/resultinbox -count=1 -v
```

The PostgreSQL tests use clearly synthetic fixtures in a randomly named disposable
schema. They cover repeat migration, restart recovery, stored hashes, TTL immutability,
secret mismatch, capability/attempt/account isolation, exact retries and conflicts,
expiry, concurrent issuance, and competing callbacks. They skip explicitly when
the environment variable is unset. Never configure these fixtures against production.
