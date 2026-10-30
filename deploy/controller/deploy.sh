#!/usr/bin/env bash
# Run on the controller host. Builds are capped; workers are never restarted.
set -euo pipefail
main() {
 local dir=/opt/vbox-controller src=/opt/vbox-controller/src target previous
 exec 9>"$dir/deploy.lock"
 flock -n 9 || return 0
 git -C "$src" fetch -q origin main
 target=$(git -C "$src" rev-parse origin/main)
 previous=$(cat "$dir/deployed" 2>/dev/null || true)
 if [[ "$target" == "$previous" && "${1:-}" != --force ]]; then return 0; fi
 git -C "$src" reset -q --hard "$target"
 # This host uses Docker's legacy builder: its per-build cgroup flags prevent
 # a Go/npm build from consuming the tracker and market overview's resources.
 if ! DOCKER_BUILDKIT=0 docker build --force-rm --cpu-period 100000 --cpu-quota 50000 \
   --memory 1024m --memory-swap 1024m --build-arg TARGETOS=linux --build-arg TARGETARCH=amd64 \
   -f "$src/Dockerfile.controller" -t "vbox-controller:$target" "$src"; then
  [[ -z "$previous" ]] || git -C "$src" reset -q --hard "$previous"
  return 1
 fi
 "$src/deploy/controller/backup.sh"
 local compose=(docker compose -p vbox-controller -f "$src/deploy/controller/compose.yml")
 if DEPLOY_TAG=$target "${compose[@]}" up -d --no-deps controller && wait_healthy; then
  printf '%s\n' "$target" >"$dir/deployed"
  echo "Controller deployed: $target"
  return 0
 fi
 echo 'Controller health check failed; restoring the previous application image.' >&2
 if [[ -n "$previous" ]]; then
  git -C "$src" reset -q --hard "$previous"
  DEPLOY_TAG=$previous "${compose[@]}" up -d --no-deps controller
 fi
 return 1
}
wait_healthy() {
 local attempt
 for attempt in {1..60}; do
  if curl --fail --silent --max-time 3 http://127.0.0.1:43120/healthz >/dev/null; then return 0; fi
  sleep 2
 done
 return 1
}
main "$@"
