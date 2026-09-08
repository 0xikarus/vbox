#!/usr/bin/env bash
# Private PostgreSQL bootstrap follows scripts/test-factory-in-box.sh.
set -euo pipefail
test -d /data/workspace
if ! test -x /usr/lib/postgresql/15/bin/initdb; then
  sudo apt-get update -qq
  sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends postgresql-15
fi
export PATH=/data/workspace/toolchains/go/bin:/usr/lib/postgresql/15/bin:$PATH
taskflow_pg_data=$(mktemp -d /data/workspace/taskflow-pg-test.XXXXXX)
taskflow_pg_socket=$(mktemp -d /tmp/taskflow-pg-socket.XXXXXX)
initdb -D "$taskflow_pg_data" -A trust --no-locale >/dev/null
trap 'pg_ctl -D "$taskflow_pg_data" -m fast stop >/dev/null 2>&1 || true' EXIT
pg_ctl -D "$taskflow_pg_data" -l "$taskflow_pg_data/server.log" -o "-h '' -k $taskflow_pg_socket" start >/dev/null
createdb -h "$taskflow_pg_socket" taskflow_tests
export VMBOX_FACTORY_TEST_DATABASE_URL="postgres:///taskflow_tests?host=$taskflow_pg_socket"
go test -race -count=1 -v ./internal/taskflow
go test -count=1 ./...
go vet ./...
go build ./...
printf 'Taskflow private PostgreSQL verification passed: %s\n' "$taskflow_pg_data"
