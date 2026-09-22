#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
go_binary="${VMBOX_TEST_GO:-go}"
image="${VMBOX_TEST_CONTAINER_IMAGE:?set a digest-pinned worker image}"
artifacts="$(mktemp -d /tmp/vmbox-container-disposable.XXXXXX)"
worker_pid=""
cleanup() {
  if [ -n "$worker_pid" ]; then kill "$worker_pid" 2>/dev/null || true; wait "$worker_pid" 2>/dev/null || true; fi
  while read -r id; do [ -z "$id" ] || docker rm -f "$id" >/dev/null; done < <(docker ps -aq --filter "label=io.vmbox.root=$artifacts/data")
  while read -r id; do [ -z "$id" ] || docker network rm "$id" >/dev/null; done < <(docker network ls -q --filter "label=io.vmbox.root=$artifacts/data")
  rm -rf "$artifacts"
}
trap cleanup EXIT
mkdir "$artifacts/data"
"$go_binary" build -buildvcs=false -o "$artifacts/worker" ./cmd/vmbox-shared-worker
VMBOX_SHARED_ROOT="$artifacts/data" VMBOX_SHARED_ISOLATION=container \
VMBOX_SHARED_CONTAINER_IMAGE="$image" VMBOX_SHARED_SLOTS=2 \
VMBOX_SHARED_ACCOUNT_ID=00000000-0000-4000-8000-000000000001 \
VMBOX_SHARED_TOKEN=disposable-container-protocol-token VMBOX_SHARED_BIND=127.0.0.1 PORT=18082 \
  "$artifacts/worker" >"$artifacts/worker.log" 2>&1 &
worker_pid=$!
ready=0
for attempt in $(seq 1 30); do
  kill -0 "$worker_pid" || { cat "$artifacts/worker.log"; exit 1; }
  if curl -fsS http://127.0.0.1:18082/healthz >/dev/null 2>&1; then ready=1; break; fi
  sleep 1
done
test "$ready" = 1
VMBOX_TEST_SHARED_DISPOSABLE_ENDPOINT=http://127.0.0.1:18082 \
VMBOX_TEST_SHARED_TOKEN=disposable-container-protocol-token \
VMBOX_TEST_SHARED_EXPECT_TIER=container \
  "$go_binary" test ./internal/provider/shared -run TestDisposableTwoSlotIsolation -v -count=1
