#!/usr/bin/env bash
# Private, consistent PostgreSQL backups. No environment values in logs/argv.
set -euo pipefail
umask 077
dir=/opt/vbox-controller/backups
mkdir -p "$dir"
exec 8>"$dir/backup.lock"
flock -n 8 || exit 0
file="$dir/controller-$(date -u +%Y%m%dT%H%M%SZ).dump"
trap 'rm -f "${file}.partial"' EXIT
docker exec vbox-controller-db-1 pg_dump -U vbox -d vbox -Fc --no-owner --no-acl >"${file}.partial"
# Parsing the TOC catches a truncated/invalid archive before replacing a backup.
docker exec -i vbox-controller-db-1 pg_restore --list <"${file}.partial" >/dev/null
mv "${file}.partial" "$file"
sha256sum "$file" >"${file}.sha256"
find "$dir" -maxdepth 1 -type f \( -name 'controller-*.dump' -o -name 'controller-*.dump.sha256' \) -mtime +7 -delete
echo 'Controller database backup completed.'
