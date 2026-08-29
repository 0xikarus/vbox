#!/usr/bin/env bash

set -euo pipefail

config_file="${VMBOX_CONFIG:-${XDG_CONFIG_HOME:-$HOME/.config}/vmbox/config}"
credentials_file="${VMBOX_CREDENTIALS:-$(dirname -- "$config_file")/credentials}"

[[ -f "$config_file" ]] && . "$config_file"
if [[ -z "${RAILWAY_API_TOKEN:-}" && -z "${RAILWAY_TOKEN:-}" && -f "$credentials_file" ]]; then
  . "$credentials_file"
fi

: "${VMBOX_PROJECT_ID:=c9671604-0a68-47ee-abe8-16c72922d391}"
: "${VMBOX_ENVIRONMENT_ID:=be38d867-15fd-4174-af7d-89b54c330daa}"
: "${VMBOX_SERVICE_PREFIX:=vmbox-}"
: "${VMBOX_DEPLOY_TIMEOUT:=900}"

bundle="${VMBOX_BUNDLE_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/vmbox/service}"
target=(--project "$VMBOX_PROJECT_ID" --environment "$VMBOX_ENVIRONMENT_ID")

usage() {
  cat <<'EOF'
Usage:
  vmbox help
  vmbox list
  vmbox ls
  vmbox start <box-id>
  vmbox resume <box-id>
  vmbox clean [--yes]

Each box is a Railway service named vmbox-<box-id>. `start` creates, deploys,
and attaches to the service when it does not exist; otherwise it attaches to
the existing box. `clean` deletes every service in the configured project and
environment after confirmation.
EOF
}

die() { echo "vmbox: $*" >&2; exit 1; }
require() { command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"; }

validate_box_id() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] ||
    die "invalid box ID '$1'; use letters, numbers, dots, dashes, or underscores"
}

services() { railway service list "${target[@]}" --json; }

find_service() {
  services | jq -c --arg name "$service_name" \
    'first(.[] | select(.name == $name)) // empty'
}

link_service() {
  local args=("${target[@]}")
  [[ -n "${1:-}" ]] && args+=(--service "$1")
  (cd "$bundle" && railway link "${args[@]}" --json >/dev/null)
}

create_service() {
  [[ -f "$bundle/Dockerfile" ]] || die "deployment bundle missing; rerun install.sh"
  echo "vmbox: provisioning Railway service '$service_name'" >&2
  link_service ""
  (cd "$bundle" && railway add --service "$service_name" --json >/dev/null)
}

wait_for_service() {
  local deployment_id="${1:-}" deadline=$((SECONDS + VMBOX_DEPLOY_TIMEOUT)) data status
  while ((SECONDS < deadline)); do
    data="$(cd "$bundle" && railway deployment list \
      --service "$service_name" --environment "$VMBOX_ENVIRONMENT_ID" \
      --limit 20 --json)"
    status="$(jq -r --arg id "$deployment_id" '
      if $id == "" then .[0].status // empty
      else first(.[] | select(.id == $id) | .status) // empty end
    ' <<<"$data")"
    case "$status" in
      SUCCESS) echo "vmbox: '$service_name' is ready" >&2; return ;;
      FAILED|CRASHED|REMOVED) die "deployment for '$service_name' ended with $status" ;;
    esac
    echo "vmbox: waiting for '$service_name' (${status:-queued})" >&2
    sleep 5
  done
  die "deployment timed out after ${VMBOX_DEPLOY_TIMEOUT}s"
}

ensure_ready() {
  local service="$1" status deploy_result deployment_id volume_added=0
  link_service "$service_name"

  if ! jq -e 'any(.volumes[]?; .mountPath == "/data")' <<<"$service" >/dev/null; then
    echo "vmbox: attaching persistent /data volume" >&2
    (cd "$bundle" && railway volume add --mount-path /data --json >/dev/null)
    volume_added=1
  fi

  status="$(jq -r '.status // empty' <<<"$service")"
  case "$volume_added:$status" in
    0:SUCCESS) return ;;
    0:BUILDING|0:DEPLOYING|0:QUEUED|0:INITIALIZING|0:WAITING) wait_for_service; return ;;
  esac

  echo "vmbox: deploying '$service_name'" >&2
  deploy_result="$(railway up "$bundle" --path-as-root --detach --json \
    --service "$service_name" "${target[@]}")"
  deployment_id="$(jq -rs 'map(select(type == "object")) | last | .deploymentId // .id // empty' \
    <<<"$deploy_result")"
  wait_for_service "$deployment_id"
}

attach() {
  railway ssh "${target[@]}" --service "$service_name" --session "$box_id"
}

display_list() {
  jq -r --arg prefix "$VMBOX_SERVICE_PREFIX" '
    ["NAME", "STATUS", "ID", "RESUME"],
    (.[] |
      (.name | if startswith($prefix) then .[($prefix | length):] else . end) as $box |
      [.name, (.status // "NO_DEPLOYMENT"), .id, "vmbox resume \($box)"]
    ) | @tsv
  ' <<<"$1"
}

open_box() {
  local requested_action="$1" service
  box_id="$2"
  validate_box_id "$box_id"
  service_name="$VMBOX_SERVICE_PREFIX$box_id"
  service="$(find_service)"

  if [[ -z "$service" && "$requested_action" == resume ]]; then
    service_name="$box_id"
    service="$(find_service)"
  fi

  if [[ -z "$service" ]]; then
    [[ "$requested_action" == start ]] || die "box '$box_id' does not exist; use: vmbox start $box_id"
    create_service
    for _ in {1..10}; do
      service="$(find_service)"
      [[ -n "$service" ]] && break
      sleep 1
    done
    [[ -n "$service" ]] || die "created '$service_name' but could not discover it"
  fi

  ensure_ready "$service"
  attach
}

select_box() {
  local list="$1" selected=0 key rest row name status box i
  local -a rows
  mapfile -t rows < <(jq -r --arg prefix "$VMBOX_SERVICE_PREFIX" '
    .[] |
    (.name | if startswith($prefix) then .[($prefix | length):] else . end) as $box |
    [.name, (.status // "NO_DEPLOYMENT"), $box] | @tsv
  ' <<<"$list")

  if [[ ! -t 0 || ! -t 1 ]]; then
    display_list "$list"
    return
  fi

  while true; do
    printf '\033[2J\033[HSelect a box to resume  ↑/↓ or j/k: move  Enter: resume  q: quit\n\n'
    for i in "${!rows[@]}"; do
      IFS=$'\t' read -r name status box <<<"${rows[$i]}"
      if ((i == selected)); then
        printf '\033[1;36m> %-28s %-14s vmbox resume %s\033[0m\n' "$name" "$status" "$box"
      else
        printf '  %-28s %-14s vmbox resume %s\n' "$name" "$status" "$box"
      fi
    done

    IFS= read -rsn1 key || return
    case "$key" in
      q) printf '\n'; return ;;
      j) selected=$(((selected + 1) % ${#rows[@]})) ;;
      k) selected=$(((selected - 1 + ${#rows[@]}) % ${#rows[@]})) ;;
      '')
        IFS=$'\t' read -r name status box <<<"${rows[$selected]}"
        printf '\033[2J\033[H'
        open_box resume "$box"
        return
        ;;
      $'\e')
        rest=""
        IFS= read -rsn2 -t 0.1 rest || true
        case "$rest" in
          '[A') selected=$(((selected - 1 + ${#rows[@]}) % ${#rows[@]})) ;;
          '[B') selected=$(((selected + 1) % ${#rows[@]})) ;;
          *) printf '\n'; return ;;
        esac
        ;;
    esac
  done
}

if (($# == 0)); then
  usage
  exit
fi

action="$1"
if [[ "$action" == -h || "$action" == --help || "$action" == help ]]; then
  usage
  exit
fi

require railway
require jq

case "$action" in
  ls|list)
    [[ $# -eq 1 ]] || die "usage: vmbox $action"
    list="$(services)"
    if [[ "$(jq 'length' <<<"$list")" == 0 ]]; then
      echo "No boxes."
    elif [[ "$action" == list ]]; then
      select_box "$list"
    else
      display_list "$list"
    fi
    ;;

  start|resume)
    [[ $# -eq 2 ]] || die "usage: vmbox $action <box-id>"
    open_box "$action" "$2"
    ;;

  clean)
    [[ $# -eq 1 || ($# -eq 2 && "${2:-}" == --yes) ]] || die "usage: vmbox clean [--yes]"
    list="$(services)"
    count="$(jq 'length' <<<"$list")"
    if [[ "$count" == 0 ]]; then
      echo "No services to delete."
      exit
    fi

    jq -r '.[] | "  \(.name) (\(.id))"' <<<"$list" >&2
    if [[ "${2:-}" != --yes ]]; then
      [[ -t 0 ]] || die "confirmation required; use: vmbox clean --yes"
      read -rp "Delete all $count services from project $VMBOX_PROJECT_ID? Type 'clean': " answer
      [[ "$answer" == clean ]] || die "cancelled"
    fi

    while IFS= read -r service_id; do
      railway service delete "${target[@]}" --service "$service_id" --yes --json >/dev/null
    done < <(jq -r '.[].id' <<<"$list")
    echo "Deleted $count services."
    ;;

  *) usage >&2; exit 2 ;;
esac
