# Factory implementation contract v1 (feature branch only)

This document freezes the first integration seam for parallel workers. All routes
are proposed additions and are disabled unless the operator enables the factory.
Controller browser cookies authenticate UI requests; server derives account/user.
Do not accept account identity in JSON or expose backend tokens in JavaScript.

## Browser API

Prefix `/v1/factory`. Errors use `{error: string}` with appropriate HTTP status.
All mutating calls require `Idempotency-Key`, except revision-protected updates.
Dates are RFC3339 UTC; IDs are opaque strings. Every read/write is account scoped.

- `GET /capabilities`: `{enabled, githubConfigured, agents: [{name, images}]}`.
- `GET /repositories`: `[{id, fullName, defaultBranch, installationId}]`; only
  authorized App repositories. Empty state offers connection instructions.
- `GET /profiles`: `[{application,name}]`, selected permitted saved agent profiles.
- `POST /assets`: multipart field `file`; returns `{id,name,mediaType,size,sha256}`.
- `GET /assets/{id}`: authenticated private binary preview/download.
- `GET /work-items`: work summaries newest first, bounded pagination via cursor.
- `POST /work-items`: `{repositoryId,baseRef,idea,agent,profile,assetIds}` -> work
  item with `id`, `revision:1`, `state:"planning_queued"`. Immutable input snapshot.
- `GET /work-items/{id}`: complete bounded record described below.
- `POST /work-items/{id}/messages`: `{text,assetIds,expectedRevision}` -> work item.
  Append user turn, queue next planning attempt; reject competing/running revisions
  with 409 rather than silently overwrite or create simultaneous planners.
- `POST /work-items/{id}/approve`: `{expectedRevision,planRevision,maxWorkers}` ->
  work item. Require a finished plan with valid task DAG/checks and matching input
  revision. Actual GitHub issue creation/build scheduling follows durable outbox.
  Max workers is policy-bounded; approval never grants automatic main merge.

Work item: `{id,revision,state,repositoryId,repositoryName,baseRef,baseSha,idea,
agent,profile,assets,createdAt,updatedAt,boxId,boxName,messages,plans,features,error}`.
Messages: `{id,role:"user"|"assistant"|"system",text,assetIds,createdAt,attemptId}`.
Plans: `{revision,markdown,questions,features,createdAt,attemptId,baseSha}`.
Features: `{id,title,description,acceptanceCriteria,dependsOn,files,checks,state,
issueUrl,prUrl,boxId}`. Checks: `{argv: string[],cwd: string,timeoutSeconds}`.
Missing optional values are omitted; collections are arrays, not null.

UI polls only while active and offers explicit refresh/reconnect after failure.
Persisted messages are authoritative, never infer text from terminal snapshots.
Escape agent text; use native inputs/buttons and avoid injecting Markdown HTML.
Plan and Approve are distinct actions. Views display planning progress, responses,
questions, revisions, dependencies and later build/review evidence in the same UI.

## Private asset package seam

`internal/factory/assets` standalone package, no controller imports or routes.
API: `New(root string) (*Store,error)`;
`Put(ctx context.Context, accountID, name string, r io.Reader) (Asset,error)`;
`Open(ctx context.Context, accountID,id string) (*os.File,Asset,error)`.
Asset JSON fields: id,name,mediaType,size,sha256; size is int64.
Reject invalid IDs/path traversal/symlinks, unknown account/asset, corrupt or
oversized decoded images. PNG/JPEG/WebP only; 10MiB/25MP per image. Store privately,
atomically and durably with metadata. Binaries never in public static assets.
The caller enforces max8 images/40MiB per submission and reference/retention policy.
Avoid secrets in errors. Tests use real image bytes, corrupt and adversarial input.
Supported image list must reflect actual decode support, never MIME alone.

Stage to verified local paths; grant broker and account authorization stay outside
this filesystem package. No URL fetcher. No agent-specific image invocation in
the storage package; those adapters are separate and require live capability proof.

## Ownership during bootstrap

- UI worker: `internal/controller/web/factory.js`, `factory.css` only, plus scoped
  UI test source. Export `mountFactory(root, request)` accepting an authenticated
  request helper `(path,method='GET',body,headers={})`; handle multipart separately
  with cookie fetch, never persistent browser secrets. Do not edit shared app.js,
  index.html, server.go or run localhost builds/tests. Integrator wires the entry.
- Assets worker: `internal/factory/assets/**` and its tests only. Own package
  API above. Do not edit go.mod/go.sum without proposing dependency to integrator.
- Integrator: shared types, account gateway, DB, planning execution, App broker,
  integration and routing. Subsequent branches extend tasks/review/verification.

All workers start from `proposal/software-factory`, branch independently, commit
and push only their files. Never merge main, deploy, change fleet, or read/print
credential files. Tests/builds execute inside their boxes; report missing tools
and actual outcomes. Existing local uncommitted reconciliation drafts are excluded.
