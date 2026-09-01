#!/usr/bin/env bash

set -euo pipefail

repo="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
fixtures="$repo/tests/fixtures"
test_root="$(mktemp -d)"
trap 'rm -rf -- "$test_root"' EXIT
pass_count=0

pass() {
  pass_count=$((pass_count + 1))
  printf 'ok %d - %s\n' "$pass_count" "$1"
}

fail() {
  printf 'not ok - %s\n' "$1" >&2
  exit 1
}

new_state() {
  local name="$1"
  VMBOX_TEST_STATE="$test_root/$name"
  export VMBOX_TEST_STATE
  mkdir -p "$VMBOX_TEST_STATE/home"
  : >"$VMBOX_TEST_STATE/railway.log"
}

run_vmbox() {
  HOME="$VMBOX_TEST_STATE/home" \
    PATH="$fixtures:$PATH" \
    VMBOX_CONFIG="$VMBOX_TEST_STATE/missing-config" \
    VMBOX_CREDENTIALS="$VMBOX_TEST_STATE/missing-credentials" \
    VMBOX_PROJECT_ID=test-project \
    VMBOX_ENVIRONMENT_ID=test-environment \
    "$repo/vmbox.sh" "$@"
}

bash -n "$repo/vmbox.sh" "$repo/install.sh" "$repo/entrypoint.sh" \
  "$fixtures/railway" "$fixtures/gh" "$fixtures/curl" "$fixtures/go" "$repo/tests/run.sh"
pass 'all Bash files parse'

for marker in EOF_VERIFY_TRUST EOF_PREPARE EOF_REPORT EOF_TASK EOF_DECORATE EOF_FETCH_TASK EOF_WAIT_TASK; do
  awk -v marker="$marker" '
    found && $0 == marker { exit }
    found { print }
    index($0, "<<\047" marker "\047") { found=1 }
  ' "$repo/vmbox.sh" >"$test_root/$marker.sh"
  [[ -s "$test_root/$marker.sh" ]] || fail "embedded script $marker was not found"
  bash -n "$test_root/$marker.sh"
done
pass 'all embedded remote Bash scripts parse'

"$repo/vmbox.sh" help | grep -Fq 'vmbox clean --all'
pass 'help works without Railway credentials or CLI'

new_state unknown-option
if run_vmbox alpha --detatch >"$VMBOX_TEST_STATE/output" 2>&1; then
  fail 'unknown box option unexpectedly succeeded'
fi
grep -Fq "unknown box option '--detatch'" "$VMBOX_TEST_STATE/output"
[[ ! -s "$VMBOX_TEST_STATE/railway.log" ]] || fail 'unknown option contacted Railway'
pass 'unknown options fail before infrastructure changes'

new_state github-discovery
github_result="$(
  HOME="$VMBOX_TEST_STATE/home" PATH="$fixtures:$PATH" \
    VMBOX_CONFIG="$VMBOX_TEST_STATE/missing-config" \
    VMBOX_CREDENTIALS="$VMBOX_TEST_STATE/missing-credentials" \
    VMBOX_TEST_SOURCE_ONLY=1 bash -c \
    '. "$1/vmbox.sh"; discover_github_accounts; printf "%s\t%s\t%s\n" "${github_hosts[0]}" "${github_users[0]}" "${github_protocols[0]}"' \
    bash "$repo"
)"
[[ "$github_result" == $'github.com\toctocat\tssh' ]] || fail "GitHub discovery returned: $github_result"
pass 'current GitHub CLI status output is parsed'

new_state api-token
export VMBOX_TEST_EXPECT_TOKEN='secret value with spaces'
api_result="$(
  HOME="$VMBOX_TEST_STATE/home" PATH="$fixtures:$PATH" \
    VMBOX_CONFIG="$VMBOX_TEST_STATE/missing-config" \
    VMBOX_CREDENTIALS="$VMBOX_TEST_STATE/missing-credentials" \
    VMBOX_TEST_SOURCE_ONLY=1 RAILWAY_API_TOKEN="$VMBOX_TEST_EXPECT_TOKEN" bash -c \
    '. "$1/vmbox.sh"; railway_api "query { ok }" "{}"' bash "$repo"
)"
[[ "$api_result" == '{"ok":true}' ]] || fail 'Railway API response was not parsed'
if grep -Fq "$VMBOX_TEST_EXPECT_TOKEN" "$VMBOX_TEST_STATE/curl.args"; then
  fail 'Railway token appeared in curl arguments'
fi
pass 'Railway API token stays out of process arguments'

new_state clean-all
run_vmbox clean --all --yes >"$VMBOX_TEST_STATE/output" 2>&1
grep -Fq 'service delete' "$VMBOX_TEST_STATE/railway.log"
grep -Fq 's-managed' "$VMBOX_TEST_STATE/railway.log"
grep -Fq 'v-managed' "$VMBOX_TEST_STATE/railway.log"
if grep -Eq 's-production|v-production' "$VMBOX_TEST_STATE/railway.log"; then
  fail 'clean --all targeted an unrelated resource'
fi
pass 'clean --all deletes only prefixed services and volumes'

new_state named-unrelated
if run_vmbox clean production --yes >"$VMBOX_TEST_STATE/output" 2>&1; then
  fail 'named cleanup targeted an unprefixed service without legacy opt-in'
fi
[[ ! -e "$VMBOX_TEST_STATE/service-deleted" ]] || fail 'unprefixed service was deleted'
pass 'named destructive commands reject unprefixed services by default'

new_state failed-clean
export VMBOX_TEST_FAIL_SERVICE_DELETE=1
if run_vmbox clean --all --yes >"$VMBOX_TEST_STATE/output" 2>&1; then
  fail 'failed service deletion unexpectedly succeeded'
fi
unset VMBOX_TEST_FAIL_SERVICE_DELETE
if grep -Fq 'volume delete' "$VMBOX_TEST_STATE/railway.log"; then
  fail 'volume deletion ran after service deletion failed'
fi
grep -Fq 'volumes were preserved' "$VMBOX_TEST_STATE/output"
pass 'service deletion failure preserves persistent volumes'

new_state deployment-reconcile
deployment_id="$(
  HOME="$VMBOX_TEST_STATE/home" PATH="$fixtures:$PATH" \
    VMBOX_CONFIG="$VMBOX_TEST_STATE/missing-config" \
    VMBOX_CREDENTIALS="$VMBOX_TEST_STATE/missing-credentials" \
    VMBOX_TEST_SOURCE_ONLY=1 VMBOX_PROJECT_ID=test-project \
    VMBOX_ENVIRONMENT_ID=test-environment bash -c \
    '. "$1/vmbox.sh"; service_name=vmbox-alpha; bundle="$2"; deploy_bundle' \
    bash "$repo" "$repo"
)"
[[ "$deployment_id" == deployment-new ]] || fail "deployment reconciliation returned: $deployment_id"
pass 'missing deployment IDs are reconciled against Railway state'

new_state stop-reconcile
export VMBOX_TEST_DOWN_FAIL_AFTER_REMOVE=1
run_vmbox stop alpha >"$VMBOX_TEST_STATE/output" 2>&1
unset VMBOX_TEST_DOWN_FAIL_AFTER_REMOVE
grep -Fq 'power-down response was interrupted; reconciling' "$VMBOX_TEST_STATE/output"
grep -Fq "Box 'alpha' is powered down" "$VMBOX_TEST_STATE/output"
pass 'interrupted power-down responses reconcile to actual state'

new_state pending-volume
export VMBOX_TEST_PENDING_VOLUME=1
HOME="$VMBOX_TEST_STATE/home" PATH="$fixtures:$PATH" \
  VMBOX_CONFIG="$VMBOX_TEST_STATE/missing-config" \
  VMBOX_CREDENTIALS="$VMBOX_TEST_STATE/missing-credentials" \
  VMBOX_TEST_SOURCE_ONLY=1 VMBOX_PROJECT_ID=test-project \
  VMBOX_ENVIRONMENT_ID=test-environment bash -c \
  '. "$1/vmbox.sh"; service_name=vmbox-alpha; selected_region=us-east; ensure_ready '\''{"id":"s-managed","status":"SUCCESS","regions":[{"name":"us-east4-eqdc4a"}]}'\''' \
  bash "$repo" >"$VMBOX_TEST_STATE/output" 2>&1
unset VMBOX_TEST_PENDING_VOLUME
[[ "$(<"$VMBOX_TEST_STATE/volume-list-count")" -ge 2 ]] || fail 'pending volume was not polled'
if grep -Eq ' up |volume .* add|volume .* create' "$VMBOX_TEST_STATE/railway.log"; then
  fail 'pending existing volume caused a duplicate create or deploy'
fi
pass 'existing volumes must become Ready before deployment continues'

new_state marker
marker_result="$(
  HOME="$VMBOX_TEST_STATE/home" VMBOX_CONFIG="$VMBOX_TEST_STATE/missing-config" \
    VMBOX_CREDENTIALS="$VMBOX_TEST_STATE/missing-credentials" VMBOX_TEST_SOURCE_ONLY=1 \
    bash -c '. "$1/vmbox.sh"; extract_task_json' bash "$repo" <<'EOF'
ssh banner
__VMBOX_TASK__
{"state":"completed","message":"contains __VMBOX_TASK__ safely"}
EOF
)"
[[ "$marker_result" == '{"state":"completed","message":"contains __VMBOX_TASK__ safely"}' ]] ||
  fail 'task status marker extraction was confused by message content'
pass 'task status framing tolerates marker text in reports'

new_state task-runtime
sed "s#/data/home#$VMBOX_TEST_STATE/home#g" "$test_root/EOF_REPORT.sh" >"$VMBOX_TEST_STATE/report.sh"
sed "s#/data/home#$VMBOX_TEST_STATE/home#g" "$test_root/EOF_TASK.sh" >"$VMBOX_TEST_STATE/task.sh"
printf 'test welcome\n' >"$VMBOX_TEST_STATE/home/.vmbox-welcome"
bash "$VMBOX_TEST_STATE/task.sh" bash -c 'sleep 0.2' >"$VMBOX_TEST_STATE/task.output" 2>&1 &
task_pid=$!
for _ in {1..50}; do
  [[ -r "$VMBOX_TEST_STATE/home/.vmbox-task-status.json" ]] && break
  sleep 0.01
done
bash "$VMBOX_TEST_STATE/report.sh" 'progress update' >/dev/null
wait "$task_pid"
jq -e '.state == "completed" and .exitCode == 0 and .message == "progress update"
  and (.runnerPid | type == "number") and ((.bootId // "") | length > 0)' \
  "$VMBOX_TEST_STATE/home/.vmbox-task-status.json" >/dev/null
[[ "$(stat -c %a "$VMBOX_TEST_STATE/home/.vmbox-task-status.json")" == 600 ]] ||
  fail 'task status mode is not 0600'
pass 'task runner and progress reporter update shared state safely'

new_state workspace-trust
HOME="$VMBOX_TEST_STATE/home" "$repo/entrypoint.sh" --configure-agent-trust /data/workspace/example
grep -Fq '[projects."/data/workspace/example"]' "$VMBOX_TEST_STATE/home/.codex/config.toml"
jq -e '.trustedDirectories | index("/data/workspace/example") != null' \
  "$VMBOX_TEST_STATE/home/.claude/settings.json" >/dev/null
pass 'runtime initializes Codex and Claude trust for the configured workspace'

new_state installer
install_bin="$VMBOX_TEST_STATE/bin with \$dollar"
shell_rc="$VMBOX_TEST_STATE/test.rc"
HOME="$VMBOX_TEST_STATE/home" PATH="$fixtures:$PATH" \
  VMBOX_INSTALL_DIR="$install_bin" VMBOX_SHELL_RC="$shell_rc" \
  XDG_CONFIG_HOME="$VMBOX_TEST_STATE/config" XDG_DATA_HOME="$VMBOX_TEST_STATE/data" \
  "$repo/install.sh" >"$VMBOX_TEST_STATE/output" 2>&1
resolved_path="$(PATH=/usr/bin:/bin bash -c '. "$1"; printf %s "$PATH"' bash "$shell_rc")"
[[ "${resolved_path%%:*}" == "$install_bin" ]] || fail 'installer wrote an unsafe PATH expression'
pass 'installer safely quotes unusual installation paths'

new_state go-installer
HOME="$VMBOX_TEST_STATE/home" PATH="$fixtures:$PATH" \
  VMBOX_INSTALL_DIR="$VMBOX_TEST_STATE/bin" VMBOX_SHELL_RC="$VMBOX_TEST_STATE/test.rc" \
  XDG_CONFIG_HOME="$VMBOX_TEST_STATE/config" XDG_DATA_HOME="$VMBOX_TEST_STATE/data" \
  "$repo/install.sh" --go-cli >"$VMBOX_TEST_STATE/output" 2>&1
"$VMBOX_TEST_STATE/bin/vmbox" --help | grep -Fq 'Go vmbox test binary'
[[ ! -s "$VMBOX_TEST_STATE/railway.log" ]] || fail 'Go CLI installation unnecessarily required Railway'
pass 'installer offers an explicit Go CLI path'

new_state token-installer
saved_token='token value with shell metacharacters $`"'
HOME="$VMBOX_TEST_STATE/home" PATH="$fixtures:$PATH" \
  RAILWAY_API_TOKEN="$saved_token" VMBOX_INSTALL_DIR="$VMBOX_TEST_STATE/bin" \
  XDG_CONFIG_HOME="$VMBOX_TEST_STATE/config" XDG_DATA_HOME="$VMBOX_TEST_STATE/data" \
  "$repo/install.sh" --workspace-token --no-shell-update >"$VMBOX_TEST_STATE/output" 2>&1
credentials_file="$VMBOX_TEST_STATE/config/vmbox/credentials"
[[ "$(stat -c %a "$credentials_file")" == 600 ]] || fail 'saved credential mode is not 0600'
[[ "$(stat -c %a "$VMBOX_TEST_STATE/config/vmbox")" == 700 ]] || fail 'credential directory mode is not 0700'
loaded_token="$(env -u RAILWAY_API_TOKEN bash -c '. "$1"; printf %s "$RAILWAY_API_TOKEN"' bash "$credentials_file")"
[[ "$loaded_token" == "$saved_token" ]] || fail 'saved credential did not round-trip safely'
pass 'installer stores shell-sensitive tokens atomically with private modes'

printf '1..%d\n' "$pass_count"
