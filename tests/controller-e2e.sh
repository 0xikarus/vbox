#!/usr/bin/env bash
set -euo pipefail

repo="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
suffix="${GITHUB_RUN_ID:-$$}"
work_dir="$(mktemp -d /tmp/vmbox-controller-e2e.XXXXXX)"
pg_name="vmbox-controller-e2e-pg-$suffix"
box_name="controller-e2e-$suffix"
controller_pid=""

cleanup() {
  if [[ -n "$controller_pid" ]]; then
    kill "$controller_pid" >/dev/null 2>&1 || true
    wait "$controller_pid" >/dev/null 2>&1 || true
  fi
  docker rm -f "$pg_name" "vmbox-$box_name" >/dev/null 2>&1 || true
  docker network rm "vmbox-$box_name-net" >/dev/null 2>&1 || true
  docker volume rm "vmbox-$box_name-data" >/dev/null 2>&1 || true
  rm -rf -- "$work_dir"
}
trap cleanup EXIT INT TERM

go build -o "$work_dir/vbox" "$repo/cmd/vbox"
go build -o "$work_dir/vmbox-controller" "$repo/cmd/vmbox-controller"
image="${VMBOX_E2E_IMAGE:-vmbox:e2e}"
image_id="$(docker image inspect --format '{{.Id}}' "$image")"
db_password="$(openssl rand -hex 24)"
docker run -d --name "$pg_name" \
  -e POSTGRES_PASSWORD="$db_password" -e POSTGRES_DB=vmbox \
  -p 127.0.0.1::5432 postgres:16-alpine >/dev/null
for _ in $(seq 1 90); do
  docker exec "$pg_name" psql -U postgres -d vmbox -v ON_ERROR_STOP=1 -Atqc 'SELECT 1' >/dev/null 2>&1 && break
  sleep 1
done
docker exec "$pg_name" psql -U postgres -d vmbox -v ON_ERROR_STOP=1 -Atqc 'SELECT 1' >/dev/null
db_port="$(docker port "$pg_name" 5432/tcp | sed 's/.*://')"
database_url="postgres://postgres:${db_password}@127.0.0.1:${db_port}/vmbox?sslmode=disable"
encryption_key="$(openssl rand -base64 32 | tr -d '\n')"

DATABASE_URL="$database_url" "$work_dir/vmbox-controller" bootstrap >"$work_dir/bootstrap"
chmod 600 "$work_dir/bootstrap"
controller_token="$(sed -n 's/^VMBOX_CONTROLLER_TOKEN=//p' "$work_dir/bootstrap")"
[[ -n "$controller_token" ]]
listen_port=$((26000 + suffix % 1000))
host_ip="$(hostname -I | awk '{print $1}')"
DATABASE_URL="$database_url" VMBOX_ENCRYPTION_KEY="$encryption_key" \
  VMBOX_CONTROLLER_LISTEN=":$listen_port" \
  VMBOX_CONTROLLER_URL="http://${host_ip}:${listen_port}" \
  "$work_dir/vmbox-controller" >"$work_dir/controller.log" 2>&1 &
controller_pid=$!
for _ in $(seq 1 60); do
  curl -fsS "http://127.0.0.1:${listen_port}/healthz" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS "http://127.0.0.1:${listen_port}/healthz" >/dev/null

config_path="$work_dir/config.json"
VMBOX_CONFIG="$config_path" "$work_dir/vbox" context add e2e \
  --provider docker --docker-context default \
  --controller "http://127.0.0.1:${listen_port}" --account e2e \
  --provider-credential local --image "$image_id" >/dev/null
VMBOX_CONFIG="$config_path" VMBOX_CONTROLLER_TOKEN="$controller_token" \
  VMBOX_E2E_PROVIDER_SECRET='{"scope":"local"}' \
  "$work_dir/vbox" credentials set docker local \
  --secret-env VMBOX_E2E_PROVIDER_SECRET \
  --config "{\"context\":\"default\",\"image\":\"$image_id\"}" >/dev/null

accepted="$(VMBOX_CONFIG="$config_path" VMBOX_CONTROLLER_TOKEN="$controller_token" \
  "$work_dir/vbox" new "$box_name" --detach -- \
  printf '%s' 'controller exact argv: $HOME; $(false)')"
run_id="$(awk '/^accepted / {print $2}' <<<"$accepted")"
[[ -n "$run_id" ]]
state=""
for _ in $(seq 1 90); do
  status="$(VMBOX_CONFIG="$config_path" VMBOX_CONTROLLER_TOKEN="$controller_token" \
    "$work_dir/vbox" status "$run_id" 2>/dev/null)"
  state="$(jq -r .state <<<"$status")"
  [[ "$state" == succeeded || "$state" == failed ]] && break
  sleep 1
done
if [[ "$state" != succeeded ]]; then
  tail -n 80 "$work_dir/controller.log" >&2
  exit 1
fi
[[ "$(jq -r .lastActivity <<<"$status")" == 'controller exact argv: $HOME; $(false)' ]]
VMBOX_CONFIG="$config_path" VMBOX_CONTROLLER_TOKEN="$controller_token" \
  "$work_dir/vbox" clean "$run_id" --yes >/dev/null
! docker container inspect "vmbox-$box_name" >/dev/null 2>&1
echo 'controller Docker/PostgreSQL E2E passed'
