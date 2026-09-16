#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."
go_binary="${VMBOX_TEST_GO:-go}"
image="${VMBOX_TEST_SHARED_IMAGE:-vmbox-agent-desktop-mvp:disposable}"
artifacts="$(mktemp -d /tmp/vmbox-shared-disposable.XXXXXX)"
name="vmbox-shared-disposable-$(date +%s)-$$"
database="$name-pg"
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker rm -f "$database" >/dev/null 2>&1 || true
  rm -rf "$artifacts"
}
trap cleanup EXIT
"$go_binary" build -buildvcs=false -o "$artifacts/worker" ./cmd/vmbox-shared-worker
"$go_binary" build -buildvcs=false -o "$artifacts/runtime" ./cmd/vmbox-runtime
docker run -d --rm --user 0 --name "$name" \
  --entrypoint /usr/local/bin/vmbox-shared-worker \
  -e VMBOX_SHARED_SLOTS=2 \
  -e VMBOX_SHARED_ACCOUNT_ID=00000000-0000-4000-8000-000000000001 \
  -e VMBOX_SHARED_TOKEN=disposable-test-token-not-for-production \
  -v "$artifacts/worker:/usr/local/bin/vmbox-shared-worker:ro" \
  -v "$artifacts/runtime:/usr/local/bin/vmbox-runtime:ro" \
  -p 127.0.0.1::8080 "$image" >/dev/null
port="$(docker port "$name" 8080/tcp | cut -d: -f2)"
for attempt in $(seq 1 30); do
  if curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null; then break; fi
  sleep 1
done
VMBOX_TEST_SHARED_DISPOSABLE_ENDPOINT="http://127.0.0.1:$port" \
VMBOX_TEST_SHARED_TOKEN=disposable-test-token-not-for-production \
  "$go_binary" test ./internal/provider/shared -run TestDisposableTwoSlotWorker -v -count=1
docker run -d --rm --name "$database" -e POSTGRES_HOST_AUTH_METHOD=trust -p 127.0.0.1::5432 postgres:17-alpine >/dev/null
for attempt in $(seq 1 30); do
  if docker exec "$database" pg_isready -U postgres >/dev/null; then break; fi
  sleep 1
done
database_port="$(docker port "$database" 5432/tcp | cut -d: -f2)"
VMBOX_TEST_DATABASE_URL="postgres://postgres@127.0.0.1:$database_port/postgres?sslmode=disable" \
VMBOX_TEST_SHARED_DISPOSABLE_ENDPOINT="http://127.0.0.1:$port" \
VMBOX_TEST_SHARED_TOKEN=disposable-test-token-not-for-production \
VMBOX_TEST_SHARED_RUNTIME="$artifacts/runtime" \
  "$go_binary" test ./internal/controller -run TestSharedWorkerControllerPostgres -v -count=1
