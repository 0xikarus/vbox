# Factory implementation progress

Feature branch: `proposal/software-factory`. No main merge or production deploy.
Maximum authorized fleet capacity: **6**. Current desired capacity remains 4;
two isolated workers use existing capacity. User box `tt2` is untouched.

## 2026-09-08 bootstrap

- Read the full goal attachment and current source/state.
- Shared first-slice contract committed/pushed as `69b5072`.
- Added core work/plan types and validation tests (not yet executed in-box).
- UI worker: `factory-ui-0908`, box ID
  `1e2139db-766f-4a7e-8440-ced500000000`, task ID
  `0458ec2e-c979-4bb4-8b6b-edad00000000`, branch `factory/ui-plan-0908`.
  Last observed **running**. Scope: factory JS/CSS and scoped UI tests only.
- Asset worker: `factory-assets-0908`, box ID
  `1bf0d5e0-129f-4828-86f6-672200000000`, task ID
  `a23c41ab-7ed4-4312-815e-ee0200000000`, branch `factory/assets-0908`.
  Last observed **queued**. Scope: private asset package and tests only.
- Both use the saved Codex profile that passed provisioning and existing GitHub
  credentials. Other Claude/Codex profile selections were rejected at create
  (HTTP 400); those rejected requests did not create their requested workspaces.
  A current Claude profile and authorized test GitHub App configuration were
  requested securely for later live proof. These are not reasons to halt coding.

## Next actions

Poll the exact task IDs above; observation failures must not cause resubmission.
Integrate only their scoped commits into the feature branch after inspecting
actual diffs/test evidence. Continue coordinator persistence, account gateway,
repository/App broker, image staging and real planning response ingestion.
Run all builds/tests inside vmbox boxes, including core validation tests.

The UI must show idea -> agent responses/questions -> editable plan -> approval
-> master/feature issues -> per-feature PRs -> verified product. GitHub is the
linked record, not a replacement for the UI conversation. The original broader
plan still needs reconciliation with this full approved workflow.

Not complete: no functional factory service/UI integration yet, no live App or
multimodal planning proof, no feature build/review/integration workflow. Existing
unrelated startup/reconciliation drafts remain excluded from all factory commits.
