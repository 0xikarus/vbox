#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."
go_binary="${VMBOX_TEST_GO:-go}"
image="${VMBOX_TEST_SHARED_IMAGE:-vmbox-shared-blender:disposable}"
artifacts="$(mktemp -d /tmp/vmbox-shared-isolation.XXXXXX)"
name="vmbox-shared-isolation-$(date +%s)-$$"
token="disposable-isolation-token-not-for-production"
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  rm -rf "$artifacts"
}
trap cleanup EXIT
"$go_binary" build -buildvcs=false -o "$artifacts/worker" ./cmd/vmbox-shared-worker
"$go_binary" build -buildvcs=false -o "$artifacts/runtime" ./cmd/vmbox-runtime
worker_env=(
  -e VMBOX_SHARED_SLOTS=2
  -e VMBOX_SHARED_ACCOUNT_ID=00000000-0000-4000-8000-000000000001
  -e "VMBOX_SHARED_TOKEN=$token"
)
if [ -n "${VMBOX_SHARED_ISOLATION:-}" ]; then
  worker_env+=(-e "VMBOX_SHARED_ISOLATION=$VMBOX_SHARED_ISOLATION")
fi
docker run -d --rm --user 0 --name "$name" \
  --entrypoint /usr/local/bin/vmbox-shared-worker \
  "${worker_env[@]}" \
  -v "$artifacts/worker:/usr/local/bin/vmbox-shared-worker:ro" \
  -v "$artifacts/runtime:/usr/local/bin/vmbox-runtime:ro" \
  -p 127.0.0.1::8080 "$image" >/dev/null
port="$(docker port "$name" 8080/tcp | cut -d: -f2)"
healthy=0
for attempt in $(seq 1 30); do
  if curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null; then
    healthy=1
    break
  fi
  sleep 1
done
if [ "$healthy" != 1 ]; then
  echo "disposable shared worker did not become healthy" >&2
  docker logs "$name" >&2 || true
  exit 1
fi
VMBOX_TEST_SHARED_DISPOSABLE_ENDPOINT="http://127.0.0.1:$port" \
VMBOX_TEST_SHARED_TOKEN="$token" \
VMBOX_TEST_SHARED_EXPECT_TIER="${VMBOX_TEST_SHARED_EXPECT_TIER:-}" \
  "$go_binary" test ./internal/provider/shared -run TestDisposableTwoSlotIsolation -v -count=1
