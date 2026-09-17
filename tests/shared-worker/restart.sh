#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
artifacts="$(mktemp -d /tmp/vmbox-shared-restart-disposable.XXXXXX)"
name="vmbox-shared-restart-disposable-$$"
volume="$name-data"
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; docker volume rm "$volume" >/dev/null 2>&1 || true; rm -rf "$artifacts"; }
trap cleanup EXIT
"${VMBOX_TEST_GO:-go}" build -buildvcs=false -o "$artifacts/worker" ./cmd/vmbox-shared-worker
docker volume create "$volume" >/dev/null
start() {
 docker run -d --name "$name" --user 0 --entrypoint /usr/local/bin/vmbox-shared-worker \
  -e VMBOX_SHARED_SLOTS=2 -e VMBOX_SHARED_ACCOUNT_ID=disposable-account \
  -e VMBOX_SHARED_TOKEN=disposable-restart-test-token-123456 \
  -v "$volume:/data" -v "$artifacts/worker:/usr/local/bin/vmbox-shared-worker:ro" \
  -p 127.0.0.1::8080 "${VMBOX_TEST_SHARED_IMAGE:-vmbox-shared-blender:disposable}" >/dev/null
 port="$(docker port "$name" 8080/tcp | cut -d: -f2)"
 for attempt in $(seq 1 30); do
  if curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null; then return; fi
  sleep 1
 done
 return 1
}
start
curl -fsS "http://127.0.0.1:$port/v1/rpc" -H 'Authorization: Bearer disposable-restart-test-token-123456' \
 -H 'Content-Type: application/json' -d '{"operation":"create","create":{"name":"disposable-slot","owner":{"accountId":"disposable-account","boxId":"disposable-slot"}}}' >/dev/null
curl -fsS "http://127.0.0.1:$port/v1/rpc" -H 'Authorization: Bearer disposable-restart-test-token-123456' \
 -H 'Content-Type: application/json' -d '{"operation":"create-storage","id":"disposable-slot","owner":{"accountId":"disposable-account","boxId":"disposable-box"},"resources":{"diskGiB":1}}' > "$artifacts/result.json"
workspace="$(node -e 'const result=require(process.argv[1]);if(result.error||!result.storage)throw Error("create failed");process.stdout.write(result.storage.id)' "$artifacts/result.json")"
docker exec --user 30000 "$name" sh -c "printf retained > /data/workspaces/$workspace/home/restart-proof"
docker rm -f "$name" >/dev/null
start
docker exec "$name" getent passwd 30000
docker exec "$name" setpriv --reuid=30000 --regid=30000 --clear-groups sh -c "test \"\$(cat /data/workspaces/$workspace/home/restart-proof)\" = retained"
echo 'PASS: fresh container restored the original workspace account and retained files before healthy'
