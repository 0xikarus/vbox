#!/usr/bin/env bash
# Local replacement for the former GitHub Actions CI (removed to avoid paid
# runner minutes). Run before merging: scripts/check.sh [--browser]
set -euo pipefail
cd "$(dirname "$0")/.."

echo "== compile every package"
go test ./... -run '^$'
echo "== vet"
go vet ./...
echo "== bundled mascot model and held-out classification"
go run ./cmd/mascot-train -check
go test ./internal/controller -run 'TestClassifyMascotText|TestMascotModelHeldoutExamples|TestMascotObservationScopesSessionAndStoresOnlyState'
echo "== local activity generator export and Python parity"
go test ./internal/activityphrase -run 'TestGeneratorArtifactChecksum|TestGeneratorMatchesPythonExport' -count=1
go test ./internal/controller -run 'TestActivityPhraseMatchesPythonFinalOutput' -count=1
echo "== agent authorization and delivery boundaries"
go test ./internal/controller -run 'TestAgentBoxQuota|TestAgentEmailQuota|TestQueueAgentFollowup|TestReconcileAgentFollowup|TestTeamRolePreset|TestEffectiveAgent'
echo "== controller tests"
go test ./internal/controller ./internal/loginprofile

if [[ "${1:-}" == "--browser" ]]; then
  echo "== browser suite (serial)"
  npm run test:browser
fi
echo "All checks passed."
