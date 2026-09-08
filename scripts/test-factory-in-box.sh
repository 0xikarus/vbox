#!/usr/bin/env bash
# Run on a disposable vmbox, never on the developer laptop.
set -euo pipefail
test -d /data/workspace || { echo 'Run this verification inside a vmbox.' >&2; exit 1; }
export DEBIAN_FRONTEND=noninteractive
if ! test -x /usr/lib/postgresql/15/bin/initdb; then
  sudo apt-get update -qq
  sudo apt-get install -y -qq --no-install-recommends postgresql-15 curl jq ca-certificates
fi
export PATH=/data/workspace/toolchains/go/bin:/usr/lib/postgresql/15/bin:$PATH
if ! command -v go >/dev/null || ! go version | grep -q 'go1.26'; then
  mkdir -p /data/workspace/toolchains
  curl -fsSL https://go.dev/dl/go1.26.0.linux-amd64.tar.gz -o /data/workspace/toolchains/go1.26.0.tar.gz
  factory_go_sha=$(curl -fsSL 'https://go.dev/dl/?mode=json&include=all' | jq -r '.[] | .files[] | select(.filename=="go1.26.0.linux-amd64.tar.gz") | .sha256')
  test -n "$factory_go_sha"
  (cd /data/workspace/toolchains; printf '%s  go1.26.0.tar.gz\n' "$factory_go_sha" | sha256sum -c -; tar -xzf go1.26.0.tar.gz)
fi
# Each execution owns a private cluster. TCP is disabled; no production DB or
# shared PostgreSQL daemon is used. Artifacts remain for inspection after exit.
factory_pg_data=$(mktemp -d /data/workspace/factory-pg-test.XXXXXX)
factory_pg_socket=$(mktemp -d /tmp/factory-pg-socket.XXXXXX)
initdb -D "$factory_pg_data" -A trust --no-locale >/dev/null
trap 'pg_ctl -D "$factory_pg_data" -m fast stop >/dev/null 2>&1 || true' EXIT
pg_ctl -D "$factory_pg_data" -l "$factory_pg_data/server.log" -o "-h '' -k $factory_pg_socket" start >/dev/null
createdb -h "$factory_pg_socket" factory_tests
export VMBOX_FACTORY_TEST_DATABASE_URL="postgres:///factory_tests?host=$factory_pg_socket"
go version
go test -count=1 ./...
go vet ./...
printf 'Factory verification passed; isolated DB artifacts: %s\n' "$factory_pg_data"
