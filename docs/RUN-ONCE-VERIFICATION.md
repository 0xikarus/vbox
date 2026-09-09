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
- Replacement deployed successfully at ac4681a (deployment
  8f62ff52-6202-4570-91dc-9301b780aac2).
- In-box job ec7903c9-431e-46a8-8445-e1cb00000000: controller PostgreSQL/race
  tests passed (10.650s). Runtime TestIdleHibernateRealTmux failed during sync
  after its 10-second context expired, immediately after installing packages.
  The test script now flushes package writes first and runs package race checks
  sequentially.
- Corrected in-box job ae0229b9-578d-4ee8-88ed-38c500000000 on 50fb8a3
  exited 0: controller PostgreSQL/race (8.400s), real runtime/tmux race (4.636s),
  full Go tests, vet/build, and all seven Chromium tests passed.
- Added explicit New run control for intentional repetition of identical input.
  In-box browser-only job 9022ac87-4799-49e1-8e11-77f400000000 on 8eaa1e7
  exited 0; all seven tests, including the fresh-key assertion, passed.
- Live shell 7db9250b-dbfb-4b51-8123-4a3600000000: actual exit 7 and retained
  output; observed hibernated, then a separate browser allocation resumed it.
- Claude 6b7a0e07-08a6-4644-8437-b45b00000000 and Codex
  60c89ca8-ca8b-4ffe-8fac-7d4f00000000: fresh saved profiles, real date
  responses, exit 0. Claude hibernated. Codex retained an independently opened
  interactive shell; safe hibernation correctly preserved that session.
- Focused shell b19eb202-22fd-4c05-86a4-374100000000: actual changing tmux
  output visible, reconnect/reload showed the same run, exit 0, then hibernated.
  Browser POSTs were only login and one submission; no replay or browser errors.
  Screenshot: /tmp/run-once-terminal-live.png. Retained volume:
  dc6eb657-8252-4467-843f-f18e296b35a8.
- The box list previously opened these retained workspaces as ordinary shells.
  0f30159 adds authoritative box-to-run lookup before any web allocation, plus
  prompt guidance, literal model/argv options and scoped image-download links.
- In-box job 2ab1e8df-4807-410f-861a-1d7100000000 on 0f30159: PostgreSQL/race
  (7.343s), real tmux/race (6.174s), full Go tests/vet/build passed. Browser result
  was 7/8: the new image test lacked an empty-history fixture. Fixed in 84025b1;
  all eight Chromium tests pass locally. Image database tests verified byte fidelity,
  account isolation, incorrect-token rejection, expiry, and numbered prompt URLs.
- 84025b1 also consolidates interactive CLI task selection into one shared dialog;
  local full CLI tests pass. Final in-box job b9fcdece-24f3-4e81-8a40-860500000000
  exited 0: CLI tests, vet/build and all eight Chromium tests passed.
- Retired Tasks instance fe2cb76d-d39f-4228-9576-6ec475199c85 is EXITED.
  Service/database/volume remain preserved, with execution disabled in its variables.
- Main efb0076 deployed successfully (c54ccf3d-ae8f-4f73-8046-af5b3ae5e764).
  Production assets expose guidance, images and model controls. Obsolete controller
  VMBOX_FACTORY_URL and VMBOX_FACTORY_GATEWAY_TOKEN variables were removed.
  The verified main revision was installed locally without shell-startup changes.
- Live ordinary box-link navigation to the completed focused shell test returned
  its actual saved output and run ID. Reload caused zero POSTs, zero browser errors,
  and left the box hibernated. Screenshot: /tmp/run-once-retained-results-live.png.
- Read-only SSH inspection of the already resumed failure-test box found exactly
  one execution-counter line in /data/workspace/proof-1788930386984. The file
  survived hibernation/resume on volume 74cec5cc-0bf9-473a-9abe-293f5f2e5cfa;
  no additional execution or write was performed for this check.
- Fresh Codex, Claude and GitHub test profiles were uploaded without overwriting
  existing profiles or exposing credentials. The final research launch was rejected
  before execution by the safety reviewer; direct user confirmation is pending.

Remaining before completion:

- Live image retrieval and inspection; actual model/argument override delivery.
- Verify live image download/inspection and final task slot release.
- Final Codex + GitHub profile one-shot: research stonkfun.xyz and create an issue
  in 0xikarus/research-crypto; verify real issue contents and task exit/hibernation.
