#!/usr/bin/env bash

# Builds the bundle produced by install.sh with a real Docker daemon. The fast
# shell suite uses a deterministic build-context resolver; this test is the
# end-to-end counterpart that proves Docker can build the installed artifact.

set -euo pipefail

repo="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
docker_bin="$(command -v docker || true)"
[[ -n "$docker_bin" ]] || { echo 'docker is required for the installed-bundle test' >&2; exit 1; }

test_root="$(mktemp -d)"
tag="vmbox-installed-bundle-test:$$"
cleanup() {
  "$docker_bin" image rm --force "$tag" >/dev/null 2>&1 || true
  rm -rf -- "$test_root"
}
trap cleanup EXIT

mkdir -p "$test_root/home"
: >"$test_root/railway.log"
HOME="$test_root/home" PATH="$repo/tests/fixtures:$PATH" \
  VMBOX_TEST_STATE="$test_root" VMBOX_INSTALL_DIR="$test_root/bin" \
  VMBOX_SHELL_RC="$test_root/test.rc" XDG_CONFIG_HOME="$test_root/config" \
  XDG_DATA_HOME="$test_root/data" \
  "$repo/install.sh" --no-shell-update >/dev/null

bundle="$test_root/data/vmbox/service"
"$docker_bin" build --tag "$tag" "$bundle"
"$docker_bin" image inspect "$tag" >/dev/null
health="$($docker_bin run --rm --entrypoint /usr/local/bin/vmbox-runtime "$tag" health)"
[[ "$health" == ok ]] || { printf 'installed runtime health check returned: %s\n' "$health" >&2; exit 1; }
echo 'installed deployment bundle built successfully with Docker'
