# Approved plan publication

`publishing` consumes only durable `approved_queued` and `publishing_issues`
`factory_work_items` for one explicitly bound operator account. It reuses
`factory.Work` and all three `githubapp.Client` publication primitives.

```go
c := publishing.New(db, githubClient, publishing.Policy{
    AccountID: operatorAccountID, // trusted configuration, never request data
    IssuesWrite: true,            // explicit operator opt-in
})
// Run factory.Store.Migrate first, then:
err := c.Migrate(ctx)
// Call repeatedly from the service's worker loop:
err = c.Step(ctx)
```

API:

- `New(*sql.DB, *githubapp.Client, Policy) *Coordinator`.
- `(*Coordinator).Migrate(context.Context) error` creates only
  `factory_publication_jobs` and `factory_publication_operations`.
- `(*Coordinator).Step(context.Context) error` performs at most one external
  publication operation. `sql.ErrNoRows` means idle, leased, delayed, or blocked
  work; `nil` means the step committed successfully, not necessarily that the
  whole plan is published. `githubapp.ErrPublicationUncertain` means durable
  unresolved outcome, `ErrBlocked` means a visible fixed failure, `ErrFence`
  means a source/approval/account fence changed during the call, and
  `factory.ErrConflict` means the durable operation lease was lost. DB/context
  failures propagate and retain the attempted operation for recovery.
- `Coordinator{DB, Client: Publisher, Policy}` supports realistic fixtures;
  `*githubapp.Client` implements `Publisher`. Configure the coordinator once
  before concurrent use. No runtime loop or shared configuration is added here.

The client independently checks its account installation allowlist, repository
visibility and narrowed `issues:write` token. Approval alone grants no write
permission. Operation authority comes from the operator policy and the selected
account's durable work row, not from arbitrary request fields.

## Durable protocol

The first claim locks an eligible work row and saves its immutable snapshot and
source/approval fence. The fence covers work ID, work revision, repository ID and
name, source SHA, approved revision and complete plan history. Progress never
changes the approved plan or increments its input/approval revision. A changed
fence blocks publication, including edits between steps. Late results for changed
work retain operation outcome evidence without overwriting its new document.

Master issue, feature issues in stable topological order, then a master comment
linking every feature issue use separate durable operations. Keys are SHA-256 of
work ID, approved plan revision, kind and feature ID, prefixed with the protocol
version. Each invocation's exact snapshot, authority, dependency issue numbers
and comment body is saved in `intent` before the first external call. IDs, issue
numbers, attempt count, timestamps, safe error and outcome state are saved after.
No database transaction spans a GitHub call.

Both job and operation acquire a durable two-minute lease in the same
transaction as intent. Work row locking with `SKIP LOCKED` serializes claims;
independent operation lease checks prevent bypassing an active operation even
if its job lease expires. Both tokens and expirations fence outcome commits.
After expiration a new owner may overlap **reconciliation reads** with a delayed
old request, but it cannot make another initial publication POST. Correctness
does not depend on lease timing or marker uniqueness alone.

`pending -> attempted` commits before network. Any resumed `attempted` or
`unknown` operation always uses `PublicationOperation.ReconcileOnly`, with the
original intent. An absent marker never resets an operation to `pending`.
Unknown outcomes remain `unknown`, with `work.error` and a 30-second read-only
reconciliation delay. This includes a crash after intent but before the network
call: safety can require manual investigation even when no issue was created.
There is deliberately no automatic abandon/reset/re-POST or fake completion.

Known invalid payloads, policy denial, conflicting evidence, and definite HTTP
400/401/403/404/422 failures block automatic processing with a safe explanation.
An error already marked uncertain by the client remains unknown, even with an
HTTP error attached; the upstream status is visible without response bodies or
credentials. A fixed failure during reconciliation blocks the job but preserves
the earlier operation's explicitly `unknown` outcome. Operators can inspect the
durable tables. There is no operator
resolution API in this package; never delete an unknown operation or reset it to
pending merely because a marker is absent. Fixing operator policy does not
silently clear a blocked job.

## Progress and integration boundaries

Progress patches only `state`, `updatedAt`, `error`, `masterIssueUrl` and
`features` in the JSON work document, preserving unrelated top-level fields.
Approved feature definitions supply progress; states are `pending` and
`issue_published`, never `implemented`, and no PR or box is invented.
`build_queued` requires successful records for the master, every feature, **and**
the final linking comment. Publication does not prove implementation or checks.

Root integration still needs the `factory.Work.MasterIssueURL` JSON field
(`masterIssueUrl`) and worker startup/migration/polling wiring. JSON patching
already persists that URL without editing root types in this branch. URLs use
the durable repository name on github.com; alternate enterprise web hosts are
not configured here. There were no live GitHub issue/comment writes.

## Local evidence

Tests use a fresh schema per test in `VMBOX_FACTORY_TEST_DATABASE_URL`; they
skip PostgreSQL cases when it is absent. They create only factory tables inside
those schemas and clean them up. This task used local PostgreSQL 15, with no
controller schema or other box involved.

```sh
export VMBOX_FACTORY_TEST_DATABASE_URL='postgres:///factory_publication_0908?host=/var/run/postgresql&user=vmbox'
CGO_ENABLED=1 /data/go/bin/go test -race ./internal/factory/publishing ./internal/factory/githubapp ./internal/factory
/data/go/bin/go vet ./internal/factory/publishing
/data/go/bin/go build ./internal/factory/publishing
```

Coverage includes committed intent before network, unlocked DB rows during
network, DAG ordering and dependency references, immutable retry payloads,
progress without fake implementation, account/state/policy filtering,
concurrent durable leases and late owners, source/approval/account changes,
crash before a call, lost outcome persistence, hidden markers with no duplicate
POST, fixed failures and scrubbed errors. A loopback HTTP fixture also exercises
the **real** GitHub client against PostgreSQL: a committed master comment's
response is lost, its marker stays invisible across retries, and publication
completes only after later reconciliation finds it.
