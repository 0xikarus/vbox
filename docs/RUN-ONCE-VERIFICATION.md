# Run once replacement — verification in progress

Scope: replace orchestration added since f96c817 with the existing process-task
runtime and web tmux attachment. Keep account profiles, fleet lifecycle, native
terminal streaming, process journals, and the removal of the account API quota.

Removed locally: factory/taskflow/taskflowruntime packages and entrypoints,
gateway, orchestration UI, deployment packaging, and associated tests/docs.
The unfinished orchestration drafts were archived at
`/tmp/vmbox-orchestration-removal-drafts-20260909.tgz` before removal.
Unrelated interaction/session reconciliation drafts remain untouched.

Implemented locally: account-scoped durable Run once queue, idempotent submission,
slot waiting/cancellation, deterministic box reservation recovery, existing
process-task dispatch, and task-aware web terminal/results view.

Evidence so far:

- Go test ./... and go vet ./... passed after initial removal/implementation in
  a Go 1.26 container. PostgreSQL/real tmux checks require their test environment.
- New PostgreSQL queue/recovery tests and in-box test script added; not yet run.
- Not committed/deployed at this checkpoint. Production still runs the old UI.

Remaining before completion:

- Run PostgreSQL, actual tmux and browser tests; test races/cancellation/reconnect.
- Live shell success/failure and Claude/Codex execution with valid saved profiles.
- Observe output, exact exit code, hibernation, retained workspace, slot release.
- Commit/push/merge/deploy controller; verify replacement in production.
- Stop/retire the separate Tasks service without deleting its database or volume;
  remove obsolete controller gateway environment variables. Preserve old data.
