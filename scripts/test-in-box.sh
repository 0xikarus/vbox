#!/usr/bin/env bash
set -euo pipefail
test -d /data/workspace
if ! test -x /usr/lib/postgresql/15/bin/initdb || ! command -v chromium >/dev/null; then
 sudo apt-get update -qq
 sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends postgresql-15 gcc libc6-dev chromium
fi
# Flush package-install writes before timing runtime filesystem-flush tests.
timeout 60 sync
export PATH=/data/workspace/toolchains/go/bin:/usr/lib/postgresql/15/bin:$PATH
pg_data=$(mktemp -d /data/workspace/vmbox-pg.XXXXXX)
pg_socket=$(mktemp -d /tmp/vmbox-pg.XXXXXX)
initdb -D "$pg_data" -A trust --no-locale >/dev/null
trap 'pg_ctl -D "$pg_data" -m fast stop >/dev/null 2>&1 || true' EXIT
pg_ctl -D "$pg_data" -l "$pg_data/server.log" -o "-h '' -k $pg_socket" start >/dev/null
createdb -h "$pg_socket" vmbox_tests
export VMBOX_TEST_DATABASE_URL="postgres:///vmbox_tests?host=$pg_socket"
CGO_ENABLED=1 go test -race -count=1 ./internal/controller
CGO_ENABLED=1 go test -race -count=1 ./internal/boxruntime
go test ./...
go vet ./...
go build ./...
npm ci --ignore-scripts
export VMBOX_CHROMIUM
VMBOX_CHROMIUM=$(command -v chromium)
npm run test:browser
