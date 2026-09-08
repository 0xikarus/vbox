# Static program only: every per-attempt value arrives on stdin.
set -euo pipefail
umask 077
exec >/dev/null 2>/dev/null
export LC_ALL=C
root=/data/workspace/.vmbox-tasks
work=
cleanup() {
 # Work is never published until complete; a killed process may leave private
 # scratch, which is intentionally not traversed or reused on the next call.
 if [[ -n "$work" ]]; then rm -rf -- "$work"; fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
fail() { exit 1; }
# Reject links at every existing ancestor and writable-by-other principals.
# The remote uid (and root) are the trusted filesystem boundary.
safe_dir() {
 local p=$1 owner mode
 [[ -d "$p" && ! -L "$p" ]] || fail
 owner=$(stat -c %u -- "$p"); mode=$(stat -c %a -- "$p")
 [[ "$owner" == 0 || "$owner" == "$UID" ]] || fail
 (( (8#$mode & 0022) == 0 )) || fail
}
private_dir() {
 local p=$1
 if [[ ! -e "$p" && ! -L "$p" ]]; then mkdir -m 700 -- "$p"; fi
 require_private_dir "$p"
}
require_private_dir() {
 local p=$1
 safe_dir "$p"
 [[ $(stat -c %u -- "$p") == "$UID" && $(stat -c %a -- "$p") == 700 ]] || fail
}
regular() {
 [[ -f "$1" && ! -L "$1" && $(stat -c %h -- "$1") == 1 && $(stat -c %u -- "$1") == "$UID" ]] || fail
}
verify_file() {
 regular "$1"
 [[ $(stat -c %a -- "$1") == "$3" && $(sha256sum -- "$1" | cut -d' ' -f1) == "$2" ]] || fail
}
for p in / /data /data/workspace; do safe_dir "$p"; done
# mkdir can race on first use; validate the winner.
mkdir -m 700 -- "$root" 2>/dev/null || :
private_dir "$root"
cd -- "$root"
if [[ ! -e .staging.lock && ! -L .staging.lock ]]; then (set -C; : > .staging.lock) 2>/dev/null || :; fi
regular .staging.lock
[[ $(stat -c %a .staging.lock) == 600 ]] || fail
exec 9<>.staging.lock
flock -x -w 120 9
for p in bin attempts reservations scratch; do private_dir "$p"; done
work=$(mktemp -d "$root/scratch/stage.XXXXXXXXXXXX")
read -r digest
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || fail
read -r attempt; [[ "$attempt" =~ ^[0-9a-f]{32}$ ]] || fail
stage_binary="bin/vmbox-task-runner"
read -r binsize binhash
read -r jobsize jobhash
read -r count
[[ "$binsize" =~ ^[1-9][0-9]{0,8}$ && "$jobsize" =~ ^[1-9][0-9]{0,5}$ && "$count" =~ ^(0|[1-9][0-9]{0,3})$ ]] || fail
(( binsize <= 134217728 && jobsize <= 200000 && count <= 8 )) || fail
[[ "$binhash" =~ ^[0-9a-f]{64}$ && "$jobhash" =~ ^[0-9a-f]{64}$ ]] || fail
printf '%s\n' "$attempt" "$binsize $binhash" "$jobsize $jobhash" "$count" > "$work/manifest"
ids=(); sizes=(); hashes=(); total=0; previous=
for ((i=0;i<count;i++)); do
 read -r id size hash
 [[ "$id" =~ ^[0-9a-f]{32}$ && "$size" =~ ^(0|[1-9][0-9]{0,7})$ && "$hash" =~ ^[0-9a-f]{64}$ ]] || fail
 [[ -z "$previous" || "$id" > "$previous" ]] || fail
 (( size <= 10485760 )) || fail
 total=$((total+size)); ((total<=41943040)) || fail
 ids+=("$id"); sizes+=("$size"); hashes+=("$hash"); previous=$id
 printf '%s\n' "$id $size $hash" >> "$work/manifest"
done
[[ $(sha256sum "$work/manifest" | cut -d' ' -f1) == "$digest" ]] || fail
if [[ -e reservations/$attempt || -L reservations/$attempt ]]; then
 regular "reservations/$attempt"
 [[ $(stat -c %a "reservations/$attempt") == 600 ]] || fail
 [[ $(cat "reservations/$attempt") == "$digest" ]] || fail
else
 printf '%s\n' "$digest" > "$work/reservation"
 mv -T -- "$work/reservation" "reservations/$attempt"
 sync -f reservations
fi
if [[ -e attempts/$attempt || -L attempts/$attempt ]]; then
 require_private_dir "attempts/$attempt"
 regular "attempts/$attempt/ready"
 [[ $(stat -c %a "attempts/$attempt/ready") == 600 ]] || fail
 [[ $(cat "attempts/$attempt/ready") == "$digest" ]] || fail
 verify_file "attempts/$attempt/manifest" "$digest" 600
 verify_file "$stage_binary" "$binhash" 700
 verify_file "attempts/$attempt/job.json" "$jobhash" 600
 require_private_dir "attempts/$attempt/images"
 require_private_dir "attempts/$attempt/workspace"
 for ((i=0;i<count;i++)); do verify_file "attempts/$attempt/images/${ids[i]}" "${hashes[i]}" 600; done
 # Consume the bounded stream so SSH can complete cleanly on an idempotent retry.
 cat > /dev/null
 exit 0
fi
receive() {
 local path=$1 size=$2 hash=$3
 (set -C; head -c "$size" > "$path")
 [[ $(stat -c %s -- "$path") == "$size" && $(sha256sum -- "$path" | cut -d' ' -f1) == "$hash" ]] || fail
}
receive "$work/binary" "$binsize" "$binhash"
mkdir -m 700 "$work/attempt" "$work/attempt/images" "$work/attempt/workspace"
receive "$work/attempt/job.json" "$jobsize" "$jobhash"
for ((i=0;i<count;i++)); do receive "$work/attempt/images/${ids[i]}" "${sizes[i]}" "${hashes[i]}"; done
# Reject trailing data before making any ready attempt visible.
[[ $(head -c 1 | wc -c) == 0 ]] || fail
if [[ -e "$stage_binary" || -L "$stage_binary" ]]; then
 regular "$stage_binary"
 [[ $(stat -c %a "$stage_binary") == 700 && $(sha256sum "$stage_binary" | cut -d' ' -f1) == "$binhash" ]] || fail
fi
# Publish the binary create-only; never replace an executable held by an agent.
if [[ ! -e "$stage_binary" ]]; then
 chmod 700 "$work/binary"
 mv -T "$work/binary" "$stage_binary"
fi
mv "$work/manifest" "$work/attempt/manifest"
printf '%s\n' "$digest" > "$work/attempt/ready"
# Rename publishes all private files and the ready marker together, last.
sync -f "$work/attempt"
mv -T "$work/attempt" "attempts/$attempt"
sync -f attempts
