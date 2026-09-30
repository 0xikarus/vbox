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
