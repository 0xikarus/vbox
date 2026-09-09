#!/usr/bin/env bash
set -euo pipefail
test -d /data/workspace
if ! test -x /usr/lib/postgresql/15/bin/initdb || ! command -v chromium >/dev/null; then
 sudo apt-get update -qq
 sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends postgresql-15 gcc libc6-dev chromium
fi
export PATH=/data/workspace/toolchains/go/bin:/usr/lib/postgresql/15/bin:$PATH
run_once_pg_data=$(mktemp -d /data/workspace/run-once-pg.XXXXXX)
run_once_pg_socket=$(mktemp -d /tmp/run-once-pg.XXXXXX)
initdb -D "$run_once_pg_data" -A trust --no-locale >/dev/null
trap 'pg_ctl -D "$run_once_pg_data" -m fast stop >/dev/null 2>&1 || true' EXIT
pg_ctl -D "$run_once_pg_data" -l "$run_once_pg_data/server.log" -o "-h '' -k $run_once_pg_socket" start >/dev/null
createdb -h "$run_once_pg_socket" run_once_tests
export VMBOX_TEST_DATABASE_URL="postgres:///run_once_tests?host=$run_once_pg_socket"
CGO_ENABLED=1 go test -race -count=1 ./internal/controller ./internal/boxruntime
go test ./...
go vet ./...
go build ./...
npm ci --ignore-scripts
export VMBOX_CHROMIUM
VMBOX_CHROMIUM=$(command -v chromium)
npm run test:browser
