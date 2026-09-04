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
  "$repo/scripts/rollout-monitor.sh" "$repo/tests/controller-e2e.sh" \
  "$fixtures/railway" "$fixtures/gh" "$fixtures/curl" "$fixtures/go" \
  "$fixtures/ssh" "$fixtures/ssh-keygen" "$fixtures/docker" "$repo/tests/run.sh"
pass 'all Bash files parse'

for marker in EOF_VERIFY_TRUST EOF_PREPARE EOF_REPORT EOF_TASK EOF_DECORATE EOF_FETCH_TASK \
  EOF_WAIT_TASK EOF_DATA_PROBE EOF_ADDRESS_PROBE EOF_GITHUB_LOGIN EOF_OWNERSHIP_REPAIR; do
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
unset VMBOX_TEST_EXPECT_TOKEN
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

# --- Installed deployment bundle ---------------------------------------------
#
# `railway up "$bundle" --path-as-root` uploads exactly the installed bundle
# directory as the build context. A bundle whose Dockerfile referenced go.mod,
# cmd/ and internal/ while shipping none of them could never build, so the box
# it was supposed to create never existed.
install_bundle_into() {
  local state="$1"
  shift
  HOME="$state/home" PATH="$fixtures:$PATH" \
    VMBOX_INSTALL_DIR="$state/bin" VMBOX_SHELL_RC="$state/test.rc" \
    XDG_CONFIG_HOME="$state/config" XDG_DATA_HOME="$state/data" \
    "$repo/install.sh" "$@" >"$state/output" 2>&1
}

# A bundle is uploaded verbatim to Railway and baked into an image layer, so it
# must never carry a usable secret. The patterns below look for credential
# values, not for the names of the variables that hold them.
assert_bundle_is_credential_free() {
  local bundle="$1" offender
  offender="$(grep -rlIE \
    "(RAILWAY_(API_)?TOKEN=[\"']?[A-Za-z0-9/+_-]{20,}|gh[pousr]_[A-Za-z0-9]{30,}|^-----BEGIN [A-Z ]*PRIVATE KEY-----)" \
    "$bundle" 2>/dev/null || true)"
  [[ -z "$offender" ]] || fail "deployment bundle contains credential material: $offender"
}

new_state bundle-image
install_bundle_into "$VMBOX_TEST_STATE" --no-shell-update
bundle_dir="$VMBOX_TEST_STATE/data/vmbox/service"
[[ -f "$bundle_dir/Dockerfile" ]] || fail 'installed bundle has no Dockerfile'
grep -Fq 'ghcr.io/0xikarus/vmbox-service@sha256:747d32f73c7847511b1bb5234fd40c6838c7bc43d1508247f2c9ea1d1ee194c5' \
  "$bundle_dir/Dockerfile" || fail 'installed bundle does not pin the audited worker image by digest'
"$fixtures/docker" build "$bundle_dir" >"$VMBOX_TEST_STATE/build" 2>&1 ||
  { cat "$VMBOX_TEST_STATE/build" >&2; fail 'installed bundle cannot build'; }
assert_bundle_is_credential_free "$bundle_dir"
pass 'installed deployment bundle is self-contained, credential-free, and builds'

new_state bundle-deploy
install_bundle_into "$VMBOX_TEST_STATE" --no-shell-update
bundle_dir="$VMBOX_TEST_STATE/data/vmbox/service"
: >"$VMBOX_TEST_STATE/railway.log"
deployment_id="$(
  HOME="$VMBOX_TEST_STATE/home" PATH="$fixtures:$PATH" \
    VMBOX_CONFIG="$VMBOX_TEST_STATE/missing-config" \
    VMBOX_CREDENTIALS="$VMBOX_TEST_STATE/missing-credentials" \
    VMBOX_TEST_SOURCE_ONLY=1 VMBOX_PROJECT_ID=test-project \
    VMBOX_ENVIRONMENT_ID=test-environment bash -c \
    '. "$1/vmbox.sh"; service_name=vmbox-alpha; bundle="$2"; deploy_bundle' \
    bash "$repo" "$bundle_dir"
)"
[[ "$deployment_id" == deployment-new ]] || fail "installed bundle did not deploy: $deployment_id"
grep -Fq -- "--path-as-root" "$VMBOX_TEST_STATE/railway.log" ||
  fail 'installed bundle was not uploaded as its own build root'
pass 'vmbox deploys the installed bundle through railway up'

new_state bundle-source
install_bundle_into "$VMBOX_TEST_STATE" --no-shell-update --bundle-from-source
bundle_dir="$VMBOX_TEST_STATE/data/vmbox/service"
for payload in go.mod go.sum cmd internal tmux.conf entrypoint.sh; do
  [[ -e "$bundle_dir/$payload" ]] || fail "source bundle is missing $payload"
done
"$fixtures/docker" build "$bundle_dir" >"$VMBOX_TEST_STATE/build" 2>&1 ||
  { cat "$VMBOX_TEST_STATE/build" >&2; fail 'source bundle cannot build'; }
assert_bundle_is_credential_free "$bundle_dir"
pass 'source deployment bundle ships the whole Go build payload and builds'

new_state bundle-incomplete
mkdir -p "$VMBOX_TEST_STATE/incomplete"
install -m 644 "$repo/Dockerfile" "$repo/.dockerignore" "$VMBOX_TEST_STATE/incomplete/"
install -m 755 "$repo/entrypoint.sh" "$VMBOX_TEST_STATE/incomplete/"
if "$fixtures/docker" build "$VMBOX_TEST_STATE/incomplete" >"$VMBOX_TEST_STATE/build" 2>&1; then
  fail 'a bundle without go.mod, cmd, and internal was accepted as buildable'
fi
grep -Fq 'is not in the build context' "$VMBOX_TEST_STATE/build" ||
  fail 'incomplete bundle failed for an unexpected reason'
pass 'the bundle check rejects the incomplete layout that shipped before'

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

# --- Direct SSH data path ------------------------------------------------------
#
# Railway is only asked once per operation which deployment instance backs a
# service. Everything else rides one ControlMaster owned by this CLI.
ssh_state() {
  new_state "$1"
  mkdir -p "$VMBOX_TEST_STATE/config/vmbox" "$VMBOX_TEST_STATE/tmp"
  : >"$VMBOX_TEST_STATE/ssh.log"
  : >"$VMBOX_TEST_STATE/ssh-remote.log"
  : >"$VMBOX_TEST_STATE/ssh-keygen.log"
}

run_sourced() {
  HOME="$VMBOX_TEST_STATE/home" PATH="$fixtures:$PATH" \
    TMPDIR="$VMBOX_TEST_STATE/tmp" \
    VMBOX_CONFIG="$VMBOX_TEST_STATE/config/vmbox/config" \
    VMBOX_CREDENTIALS="$VMBOX_TEST_STATE/missing-credentials" \
    VMBOX_TEST_SOURCE_ONLY=1 VMBOX_PROJECT_ID=test-project \
    VMBOX_ENVIRONMENT_ID=test-environment RAILWAY_API_TOKEN=test-token \
    bash -c "$1" bash "$repo"
}

ssh_state ssh-reuse
run_sourced '
  . "$1/vmbox.sh"
  service_name=vmbox-alpha
  box_ssh true >/dev/null
  box_ssh true >/dev/null
  box_ssh_as_workload true >/dev/null
' >"$VMBOX_TEST_STATE/output" 2>&1 ||
  { cat "$VMBOX_TEST_STATE/output" >&2; fail 'remote commands did not run over direct SSH'; }
[[ "$(<"$VMBOX_TEST_STATE/instance-lookups")" == 1 ]] ||
  fail "endpoint was resolved $(<"$VMBOX_TEST_STATE/instance-lookups") times instead of once"
master_starts="$(grep -c -e ' -M ' "$VMBOX_TEST_STATE/ssh.log" || true)"
[[ "$master_starts" == 1 ]] || fail "opened $master_starts ControlMasters instead of one"
[[ "$(<"$VMBOX_TEST_STATE/ssh-command-count")" == 3 ]] ||
  fail "commands did not all stream over the shared connection"
grep -Fq 'deployment-instance@ssh.railway.com' "$VMBOX_TEST_STATE/ssh.log" ||
  fail 'commands did not target the resolved deployment instance'
if grep -Eq "^railway ssh|' ssh '" "$VMBOX_TEST_STATE/railway.log"; then
  fail 'the CLI still routed remote commands through railway ssh'
fi
pass 'one Railway lookup and one ControlMaster serve every remote command'

ssh_state ssh-workload
run_sourced '
  . "$1/vmbox.sh"
  service_name=vmbox-alpha
  box_ssh_as_workload true >/dev/null
  box_ssh_as_workload_interactive true >/dev/null
' >"$VMBOX_TEST_STATE/output" 2>&1 ||
  { cat "$VMBOX_TEST_STATE/output" >&2; fail 'unprivileged remote commands failed'; }
workload_commands="$(grep -c "^'sudo' '-n' '-H' '-u' 'vmbox' '--' 'env' 'HOME=/data/home'" \
  "$VMBOX_TEST_STATE/ssh-remote.log" || true)"
[[ "$workload_commands" == 2 ]] ||
  fail "only $workload_commands remote commands dropped to the vmbox user with HOME=/data/home"
grep -Fq -- '-tt' "$VMBOX_TEST_STATE/ssh.log" ||
  fail 'the interactive path lost its terminal allocation'
grep -Fq "'TERM=" "$VMBOX_TEST_STATE/ssh-remote.log" ||
  fail 'the interactive path did not carry the terminal type through sudo'
pass 'remote commands run as vmbox with HOME=/data/home and keep an interactive TTY'

ssh_state ssh-control-path
control_path="$(run_sourced '
  . "$1/vmbox.sh"
  ssh_control_path_for "deployment-instance@ssh.railway.com"
')"
[[ ${#control_path} -lt 100 ]] || fail "ControlPath is $((${#control_path})) bytes: $control_path"
[[ "$control_path" == "$VMBOX_TEST_STATE/tmp/vmbox-ssh-$(id -u)/"*.sock ]] ||
  fail "ControlPath is not in a private per-user directory: $control_path"
[[ "$(stat -c %a "$(dirname -- "$control_path")")" == 700 ]] ||
  fail 'the SSH control directory is not private'
pass 'the ControlPath stays short and private'

ssh_state ssh-host-key
known_hosts="$VMBOX_TEST_STATE/config/vmbox/railway-known-hosts"
printf 'ssh.railway.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAstale\nexample.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAkeep\n' \
  >"$known_hosts"
mkdir -p "$VMBOX_TEST_STATE/home/.ssh"
printf 'ssh.railway.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAuser\n' \
  >"$VMBOX_TEST_STATE/home/.ssh/known_hosts"
VMBOX_TEST_SSH_ROTATED_KEY=1 run_sourced '
  . "$1/vmbox.sh"
  service_name=vmbox-alpha
  box_ssh true >/dev/null
' >"$VMBOX_TEST_STATE/output" 2>&1 || fail 'a rotated host key was not recovered'
grep -Fq -- "-R ssh.railway.com" "$VMBOX_TEST_STATE/ssh-keygen.log" ||
  fail 'host-key recovery did not target ssh.railway.com'
grep -Fq -- "-f $known_hosts" "$VMBOX_TEST_STATE/ssh-keygen.log" ||
  fail 'host-key recovery did not use the vmbox-only known-hosts file'
grep -Fq 'ssh.railway.com' "$known_hosts" && fail 'the stale Railway host key survived'
grep -Fq 'example.com' "$known_hosts" || fail 'recovery removed an unrelated known host'
grep -Fq 'ssh.railway.com' "$VMBOX_TEST_STATE/home/.ssh/known_hosts" ||
  fail 'recovery touched the user own known_hosts file'
pass 'a rotated ssh.railway.com host key is repaired narrowly and safely'

ssh_state ssh-transport-failure
VMBOX_TEST_SSH_FAIL_ONCE=1 run_sourced '
  . "$1/vmbox.sh"
  service_name=vmbox-alpha
  box_ssh true >/dev/null
' >"$VMBOX_TEST_STATE/output" 2>&1 || fail 'a transport failure was not retried'
[[ "$(<"$VMBOX_TEST_STATE/instance-lookups")" == 2 ]] ||
  fail 'SSH exit 255 did not invalidate the cached deployment identity'
[[ "$(<"$VMBOX_TEST_STATE/ssh-command-count")" == 1 ]] ||
  fail 'the retried command did not run exactly once'
pass 'SSH exit 255 invalidates the endpoint and the master before one retry'

ssh_state ssh-fallback
VMBOX_TEST_INSTANCE_LOOKUP_FAILS=1 run_sourced '
  . "$1/vmbox.sh"
  service_name=vmbox-alpha
  box_ssh true >/dev/null || true
' >"$VMBOX_TEST_STATE/output" 2>&1
grep -Fq "direct Railway SSH is unavailable" "$VMBOX_TEST_STATE/output" ||
  fail 'an unresolvable endpoint did not fall back to railway ssh'
grep -Fq "ssh" "$VMBOX_TEST_STATE/railway.log" ||
  fail 'the fallback never reached railway ssh'
pass 'an unresolvable deployment instance falls back to railway ssh'

# A box provisioned before the ownership fix still holds root-owned credentials.
# The repair has to run as root — vmbox cannot give away files it does not own —
# and must touch nothing that already belongs to the workload user.
ssh_state ownership-repair
run_sourced '
  . "$1/vmbox.sh"
  service_name=vmbox-alpha
  repair_workload_ownership
' >"$VMBOX_TEST_STATE/output" 2>&1 ||
  { cat "$VMBOX_TEST_STATE/output" >&2; fail 'the ownership repair did not run'; }
if grep -q "^'sudo'" "$VMBOX_TEST_STATE/ssh-remote.log"; then
  fail 'the ownership repair dropped to vmbox, which cannot chown root-owned files'
fi
grep -Fq '! -user vmbox -exec chown vmbox:vmbox' "$VMBOX_TEST_STATE/ssh-remote.log" ||
  fail 'the ownership repair did not skip files the workload user already owns'
grep -Fq '/data/workspace -xdev -maxdepth 1' "$VMBOX_TEST_STATE/ssh-remote.log" ||
  fail 'the ownership repair was not bounded to the installed workspace files'
pass 'a box damaged by root-owned credentials is repaired in place'

# --- /data verification --------------------------------------------------------
#
# A failed SSH transport is not evidence that /data is gone. Treating it as such
# escalated a rotated host key into a repair redeploy of a healthy box.
ssh_state data-mounted
run_sourced '
  . "$1/vmbox.sh"
  service_name=vmbox-alpha
  persistent_data_mounted
  printf "mounted=%s conclusive=%s\n" "$?" "$data_probe_conclusive"
' >"$VMBOX_TEST_STATE/output" 2>&1 ||
  { cat "$VMBOX_TEST_STATE/output" >&2; fail 'the /data probe did not complete'; }
grep -Fq 'mounted=0 conclusive=1' "$VMBOX_TEST_STATE/output" ||
  fail "mounted /data was not recognized: $(cat "$VMBOX_TEST_STATE/output")"
pass 'a mounted /data is recognized from its filesystem type'

ssh_state data-missing
VMBOX_TEST_SSH_FSTYPE=none run_sourced '
  . "$1/vmbox.sh"
  service_name=vmbox-alpha
  persistent_data_mounted || true
  printf "conclusive=%s\n" "$data_probe_conclusive"
' >"$VMBOX_TEST_STATE/output" 2>&1 ||
  { cat "$VMBOX_TEST_STATE/output" >&2; fail 'the negative /data probe did not complete'; }
grep -Fq 'conclusive=1' "$VMBOX_TEST_STATE/output" ||
  fail 'a definitive negative probe was not recognized as conclusive'
pass 'an unmounted /data is a conclusive negative answer'

ssh_state data-unreachable
VMBOX_TEST_INSTANCE_LOOKUP_FAILS=1 run_sourced '
  . "$1/vmbox.sh"
  service_name=vmbox-alpha
  ensure_persistent_data
' >"$VMBOX_TEST_STATE/output" 2>&1 && fail 'an unreachable box was reported as healthy'
grep -Fq 'nothing was redeployed' "$VMBOX_TEST_STATE/output" ||
  fail "an unreachable box did not report the transport failure: $(cat "$VMBOX_TEST_STATE/output")"
if grep -Eq "redeploy|' up '" "$VMBOX_TEST_STATE/railway.log"; then
  fail 'an unreachable box triggered a repair redeploy'
fi
pass 'an unreachable box never turns into a repair redeploy of a healthy volume'

# --- Resumable provisioning ----------------------------------------------------
#
# A Railway service is only the first provisioning step. Until the credential
# sync that follows it has run, the box is not configured, and the CLI must
# resume instead of attaching to a silently empty box.
ssh_state provisioning-resume
run_sourced '
  . "$1/vmbox.sh"
  service_name=vmbox-alpha
  selected_region=us-east
  component_ids=(codex claude opencode bun)
  component_selected=(1 0 0 0)
  profile_providers=(); profile_sources=(); profile_selected=()
  github_hosts=(); github_users=(); github_protocols=(); github_selected=()
  instruction_sources=(); instruction_selected=()
  provisioning_incomplete && { echo "unexpected record"; exit 1; }
  begin_provisioning
  provisioning_incomplete || { echo "record was not written"; exit 1; }
  printf "mode=%s\n" "$(stat -c %a "$(provisioning_record_path)")"
  component_selected=(0 0 0 0)
  selected_region=""
  resume_provisioning_record
  printf "resumed_components=%s region=%s\n" "$(selected_components_value)" "$selected_region"
  complete_provisioning
  provisioning_incomplete && { echo "record survived completion"; exit 1; }
  echo cleared
' >"$VMBOX_TEST_STATE/output" 2>&1 || { cat "$VMBOX_TEST_STATE/output" >&2; fail 'provisioning record lifecycle failed'; }
grep -Fq 'mode=600' "$VMBOX_TEST_STATE/output" || fail 'provisioning record is not private'
grep -Fq "first-time setup never finished; resuming it" "$VMBOX_TEST_STATE/output" ||
  fail 'an incomplete service was not reported as resumable'
grep -Fq 'resumed_components=codex region=us-east' "$VMBOX_TEST_STATE/output" ||
  fail "the saved setup was not restored: $(cat "$VMBOX_TEST_STATE/output")"
grep -Fq cleared "$VMBOX_TEST_STATE/output" || fail 'a completed provisioning left its record behind'
pass 'interrupted provisioning is recorded, resumed, and cleared'

ssh_state provisioning-tolerates-gaps
run_sourced '
  . "$1/vmbox.sh"
  service_name=vmbox-alpha
  mkdir -p "$(provisioning_directory)"
  cat >"$(provisioning_record_path)" <<JSON
{"version":1,"savedAt":"2026-09-04T00:00:00Z","components":["codex"],"region":"us-east",
 "profiles":[{"provider":"codex","source":"/nonexistent/profile"}],"github":null,"instructions":null}
JSON
  component_ids=(codex claude opencode bun)
  component_selected=(0 0 0 0)
  resume_provisioning_record
  echo resumed
' >"$VMBOX_TEST_STATE/output" 2>&1 || { cat "$VMBOX_TEST_STATE/output" >&2; fail 'a resume refused to continue past a missing profile'; }
grep -Fq 'no longer available: /nonexistent/profile' "$VMBOX_TEST_STATE/output" ||
  fail 'a missing saved profile was not reported'
grep -Fq resumed "$VMBOX_TEST_STATE/output" ||
  fail 'a missing saved profile blocked the resume'
pass 'a resumed setup warns about vanished selections instead of stranding the box'

ssh_state provisioning-cleaned
run_sourced '
  . "$1/vmbox.sh"
  service_name=vmbox-alpha
  mkdir -p "$(provisioning_directory)"
  : >"$(provisioning_record_path)"
  clean_boxes alpha --yes >/dev/null
  provisioning_incomplete && echo "record survived deletion" || echo cleared
' >"$VMBOX_TEST_STATE/output" 2>&1 ||
  { cat "$VMBOX_TEST_STATE/output" >&2; fail 'cleaning a box failed'; }
grep -Fq cleared "$VMBOX_TEST_STATE/output" ||
  fail 'deleting a box left its provisioning record behind'
pass 'deleting a box drops its provisioning record'

printf '1..%d\n' "$pass_count"
