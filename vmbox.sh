#!/usr/bin/env bash

set -euo pipefail

config_file="${VMBOX_CONFIG:-${XDG_CONFIG_HOME:-$HOME/.config}/vmbox/config}"
credentials_file="${VMBOX_CREDENTIALS:-$(dirname -- "$config_file")/credentials}"
state_dir="${VMBOX_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/vmbox/boxes}"

if [[ -f "$config_file" ]]; then
  # shellcheck source=/dev/null
  . "$config_file"
fi

if [[ -z "${RAILWAY_API_TOKEN:-}" && -z "${RAILWAY_TOKEN:-}" && -f "$credentials_file" ]]; then
  # shellcheck source=/dev/null
  . "$credentials_file"
fi

: "${VMBOX_PROJECT_ID:=c9671604-0a68-47ee-abe8-16c72922d391}"
: "${VMBOX_SERVICE_ID:=24746a92-81e4-4479-924b-b8f3c9986f98}"
: "${VMBOX_ENVIRONMENT_ID:=be38d867-15fd-4174-af7d-89b54c330daa}"
: "${VMBOX_WORKSPACE_ROOT:=/data/workspace}"

usage() {
  cat <<'EOF'
Usage:
  vmbox ls
  vmbox start <box-id> [remote-workspace]
  vmbox resume <box-id>

A box is a named tmux session inside a Railway service. `start` records the
stable Railway project/service/environment IDs and workspace locally. `resume`
uses that record and recreates the tmux session at the same workspace if a
container replacement removed the process state.
EOF
}

die() {
  echo "vmbox: $*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

validate_box_id() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] ||
    die "invalid box ID '$1'; use letters, numbers, dots, dashes, or underscores"
}

validate_workspace() {
  [[ "$1" =~ ^/data/workspace(/[A-Za-z0-9._-]+)*$ ]] ||
    die "workspace must be /data/workspace or a child directory"
}

set_target() {
  target=(
    --project "$project_id"
    --service "$service_id"
    --environment "$environment_id"
  )
}

load_defaults() {
  project_id="$VMBOX_PROJECT_ID"
  service_id="$VMBOX_SERVICE_ID"
  environment_id="$VMBOX_ENVIRONMENT_ID"
}

load_box() {
  local record="$state_dir/$1"
  [[ -f "$record" ]] || die "unknown box '$1'; use: vmbox start $1"
  IFS=$'\t' read -r project_id service_id environment_id workspace < "$record"
  [[ -n "$project_id" && -n "$service_id" && -n "$environment_id" && -n "$workspace" ]] ||
    die "invalid box record: $record"
  validate_workspace "$workspace"
}

box_exists() {
  railway ssh "${target[@]}" \
    "tmux has-session -t '=$box_id'" >/dev/null 2>&1
}

attach_box() {
  railway ssh "${target[@]}" --session "$box_id"
}

action="${1:-ls}"
box_id="${2:-}"
workspace=""
project_id=""
service_id=""
environment_id=""
declare -a target

require_command railway

case "$action" in
  ls)
    [[ $# -eq 1 ]] || die "usage: vmbox ls"
    load_defaults
    set_target
    output="$(railway ssh "${target[@]}" \
      'tmux list-sessions -F "#{session_name} windows=#{session_windows} attached=#{session_attached}" 2>/dev/null || true')"
    if [[ -n "$output" ]]; then
      printf '%s\n' "$output"
    else
      echo "No running boxes."
    fi
    ;;

  start)
    [[ $# -ge 2 && $# -le 3 ]] || die "usage: vmbox start <box-id> [remote-workspace]"
    validate_box_id "$box_id"
    load_defaults
    workspace="${3:-$VMBOX_WORKSPACE_ROOT/$box_id}"
    validate_workspace "$workspace"
    set_target

    box_exists && die "box '$box_id' already exists; use: vmbox resume $box_id"

    railway ssh "${target[@]}" \
      "mkdir -p '$workspace' && tmux new-session -d -s '$box_id' -c '$workspace'"

    mkdir -p "$state_dir"
    printf '%s\t%s\t%s\t%s\n' \
      "$project_id" "$service_id" "$environment_id" "$workspace" > "$state_dir/$box_id"
    chmod 600 "$state_dir/$box_id"

    attach_box
    ;;

  resume)
    [[ $# -eq 2 ]] || die "usage: vmbox resume <box-id>"
    validate_box_id "$box_id"
    load_box "$box_id"
    set_target

    if ! box_exists; then
      echo "vmbox: recreating '$box_id' at $workspace (previous process state is unavailable)" >&2
      railway ssh "${target[@]}" \
        "mkdir -p '$workspace' && tmux new-session -d -s '$box_id' -c '$workspace'"
    fi

    attach_box
    ;;

  -h|--help|help)
    usage
    ;;

  *)
    usage >&2
    exit 2
    ;;
esac
