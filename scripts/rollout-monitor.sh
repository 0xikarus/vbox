#!/usr/bin/env bash
set -euo pipefail

base_url="${VMBOX_MONITOR_URL:-https://controller.example.com}"
token="${VMBOX_MONITOR_TOKEN:?set VMBOX_MONITOR_TOKEN}"
provider="${VMBOX_MONITOR_PROVIDER:-railway}"
credential="${VMBOX_MONITOR_CREDENTIAL:-primary}"
iterations="${VMBOX_MONITOR_ITERATIONS:-12}"
interval="${VMBOX_MONITOR_INTERVAL:-5}"
previous=""

request() {
  local path="$1"
  curl --silent --show-error --max-time 12 \
    -H "Authorization: Bearer $token" \
    -w $'\n%{http_code}' \
    "$base_url$path" 2>&1 || true
}

for ((attempt = 1; attempt <= iterations; attempt++)); do
  now="$(date -u +%FT%TZ)"
  health="$(curl --silent --show-error --max-time 8 -w $'\n%{http_code}' "$base_url/healthz" 2>&1 || true)"
  query="provider=$provider&providerCredential=$credential"
  inventory="$(request "/v1/inventory?$query")"
  fleet="$(request "/v1/fleet/status?$query")"
  current="health=$health
inventory=$inventory
fleet=$fleet"

  if [[ "$current" != "$previous" ]]; then
    printf '[%s] rollout state changed\n%s\n' "$now" "$current"
    previous="$current"
  fi

  if [[ "$health" == *'"status":"ok"'* ]] && \
     [[ "$health" == *$'\n200' ]] && \
     [[ "$inventory" == *$'\n200' ]] && \
     [[ "$fleet" == *$'\n200' ]] && \
     [[ "$inventory" != *'"state":"attaching"'* ]] && \
     [[ "$fleet" != *'"state":"draining"'* ]] && \
     [[ "$fleet" != *'"health":"unhealthy"'* ]]; then
    printf '[%s] rollout reached a stable state\n' "$now"
    exit 0
  fi

  (( attempt == iterations )) || sleep "$interval"
done

printf 'rollout did not stabilize within %ss\n' "$((iterations * interval))" >&2
exit 1
