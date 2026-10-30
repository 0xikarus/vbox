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

bash -n "$repo/install.sh" "$repo/scripts/worker-entrypoint.sh" "$repo/scripts/worker-agent-trust.sh" \
  "$repo/scripts/rollout-monitor.sh" "$repo/scripts/test-installed-bundle.sh" \
  "$repo/tests/controller-e2e.sh" "$fixtures/go" "$repo/tests/run.sh"
pass 'all maintained Bash files parse'

trust_home="$test_root/trust-home"
trust_workspace="$test_root/workspace"
mkdir -p "$trust_home" "$trust_workspace"
HOME="$trust_home" "$repo/scripts/worker-entrypoint.sh" --configure-agent-trust "$trust_workspace"
jq -e --arg workspace "$trust_workspace" \
  '.projects[$workspace].hasTrustDialogAccepted == true' "$trust_home/.claude.json" >/dev/null
test "$(stat -c '%a' "$trust_home/.claude.json")" = 600
pass 'entrypoint records current Claude workspace trust with owner-only mode'

mkdir -p "$test_root/home"
HOME="$test_root/home" PATH="$fixtures:$PATH" \
  VMBOX_INSTALL_DIR="$test_root/bin" VMBOX_SHELL_RC="$test_root/bashrc" \
  XDG_CONFIG_HOME="$test_root/config" XDG_DATA_HOME="$test_root/data" \
  "$repo/install.sh" --no-shell-update >"$test_root/install.out"

installed_help="$("$test_root/bin/vbox" --help)"
grep -Fq 'Go vbox test binary' <<<"$installed_help"
test "$(readlink "$test_root/bin/vmbox")" = vbox
alias_help="$("$test_root/bin/vmbox" --help)"
grep -Fq 'Go vbox test binary' <<<"$alias_help"
test ! -e "$test_root/data/vmbox/service"
test ! -e "$test_root/data/vmbox/runtime"
pass 'installer builds only CLI (vbox plus vmbox alias); no standalone bundle or worker payload'

mkdir -p "$test_root/docker-bin"
cat >"$test_root/docker-bin/docker" <<'DOCKER'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$VMBOX_INSTALL_TEST_DOCKER_LOG"
[[ "${1:-}" != --host ]] || shift 2
case "${1:-}" in
 info) printf '%s\n' linux/aarch64 ;;
 build) exit "${VMBOX_INSTALL_TEST_BUILD_EXIT:-0}" ;;
 *) echo 'Unexpected Docker action in installer test' >&2; exit 1 ;;
esac
DOCKER
chmod +x "$test_root/docker-bin/docker"
HOME="$test_root/home" PATH="$test_root/docker-bin:$fixtures:$PATH" \
 VMBOX_INSTALL_DIR="$test_root/bin" VMBOX_INSTALL_TEST_DOCKER_LOG="$test_root/docker.log" \
 "$repo/install.sh" --no-shell-update --standalone --host test-host --image standalone:test >"$test_root/standalone.out"
grep -Fq -- '--host ssh://test-host info' "$test_root/docker.log"
grep -Fq -- '--host ssh://test-host build --file' "$test_root/docker.log"
grep -Fq -- '--tag standalone:test' "$test_root/docker.log"
grep -Fq -- 'vbox standalone create my-box --mode container --host test-host --image standalone:test' "$test_root/standalone.out"
pass 'standalone installation builds on the selected SSH engine and prints the matching creation command'

: >"$test_root/docker.log"
HOME="$test_root/home" PATH="$test_root/docker-bin:$fixtures:$PATH" \
 VMBOX_INSTALL_DIR="$test_root/bin" VMBOX_INSTALL_TEST_DOCKER_LOG="$test_root/docker.log" \
 "$repo/install.sh" --no-shell-update --standalone >"$test_root/standalone-local.out"
grep -Fq -- 'build --file' "$test_root/docker.log"
grep -Fq -- '--tag vbox-standalone:dev' "$test_root/docker.log"
if grep -Fq -- '--host' "$test_root/docker.log"; then echo 'Local install unexpectedly uses SSH' >&2; exit 1; fi
pass 'standalone installation uses the current engine and default image locally'

printf '#!/bin/sh\nexit 42\n' >"$test_root/bin/vbox"
cp "$test_root/bin/vbox" "$test_root/previous-cli"
if HOME="$test_root/home" PATH="$test_root/docker-bin:$fixtures:$PATH" \
 VMBOX_INSTALL_DIR="$test_root/bin" VMBOX_INSTALL_TEST_DOCKER_LOG="$test_root/docker.log" \
 VMBOX_INSTALL_TEST_BUILD_EXIT=1 \
 "$repo/install.sh" --no-shell-update --standalone >"$test_root/failed-build.out" 2>&1; then
 echo 'Failed standalone build unexpectedly succeeded' >&2; exit 1
fi
cmp "$test_root/previous-cli" "$test_root/bin/vbox"
pass 'failed image build preserves the installed CLI'

if "$repo/install.sh" --host test-host >"$test_root/invalid-host.out" 2>&1; then
 echo '--host without --standalone unexpectedly succeeded' >&2; exit 1
fi
pass 'standalone-only installer options require standalone mode'

for removed_option in --go-cli --workspace-token; do
  if HOME="$test_root/home" PATH="$fixtures:$PATH" \
    VMBOX_INSTALL_DIR="$test_root/bin" VMBOX_SHELL_RC="$test_root/bashrc" \
    XDG_CONFIG_HOME="$test_root/config" XDG_DATA_HOME="$test_root/data" \
    "$repo/install.sh" "$removed_option" >"$test_root/legacy.out" 2>&1; then
    echo "removed installer option $removed_option unexpectedly succeeded" >&2
    exit 1
  fi
  grep -Fq "Unknown option: $removed_option" "$test_root/legacy.out"
done
pass 'legacy shell installer modes are unavailable'

HOME="$test_root/home" PATH="$fixtures:$PATH" \
  VMBOX_INSTALL_DIR="$test_root/bin" VMBOX_SHELL_RC="$test_root/bashrc" \
  XDG_CONFIG_HOME="$test_root/config" XDG_DATA_HOME="$test_root/data" \
  "$repo/install.sh" --uninstall >/dev/null
test ! -e "$test_root/bin/vbox"
test ! -L "$test_root/bin/vmbox"
test ! -e "$test_root/bin/vmbox"
test ! -e "$test_root/data/vmbox/runtime/vmbox-runtime-linux-amd64"
pass 'uninstall removes Go CLI artifacts'

printf '1..%d\n' "$pass_count"
