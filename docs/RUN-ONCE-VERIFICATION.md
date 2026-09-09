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
- Committed/pushed feature checkpoint c98fb12; production still runs the old UI.
- In-box job ec7903c9-431e-46a8-8445-e1cb00000000: controller PostgreSQL/race
  tests passed (10.650s). Runtime TestIdleHibernateRealTmux failed during sync
  after its 10-second context expired, immediately after installing packages.
  The test script now flushes package writes first and runs package race checks
  sequentially. Remaining tests and the rerun are pending.

Remaining before completion:

- Run PostgreSQL, actual tmux and browser tests; test races/cancellation/reconnect.
- Live shell success/failure and Claude/Codex execution with valid saved profiles.
- Observe output, exact exit code, hibernation, retained workspace, slot release.
- Commit/push/merge/deploy controller; verify replacement in production.
- Stop/retire the separate Tasks service without deleting its database or volume;
  remove obsolete controller gateway environment variables. Preserve old data.
