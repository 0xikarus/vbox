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
: "${VMBOX_VOLUME_TIMEOUT:=60}"
: "${VMBOX_DEFAULT_REGION:=us-east}"

bundle="${VMBOX_BUNDLE_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/vmbox/service}"
target=(--project "$VMBOX_PROJECT_ID" --environment "$VMBOX_ENVIRONMENT_ID")

quick_usage() {
  cat <<'EOF_QUICK'
vmbox <name>                 create or connect
vmbox list                   choose a box
vmbox ls                     list boxes
vmbox cost [name]            show costs
vmbox resize [name]          change CPU/RAM limits
vmbox stop <name>            power down, keep /data
vmbox clean [name ...]       delete selected boxes and /data
vmbox help                   full command guide
EOF_QUICK
}

usage() {
  cat <<'EOF'
Usage:
  vmbox <box-id> [--detach] [-- COMMAND [ARG...]]
  vmbox new <box-id> [--detach] [-- COMMAND [ARG...]]
  vmbox help
  vmbox list
  vmbox ls
  vmbox cost [box-id]
  vmbox resize [box-id]
  vmbox auth <box-id>
  vmbox github <box-id>
  vmbox start <box-id> [--detach] [-- COMMAND [ARG...]]
  vmbox resume <box-id> [--detach] [-- COMMAND [ARG...]]
  vmbox stop <box-id>
  vmbox clean [box-id ...] [--yes]
  vmbox clean --all [--yes]

Each box is a Railway service named vmbox-<box-id>. `<box-id>`, `new`, and
`start` create, deploy, and attach to the service when it does not exist;
otherwise they attach to the existing box. `stop` removes its active deployment
but preserves the service and /data volume; `resume` deploys it again. Running
processes do not survive a stop. `clean` opens a checkbox selector, accepts
named boxes, or uses `--all`; selected services and their /data volumes are
deleted only after review. `cost` shows accrued costs for the current Railway
billing period and combines removed boxes into one `deleted services (N)` row.
`resize` changes the per-replica vCPU and RAM limits for one
box; Railway continues to bill actual usage rather than the selected limits.
`vmbox <box-id>` is create-or-resume shorthand: it creates a missing box and
reconnects when that name already exists.

New boxes use one setup checklist for components, Railway location, agent profiles,
GitHub, and Markdown instructions before any service is created or deployed.
Move with Up/Down, select with Space, and activate `[ Provision box ]` with Space
or Enter. Enter does nothing on other rows; q cancels without provisioning.
Selected credentials and instructions upload only after the deployment is healthy.

Task Codex interactively in tmux:
  vmbox <box-id> -- codex "inspect active tickets, fix them, test, and commit"
Launch it in tmux and return immediately:
  vmbox <box-id> --detach -- codex "inspect active tickets, fix them, test, and commit"
Use `codex exec` for a non-interactive agent run. Without `--detach`, standard
input stays connected to the forwarded command.

Keep Codex and other work running when you leave:
  1. Press Ctrl-b
  2. Release both keys
  3. Press d

Reconnect with: vmbox resume <box-id>
Use `exit` only when you intend to stop the shell/session.
EOF
}

tmux_help() {
  cat >&2 <<'EOF'

Leave this box without stopping Codex:
  1. Press Ctrl-b
  2. Release both keys
  3. Press d

Then reconnect with: vmbox resume <box-id>
Typing `exit` can terminate the running shell/session.
EOF
}

die() { echo "vmbox: $*" >&2; exit 1; }
require() { command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"; }

railway_api() {
  local query="$1" variables="$2" payload response api_token=""
  local -a headers=(-H "Content-Type: application/json")
  require curl
  if [[ -n "${RAILWAY_API_TOKEN:-}" ]]; then
    headers+=(-H "Authorization: Bearer $RAILWAY_API_TOKEN")
  elif [[ -n "${RAILWAY_TOKEN:-}" ]]; then
    headers+=(-H "Project-Access-Token: $RAILWAY_TOKEN")
  elif [[ -r "${RAILWAY_CONFIG_DIR:-$HOME/.railway}/config.json" ]]; then
    api_token="$(jq -r '.user.token // .user.accessToken // empty' \
      "${RAILWAY_CONFIG_DIR:-$HOME/.railway}/config.json")"
    [[ -n "$api_token" ]] && headers+=(-H "Authorization: Bearer $api_token")
  else
    die "Railway API authentication required; rerun install.sh --workspace-token"
  fi

  ((${#headers[@]} >= 4)) ||
    die "Railway API authentication required; rerun install.sh --workspace-token"

  payload="$(jq -nc --arg query "$query" --argjson variables "$variables" \
    '{query: $query, variables: $variables}')"
  if ! response="$(curl -fsS --connect-timeout 10 --max-time 45 https://backboard.railway.com/graphql/v2 \
    "${headers[@]}" --data-binary "$payload")"; then
    echo "vmbox: Railway API request failed" >&2
    return 1
  fi
  if jq -e '(.errors // []) | length > 0' <<<"$response" >/dev/null; then
    jq -r '.errors[] | "vmbox: Railway API: \(.message)"' <<<"$response" >&2
    return 1
  fi
  jq -c '.data' <<<"$response"
}

railway_api_retry() {
  local query="$1" variables="$2" attempt output
  for attempt in 1 2 3 4 5; do
    if output="$(railway_api "$query" "$variables")"; then
      printf '%s\n' "$output"
      return 0
    fi
    if ((attempt < 5)); then
      echo "vmbox: Railway API update failed (attempt $attempt/5); retrying" >&2
      sleep 2
    fi
  done
  return 1
}

validate_box_id() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] ||
    die "invalid box ID '$1'; use letters, numbers, dots, dashes, or underscores"
}

railway_retry() {
  local attempt output status
  for attempt in 1 2 3 4 5; do
    if output="$("$@" 2>&1)"; then
      printf '%s\n' "$output"
      return 0
    else
      status=$?
    fi
    if ((attempt < 5)); then
      echo "vmbox: Railway request failed (attempt $attempt/5); retrying" >&2
      sleep 2
    fi
  done
  printf '%s\n' "$output" >&2
  return "$status"
}

services() { railway_retry railway service list "${target[@]}" --json; }
volumes() {
  local data
  data="$(railway_retry railway volume "${target[@]}" list --json)" || return
  jq '
    .volumes = ((.volumes // []) |
      map(select(.isPendingDeletion != true and (.deletedAt // null) == null)))
  ' <<<"$data"
}

delete_volume_if_active() {
  local volume_id="$1" current output
  current="$(volumes)"
  jq -e --arg id "$volume_id" 'any(.volumes[]; .id == $id)' \
    <<<"$current" >/dev/null || return 0

  if output="$(railway volume "${target[@]}" delete \
    --volume "$volume_id" --yes --json </dev/null 2>&1)"; then
    return 0
  fi

  current="$(volumes)"
  if ! jq -e --arg id "$volume_id" 'any(.volumes[]; .id == $id)' \
    <<<"$current" >/dev/null; then
    echo "vmbox: volume $volume_id was already pending deletion; skipped" >&2
    return 0
  fi
  printf '%s\n' "$output" >&2
  die "could not delete volume $volume_id"
}

find_service() {
  services | jq -c --arg name "$service_name" \
    'first(.[] | select(.name == $name)) // empty'
}

service_has_deployment_history() {
  local data
  data="$(railway_retry railway deployment list "${target[@]}" \
    --service "$service_name" --limit 1 --json)" || return
  [[ "$(jq 'length' <<<"$data")" != 0 ]]
}

create_service() {
  local query variables result created_id created_name service attempt check
  [[ -f "$bundle/Dockerfile" ]] || die "deployment bundle missing; rerun install.sh"
  echo "vmbox: creating Railway service '$service_name'" >&2
  query='mutation createService($input: ServiceCreateInput!) {
    serviceCreate(input: $input) { id name }
  }'
  variables="$(jq -nc --arg projectId "$VMBOX_PROJECT_ID" \
    --arg environmentId "$VMBOX_ENVIRONMENT_ID" --arg name "$service_name" \
    '{input: {projectId: $projectId, environmentId: $environmentId, name: $name}}')"
  for attempt in 1 2 3; do
    if result="$(railway_api "$query" "$variables")"; then
      created_id="$(jq -r '.serviceCreate.id // empty' <<<"$result")"
      created_name="$(jq -r '.serviceCreate.name // empty' <<<"$result")"
      [[ -n "$created_id" && "$created_name" == "$service_name" ]] && return 0
    fi
    for check in 1 2 3 4 5; do
      service="$(find_service 2>/dev/null || true)"
      [[ -z "$service" ]] || {
        echo "vmbox: service creation recovered after an interrupted API response" >&2
        return 0
      }
      sleep 1
    done
    ((attempt == 3)) || echo "vmbox: retrying service creation ($attempt/3)" >&2
  done
  die "Railway did not create service '$service_name'"
}

wait_for_service() {
  local deployment_id="${1:-}" deadline=$((SECONDS + VMBOX_DEPLOY_TIMEOUT)) data status error
  while ((SECONDS < deadline)); do
    if ! data="$(railway_retry railway deployment list "${target[@]}" \
      --service "$service_name" \
      --limit 20 --json)"; then
      echo "vmbox: deployment status temporarily unavailable; retrying" >&2
      sleep 5
      continue
    fi
    status="$(jq -r --arg id "$deployment_id" '
      if $id == "" then .[0].status // empty
      else first(.[] | select(.id == $id) | .status) // empty end
    ' <<<"$data")"
    case "$status" in
      SUCCESS) echo "vmbox: '$service_name' is ready" >&2; return ;;
      FAILED|CRASHED|REMOVED)
        error="$(jq -r --arg id "$deployment_id" '
          (if $id == "" then .[0] else first(.[] | select(.id == $id)) end)
          | .meta.configErrors[]? // empty
        ' <<<"$data")"
        [[ -z "$error" ]] || printf 'Railway: %s\n' "$error" >&2
        die "deployment for '$service_name' ended with $status"
        ;;
    esac
    echo "vmbox: waiting for '$service_name' (${status:-queued})" >&2
    sleep 5
  done
  die "deployment timed out after ${VMBOX_DEPLOY_TIMEOUT}s"
}

create_data_volume() {
  local service_id="$1" region="$2" query variables result volume_id
  local data attempt check
  echo "vmbox: creating persistent /data volume" >&2
  query='mutation createVolume($input: VolumeCreateInput!) {
    volumeCreate(input: $input) { id }
  }'
  variables="$(jq -nc --arg projectId "$VMBOX_PROJECT_ID" \
    --arg environmentId "$VMBOX_ENVIRONMENT_ID" --arg serviceId "$service_id" \
    --arg mountPath /data --arg region "$region" \
    '{input: {projectId: $projectId, environmentId: $environmentId,
      serviceId: $serviceId, mountPath: $mountPath, region: $region}}')"
  for attempt in 1 2 3; do
    if result="$(railway_api "$query" "$variables")"; then
      volume_id="$(jq -r '.volumeCreate.id // empty' <<<"$result")"
      [[ -n "$volume_id" ]] && return 0
    fi
    for check in 1 2 3 4 5; do
      data="$(volumes 2>/dev/null || true)"
      if jq -e --arg service "$service_name" \
        'any(.volumes[]?; .serviceName == $service and .mountPath == "/data")' \
        <<<"$data" >/dev/null 2>&1; then
        echo "vmbox: volume creation recovered after an interrupted API response" >&2
        return 0
      fi
      sleep 1
    done
    ((attempt == 3)) || echo "vmbox: retrying volume creation ($attempt/3)" >&2
  done
  die "Railway did not create /data for '$service_name'"
}

deploy_bundle() {
  local previous_data previous_id submit_output deployment_id data attempt
  previous_data="$(railway_retry railway deployment list "${target[@]}" \
    --service "$service_name" --limit 1 --json 2>/dev/null || printf '[]')"
  previous_id="$(jq -r '.[0].id // empty' <<<"$previous_data")"

  if submit_output="$(railway up "$bundle" --path-as-root --detach --json \
    --service "$service_name" "${target[@]}" 2>&1)"; then
    deployment_id="$(jq -rs \
      'map(select(type == "object")) | last | .deploymentId // .id // empty' \
      <<<"$submit_output")"
    printf '%s' "$deployment_id"
    return 0
  fi

  echo "vmbox: deployment submission was interrupted; checking Railway before retrying" >&2
  for attempt in {1..10}; do
    data="$(railway_retry railway deployment list "${target[@]}" \
      --service "$service_name" --limit 1 --json 2>/dev/null || printf '[]')"
    deployment_id="$(jq -r '.[0].id // empty' <<<"$data")"
    if [[ -n "$deployment_id" && "$deployment_id" != "$previous_id" ]]; then
      echo "vmbox: recovered submitted deployment '$deployment_id'" >&2
      printf '%s' "$deployment_id"
      return 0
    fi
    sleep 2
  done
  printf '%s\n' "$submit_output" >&2
  return 1
}

wait_for_volume_attachment() {
  local deadline=$((SECONDS + VMBOX_VOLUME_TIMEOUT)) data
  while ((SECONDS < deadline)); do
    data="$(volumes)"
    if jq -e --arg service "$service_name" '
      any(.volumes[]?;
        .serviceName == $service and .mountPath == "/data" and
        ((.status // "") | ascii_downcase) == "ready")
    ' <<<"$data" >/dev/null; then
      echo "vmbox: persistent /data volume is attached and ready" >&2
      return 0
    fi
    sleep 2
  done
  die "persistent /data volume did not become ready for '$service_name' before deployment"
}

ensure_ready() {
  local service="$1" status deployment_id volume_added=0
  local service_id volume_region

  if ! jq -e 'any(.volumes[]?; .mountPath == "/data")' <<<"$service" >/dev/null; then
    service_id="$(jq -r '.id' <<<"$service")"
    volume_region="$(jq -r 'first(.regions[]?.name) // empty' <<<"$service")"
    [[ -n "$volume_region" ]] || volume_region="$(platform_region_id "$selected_region")"
    create_data_volume "$service_id" "$volume_region"
    wait_for_volume_attachment
    volume_added=1
  fi

  status="$(jq -r '.status // empty' <<<"$service")"
  case "$volume_added:$status" in
    0:SUCCESS) return ;;
    0:BUILDING|0:DEPLOYING|0:QUEUED|0:INITIALIZING|0:WAITING) wait_for_service; return ;;
  esac

  echo "vmbox: deploying '$service_name'" >&2
  if ! deployment_id="$(deploy_bundle)"; then
    die "could not submit deployment for '$service_name'"
  fi
  wait_for_service "$deployment_id"
}

persistent_data_mounted() {
  local attempt
  local -a ssh_command=(railway ssh "${target[@]}" --service "$service_name")
  for attempt in 1 2 3; do
    if command -v timeout >/dev/null 2>&1; then
      timeout 30 "${ssh_command[@]}" 'findmnt -rn -M /data >/dev/null' >/dev/null 2>&1 && return 0
    else
      "${ssh_command[@]}" 'findmnt -rn -M /data >/dev/null' >/dev/null 2>&1 && return 0
    fi
    ((attempt == 3)) || { echo "vmbox: /data mount check failed; retrying ($attempt/3)" >&2; sleep 2; }
  done
  return 1
}

ensure_persistent_data() {
  local redeploy_result deployment_id attempt
  echo "vmbox: verifying persistent /data mount" >&2
  if persistent_data_mounted; then
    echo "vmbox: persistent /data is ready" >&2
    return 0
  fi

  echo "vmbox: /data is attached but missing from the running container; redeploying once" >&2
  redeploy_result="$(railway redeploy "${target[@]}" --service "$service_name" --yes --json)"
  deployment_id="$(jq -rs 'map(select(type == "object")) | last | .deploymentId // .id // empty' \
    <<<"$redeploy_result")"
  [[ -n "$deployment_id" ]] || die "Railway did not return a repair deployment ID"
  wait_for_service "$deployment_id"

  for attempt in {1..6}; do
    if persistent_data_mounted; then
      echo "vmbox: persistent /data is ready after repair redeploy" >&2
      return 0
    fi
    echo "vmbox: waiting for repaired /data mount ($attempt/6)" >&2
    sleep 2
  done
  die "persistent /data mount is still missing after repair redeploy for '$service_name'"
}

box_welcome=""
box_status=""

show_box_welcome() {
  local service service_id status regions replicas volume_size limits specs limit_source
  local remote_info private_ip public_ip
  box_status=""
  box_welcome=""
  service="$(find_service)"
  [[ -n "$service" ]] || return 0
  service_id="$(jq -r '.id' <<<"$service")"
  status="$(jq -r '.status // "UNKNOWN"' <<<"$service")"
  regions="$(jq -r '[.regions[]?.name] | if length then join(", ") else "unknown" end' <<<"$service")"
  replicas="$(jq -r '.replicas.running // .replicas.configured // 0' <<<"$service")"
  volume_size="$(jq -r 'first(.volumes[]?) | .currentSizeMb // .currentSizeMB // 0' <<<"$service")"
  limits="$(get_resource_limits "$service_id" 2>/dev/null || true)"
  if [[ -n "$limits" ]]; then
    specs="$(jq -r '(.override // .effective) | "\(.vCPUs) vCPU / \(.memoryGB) GB RAM"' <<<"$limits")"
    limit_source="$(jq -r 'if .override then "custom limit" else "Railway plan default" end' <<<"$limits")"
  else
    specs="unavailable"
    limit_source="resource API unavailable"
  fi

  remote_info="$(railway ssh "${target[@]}" --service "$service_name" \
    'private_ip=$(hostname -I 2>/dev/null | cut -d" " -f1); public_ip=$(curl -fsS --max-time 3 https://api.ipify.org 2>/dev/null || true); printf "%s\t%s\n" "$private_ip" "$public_ip"' \
    2>/dev/null || true)"
  IFS=$'\t' read -r private_ip public_ip <<<"$(tail -1 <<<"$remote_info")"
  box_status=" vmbox $box_id | $specs | $regions "
  [[ -n "$private_ip" ]] || private_ip="unavailable"
  [[ -n "$public_ip" ]] || public_ip="unavailable"

  box_welcome="$(cat <<EOF_BANNER

Box ready
  Name:              $box_id
  Service:           $service_name ($service_id)
  Status / region:   $status / $regions
  Specs:             $specs ($limit_source, per replica)
  Running replicas:  $replicas
  Private IP:        $private_ip
  Public egress IP:  $public_ip (current, not guaranteed static)
  Persistent data:   /data ($volume_size MB used)
  Workspace:         /data/workspace

Keep this session running:
  Detach:            Ctrl-b, release both keys, then d
  Reconnect:         vmbox resume $box_id
  Avoid:             exit (ends the shell/session)
EOF_BANNER
)"
}

attach() {
  local detached="$1" welcome_b64 command_b64="" prepare_script decorate_script decorator_pid
  shift
  show_box_welcome
  welcome_b64="$(printf '%s\n' "$box_welcome" | base64 | tr -d '\n')"
  if (($#)); then
    command_b64="$(printf '%s\0' "$@" | base64 | tr -d '\n')"
  fi
  prepare_script="$(cat <<'EOF_PREPARE'
session="$1"
banner="$2"
status="$3"
command_payload="$4"
command=()
if [[ -n "$command_payload" ]]; then
  mapfile -d '' -t command < <(printf '%s' "$command_payload" | base64 -d)
fi
mkdir -p /data/home
printf '%s' "$banner" | base64 -d > /data/home/.vmbox-welcome
chmod 0644 /data/home/.vmbox-welcome
unset GH_TOKEN GITHUB_TOKEN
if tmux has-session -t "$session" 2>/dev/null; then
  tmux set-environment -g -u GH_TOKEN 2>/dev/null || true
  tmux set-environment -g -u GITHUB_TOKEN 2>/dev/null || true
  if ((${#command[@]})); then
    tmux new-window -t "$session" -c /data/workspace \
      bash -lc 'cat /data/home/.vmbox-welcome; printf "\n"; exec "$@"' bash "${command[@]}"
  fi
else
  if ((${#command[@]})); then
    tmux new-session -d -s "$session" -c /data/workspace \
      bash -lc 'cat /data/home/.vmbox-welcome; printf "\n"; exec "$@"' bash "${command[@]}"
  else
    tmux new-session -d -s "$session" -c /data/workspace \
      bash -lc 'cat /data/home/.vmbox-welcome; printf "\n"; exec bash -l'
  fi
fi
tmux set-option -t "$session" status-left-length 100
tmux set-option -t "$session" status-left "$status"
EOF_PREPARE
)"

  echo "vmbox: preparing tmux with box specs inside the session" >&2
  if (($#)); then
    echo "vmbox: starting forwarded command in tmux: $1" >&2
  fi
  railway ssh "${target[@]}" --service "$service_name" \
    bash -lc "$prepare_script" bash "$box_id" "$welcome_b64" "$box_status" "$command_b64" \
    </dev/null >/dev/null
  if ((detached)); then
    echo "vmbox: command is running detached in tmux on '$box_id'" >&2
    echo "Reconnect with: vmbox $box_id" >&2
    return 0
  fi
  decorate_script="$(cat <<'EOF_DECORATE'
session="$1"
status="$2"
for _ in {1..40}; do
  if tmux list-clients -t "$session" -F '#{client_name}' 2>/dev/null | grep -q .; then
    tmux display-message -t "$session" -d 6000 "$status | Detach: Ctrl-b, then d"
    exit 0
  fi
  sleep 0.25
done
EOF_DECORATE
)"
  railway ssh "${target[@]}" --service "$service_name" \
    bash -lc "$decorate_script" bash "$box_id" "$box_status" \
    </dev/null >/dev/null 2>&1 &
  decorator_pid=$!
  if ! railway ssh "${target[@]}" --service "$service_name" -- tmux attach-session -t "$box_id"; then
    echo "vmbox: SSH attach failed; verify the Railway SSH host key, then reconnect" >&2
    return 1
  fi
  wait "$decorator_pid" 2>/dev/null || true
}

display_list() {
  jq -r --arg prefix "$VMBOX_SERVICE_PREFIX" '
    ["NAME", "STATUS", "ID", "RESUME", "STOP"],
    (.[] |
      (.name | if startswith($prefix) then .[($prefix | length):] else . end) as $box |
      (.status // "NO_DEPLOYMENT") as $status |
      [.name, $status, .id, "vmbox resume \($box)",
       (if $status == "SUCCESS" then "vmbox stop \($box)" else "-" end)]
    ) | @tsv
  ' <<<"$1"
  tmux_help
}

show_cost() {
  local requested="${1:-}" data period_start period_end service service_id
  railway usage projects --help >/dev/null 2>&1 ||
    die "Railway CLI is too old for cost reporting; rerun vmbox-service/install.sh"

  data="$(railway usage projects --project "$VMBOX_PROJECT_ID" --period current --json)"
  period_start="$(jq -r '.billingPeriod.start[0:10]' <<<"$data")"
  period_end="$(jq -r '.billingPeriod.end[0:10]' <<<"$data")"
  printf 'Accrued Railway cost, current billing period (%s to %s)\n\n' "$period_start" "$period_end"
  printf '%-28s %11s %11s %11s %11s %11s %11s\n' \
    NAME TOTAL CPU MEMORY VOLUME EGRESS BACKUP

  if [[ -n "$requested" ]]; then
    validate_box_id "$requested"
    service_name="$VMBOX_SERVICE_PREFIX$requested"
    service="$(find_service)"
    if [[ -z "$service" ]]; then
      service_name="$requested"
      service="$(find_service)"
    fi
    [[ -n "$service" ]] || die "box '$requested' does not exist"
    service_id="$(jq -r '.id' <<<"$service")"
    jq -r --arg id "$service_id" --arg name "$service_name" '
      (first(.services[] | select(.id == $id)) //
      {name: $name, totalDollars: 0, cpuDollars: 0, memoryDollars: 0,
       volumeDollars: 0, egressDollars: 0, backupDollars: 0}) |
      [.name, .totalDollars, .cpuDollars, .memoryDollars, .volumeDollars,
       .egressDollars, .backupDollars] | @tsv
    ' <<<"$data"
  else
    jq -r '
      .services as $services |
      ($services | map(select(.name == "deleted service"))) as $deleted |
      (($services | map(select(.name != "deleted service"))) +
        (if ($deleted | length) > 0 then
          [reduce $deleted[] as $service (
            {name: ("deleted services (" + (($deleted | length) | tostring) + ")"),
             totalDollars: 0, cpuDollars: 0, memoryDollars: 0,
             volumeDollars: 0, egressDollars: 0, backupDollars: 0};
            .totalDollars += ($service.totalDollars // 0) |
            .cpuDollars += ($service.cpuDollars // 0) |
            .memoryDollars += ($service.memoryDollars // 0) |
            .volumeDollars += ($service.volumeDollars // 0) |
            .egressDollars += ($service.egressDollars // 0) |
            .backupDollars += ($service.backupDollars // 0)
          )]
        else [] end))[] |
      [.name, .totalDollars, .cpuDollars, .memoryDollars, .volumeDollars,
       .egressDollars, .backupDollars] | @tsv
    ' <<<"$data"
  fi | while IFS=$'\t' read -r name total cpu memory volume egress backup; do
    printf '%-28s $%10.6f $%10.6f $%10.6f $%10.6f $%10.6f $%10.6f\n' \
      "$name" "$total" "$cpu" "$memory" "$volume" "$egress" "$backup"
  done

  if [[ -z "$requested" ]]; then
    printf '\nProject total: $%.6f\n' "$(jq -r '.currentUsageDollars' <<<"$data")"
  fi
}

selected_resize_box=""
selected_resize_cpu=""
selected_resize_memory=""

select_resize_box() {
  local list selected=0 checked=-1 key rest i marker
  local name status box service_id
  local -a rows
  list="$(services)"
  mapfile -t rows < <(jq -r --arg prefix "$VMBOX_SERVICE_PREFIX" '
    .[] |
    (.name | if startswith($prefix) then .[($prefix | length):] else . end) as $box |
    [.id, .name, (.status // "NO_DEPLOYMENT"), $box] | @tsv
  ' <<<"$list")
  ((${#rows[@]})) || die "no boxes available to resize"
  [[ -t 0 && -t 1 ]] || die "vmbox resize without a box ID requires an interactive terminal"

  while true; do
    printf '\033[2J\033[HChoose a box to resize\n'
    echo "↑/↓ or j/k: move  Space: toggle  Enter: confirm  q: cancel"
    echo "Enter uses the highlighted box."
    echo
    for i in "${!rows[@]}"; do
      IFS=$'\t' read -r service_id name status box <<<"${rows[$i]}"
      ((i == checked)) && marker=x || marker=' '
      if ((i == selected)); then
        printf '\033[1;36m> [%s] %-28s %-14s %s\033[0m\n' "$marker" "$name" "$status" "$box"
      else
        printf '  [%s] %-28s %-14s %s\n' "$marker" "$name" "$status" "$box"
      fi
    done
    IFS= read -rsn1 key || return 1
    case "$key" in
      ' ') ((checked == selected)) && checked=-1 || checked=$selected ;;
      j) selected=$(((selected + 1) % ${#rows[@]})) ;;
      k) selected=$(((selected - 1 + ${#rows[@]}) % ${#rows[@]})) ;;
      '')
        printf '\033[2J\033[H'
        ((checked >= 0)) || checked=$selected
        IFS=$'\t' read -r service_id name status selected_resize_box <<<"${rows[$checked]}"
        return 0
        ;;
      q) printf '\033[2J\033[H'; echo "vmbox: resize cancelled" >&2; return 1 ;;
      $'\e')
        rest=""
        IFS= read -rsn2 -t 0.1 rest || true
        case "$rest" in
          '[A') selected=$(((selected - 1 + ${#rows[@]}) % ${#rows[@]})) ;;
          '[B') selected=$(((selected + 1) % ${#rows[@]})) ;;
          *) printf '\033[2J\033[H'; echo "vmbox: resize cancelled" >&2; return 1 ;;
        esac
        ;;
    esac
  done
}

get_resource_limits() {
  local service_id="$1" query variables
  query='query limits($serviceId: String!, $environmentId: String!) {
    serviceInstanceLimitOverride(serviceId: $serviceId, environmentId: $environmentId)
    serviceInstanceLimits(serviceId: $serviceId, environmentId: $environmentId)
  }'
  variables="$(jq -nc --arg serviceId "$service_id" \
    --arg environmentId "$VMBOX_ENVIRONMENT_ID" \
    '{serviceId: $serviceId, environmentId: $environmentId}')"
  railway_api "$query" "$variables" | jq -c '
    def normalized:
      if . == null then null else
        {vCPUs: (.vCPUs // .containers.cpu),
         memoryGB: (.memoryGB // ((.containers.memoryBytes // 0) / 1000000000))}
      end;
    {override: (.serviceInstanceLimitOverride | normalized),
     effective: (.serviceInstanceLimits | normalized)}
  '
}

resource_limits_label() {
  jq -r '
    if .override then
      "Current override: \(.override.vCPUs) vCPU / \(.override.memoryGB) GB RAM"
    else
      "Current override: none (Railway default: \(.effective.vCPUs) vCPU / \(.effective.memoryGB) GB RAM)"
    end
  ' <<<"$1"
}

valid_positive_number() {
  jq -en --arg value "$1" 'try (($value | tonumber) > 0) catch false' >/dev/null
}

select_resize_limits() {
  local current="$1" selected=1 checked=-1 key rest i marker cpu memory
  local -a labels=("Tiny" "Standard (recommended)" "Large" "XL")
  local -a cpus=(1 2 4 8)
  local -a memories=(1 4 8 16)
  [[ -t 0 && -t 1 ]] || die "choosing CPU/RAM limits requires an interactive terminal"

  while true; do
    printf '\033[2J\033[HResize %s\n' "$service_name"
    resource_limits_label "$current"
    echo "Limits cap usage; Railway bills actual CPU and RAM consumption."
    echo "↑/↓ or j/k: move  Space: toggle  c: custom  Enter: confirm  q: cancel"
    echo
    for i in "${!labels[@]}"; do
      ((i == checked)) && marker=x || marker=' '
      if ((i == selected)); then
        printf '\033[1;36m> [%s] %-24s %s vCPU / %s GB RAM\033[0m\n' \
          "$marker" "${labels[$i]}" "${cpus[$i]}" "${memories[$i]}"
      else
        printf '  [%s] %-24s %s vCPU / %s GB RAM\n' \
          "$marker" "${labels[$i]}" "${cpus[$i]}" "${memories[$i]}"
      fi
    done
    IFS= read -rsn1 key || return 1
    case "$key" in
      ' ') ((checked == selected)) && checked=-1 || checked=$selected ;;
      c)
        printf '\033[2J\033[H'
        read -rp "vCPU limit: " cpu
        read -rp "RAM limit in GB: " memory
        if ! valid_positive_number "$cpu" || ! valid_positive_number "$memory"; then
          echo "vmbox: CPU and RAM limits must be positive numbers" >&2
          sleep 1
          continue
        fi
        selected_resize_cpu="$cpu"
        selected_resize_memory="$memory"
        return 0
        ;;
      j) selected=$(((selected + 1) % ${#labels[@]})) ;;
      k) selected=$(((selected - 1 + ${#labels[@]}) % ${#labels[@]})) ;;
      '')
        printf '\033[2J\033[H'
        ((checked >= 0)) || checked=$selected
        selected_resize_cpu="${cpus[$checked]}"
        selected_resize_memory="${memories[$checked]}"
        return 0
        ;;
      q) printf '\033[2J\033[H'; echo "vmbox: resize cancelled" >&2; return 1 ;;
      $'\e')
        rest=""
        IFS= read -rsn2 -t 0.1 rest || true
        case "$rest" in
          '[A') selected=$(((selected - 1 + ${#labels[@]}) % ${#labels[@]})) ;;
          '[B') selected=$(((selected + 1) % ${#labels[@]})) ;;
          *) printf '\033[2J\033[H'; echo "vmbox: resize cancelled" >&2; return 1 ;;
        esac
        ;;
    esac
  done
}

apply_resource_limits() {
  local service_id="$1" cpu="$2" memory="$3" query variables result verified
  query='mutation resize($input: ServiceInstanceLimitsUpdateInput!) {
    serviceInstanceLimitsUpdate(input: $input)
  }'
  variables="$(jq -nc --arg serviceId "$service_id" \
    --arg environmentId "$VMBOX_ENVIRONMENT_ID" --argjson vCPUs "$cpu" \
    --argjson memoryGB "$memory" \
    '{input: {serviceId: $serviceId, environmentId: $environmentId,
      vCPUs: $vCPUs, memoryGB: $memoryGB}}')"
  result="$(railway_api_retry "$query" "$variables")"
  jq -e '.serviceInstanceLimitsUpdate == true' <<<"$result" >/dev/null ||
    die "Railway did not accept the resource-limit update"
  verified="$(get_resource_limits "$service_id")"
  jq -e --argjson cpu "$cpu" --argjson memory "$memory" \
    '.override.vCPUs == $cpu and .override.memoryGB == $memory' \
    <<<"$verified" >/dev/null || die "Railway accepted resize but verification failed"
  printf "Resized %s to %s vCPU / %s GB RAM.\n" "$service_name" "$cpu" "$memory"
  echo "Railway bills actual usage; these values are per-replica ceilings."
}

resize_box() {
  local requested="${1:-}" service service_id current
  if [[ -z "$requested" ]]; then
    select_resize_box || return 0
    requested="$selected_resize_box"
  fi
  validate_box_id "$requested"
  service_name="$VMBOX_SERVICE_PREFIX$requested"
  service="$(find_service)"
  if [[ -z "$service" ]]; then
    service_name="$requested"
    service="$(find_service)"
  fi
  [[ -n "$service" ]] || die "box '$requested' does not exist"
  service_id="$(jq -r '.id' <<<"$service")"
  current="$(get_resource_limits "$service_id")"
  select_resize_limits "$current" || return 0
  apply_resource_limits "$service_id" "$selected_resize_cpu" "$selected_resize_memory"
}

declare -a component_ids=(codex claude bun foundry)
declare -a component_labels=("Codex CLI" "Claude Code" "Bun" "Foundry: Forge/Cast/Anvil/Chisel")
declare -a component_selected=(1 1 1 1)

selected_components_value() {
  local i joined=""
  for i in "${!component_ids[@]}"; do
    ((component_selected[i])) || continue
    joined="${joined:+$joined,}${component_ids[$i]}"
  done
  printf '%s' "${joined:-core}"
}

apply_selected_components() {
  local service service_id joined query variables result
  service="$(find_service)"
  [[ -n "$service" ]] || die "could not find '$service_name' to configure components"
  service_id="$(jq -r '.id' <<<"$service")"
  joined="$(selected_components_value)"
  echo "vmbox: selected components: $joined" >&2
  query='mutation setComponents($input: VariableCollectionUpsertInput!) {
    variableCollectionUpsert(input: $input)
  }'
  variables="$(jq -nc --arg projectId "$VMBOX_PROJECT_ID" \
    --arg environmentId "$VMBOX_ENVIRONMENT_ID" --arg serviceId "$service_id" \
    --arg components "$joined" \
    '{input: {projectId: $projectId, environmentId: $environmentId,
      serviceId: $serviceId, skipDeploys: true, replace: false,
      variables: {VMBOX_COMPONENTS: $components}}}')"
  result="$(railway_api_retry "$query" "$variables")"
  jq -e '.variableCollectionUpsert == true' <<<"$result" >/dev/null ||
    die "Railway did not accept component configuration for '$service_name'"
}

select_components() {
  local selected=0 key rest i marker
  local component_count=${#component_ids[@]}
  local option_count=$((component_count + 1))
  component_selected=(1 1 1 1)
  if [[ ! -t 0 || ! -t 1 ]]; then
    echo "vmbox: no interactive terminal; installing all optional components" >&2
    return 0
  fi

  while true; do
    printf '\033[2J\033[HChoose components for this box\n'
    echo "Core tools (tmux, Git, gh, SSH, sudo) are always installed."
    echo "↑/↓ or j/k: move  Space: toggle  Enter: confirm"
    echo "Enter accepts the current choices; q keeps all defaults."
    echo "All optional components are selected by default."
    echo
    for i in "${!component_ids[@]}"; do
      ((component_selected[i])) && marker=x || marker=' '
      if ((i == selected)); then
        printf '\033[1;36m> [%s] %s\033[0m\n' "$marker" "${component_labels[$i]}"
      else
        printf '  [%s] %s\n' "$marker" "${component_labels[$i]}"
      fi
    done
    echo
    if ((selected == component_count)); then
      printf '\033[1;36m> [ Confirm selection ]\033[0m\n'
    else
      printf '  [ Confirm selection ]\n'
    fi

    IFS= read -rsn1 key || { component_selected=(1 1 1 1); return 0; }
    case "$key" in
      ' ')
        if ((selected < component_count)); then
          ((component_selected[selected])) && component_selected[selected]=0 || component_selected[selected]=1
        fi
        ;;
      j) selected=$(((selected + 1) % option_count)) ;;
      k) selected=$(((selected - 1 + option_count) % option_count)) ;;
      '')
        printf '\033[2J\033[H'
        return 0
        ;;
      q) component_selected=(1 1 1 1); printf '\033[2J\033[H'; return 0 ;;
      $'\e')
        rest=""
        IFS= read -rsn2 -t 0.1 rest || true
        case "$rest" in
          '[A') selected=$(((selected - 1 + option_count) % option_count)) ;;
          '[B') selected=$(((selected + 1) % option_count)) ;;
          *) component_selected=(1 1 1 1); printf '\033[2J\033[H'; return 0 ;;
        esac
        ;;
    esac
  done
}


declare -a region_ids=(us-west us-east eu-west southeast-asia)
declare -a region_labels=("US West" "US East" "Europe West" "Southeast Asia")
declare -a region_platform_ids=(us-west2 us-east4-eqdc4a europe-west4-drams3a asia-southeast1-eqsg3a)

platform_region_id() {
  local requested="$1" i
  for i in "${!region_ids[@]}"; do
    if [[ "${region_ids[$i]}" == "$requested" ]]; then
      printf '%s' "${region_platform_ids[$i]}"
      return 0
    fi
  done
  return 1
}
selected_region="$VMBOX_DEFAULT_REGION"

apply_selected_region() {
  local region="$1" service service_id region_id="" query variables result i
  [[ "$region" =~ ^[A-Za-z0-9-]+$ ]] || die "invalid Railway region '$region'"
  for i in "${!region_ids[@]}"; do
    [[ "${region_ids[$i]}" == "$region" ]] && region_id="${region_platform_ids[$i]}"
  done
  [[ -n "$region_id" ]] || die "unsupported Railway region '$region'"
  service="$(find_service)"
  [[ -n "$service" ]] || die "could not find '$service_name' to set its region"
  service_id="$(jq -r '.id' <<<"$service")"
  echo "vmbox: selected Railway region: $region" >&2
  query='mutation setRegion($serviceId: String!, $environmentId: String!, $input: ServiceInstanceUpdateInput!) {
    serviceInstanceUpdate(serviceId: $serviceId, environmentId: $environmentId, input: $input)
  }'
  variables="$(jq -nc --arg serviceId "$service_id" --arg environmentId "$VMBOX_ENVIRONMENT_ID" \
    --arg regionId "$region_id" \
    '{serviceId: $serviceId, environmentId: $environmentId,
      input: {multiRegionConfig: {($regionId): {numReplicas: 1}}}}')"
  result="$(railway_api_retry "$query" "$variables")"
  jq -e '.serviceInstanceUpdate == true' <<<"$result" >/dev/null ||
    die "Railway did not accept region '$region'"
}

select_region() {
  local selected=0 chosen=-1 key rest i marker region_count option_count
  region_count=${#region_ids[@]}
  option_count=$((region_count + 1))
  for i in "${!region_ids[@]}"; do
    if [[ "${region_ids[$i]}" == "$VMBOX_DEFAULT_REGION" ]]; then
      selected=$i
      chosen=$i
      break
    fi
  done
  ((chosen >= 0)) || die "VMBOX_DEFAULT_REGION must be one of: ${region_ids[*]}"

  if [[ ! -t 0 || ! -t 1 ]]; then
    echo "vmbox: no interactive terminal; using region $VMBOX_DEFAULT_REGION" >&2
    selected_region="$VMBOX_DEFAULT_REGION"
    return 0
  fi

  while true; do
    printf '\033[2J\033[HChoose a Railway location for this box\n'
    echo "↑/↓ or j/k: move  Space: select  Enter: confirm"
    echo "Enter accepts the highlighted location; q keeps the configured default."
    echo
    for i in "${!region_ids[@]}"; do
      ((i == chosen)) && marker=x || marker=' '
      if ((i == selected)); then
        printf '\033[1;36m> [%s] %-20s %s\033[0m\n' "$marker" "${region_labels[$i]}" "${region_ids[$i]}"
      else
        printf '  [%s] %-20s %s\n' "$marker" "${region_labels[$i]}" "${region_ids[$i]}"
      fi
    done
    echo
    if ((selected == region_count)); then
      printf '\033[1;36m> [ Confirm location ]\033[0m\n'
    else
      printf '  [ Confirm location ]\n'
    fi

    IFS= read -rsn1 key || { selected_region="${region_ids[$chosen]}"; return 0; }
    case "$key" in
      ' ') ((selected < region_count)) && chosen=$selected ;;
      j) selected=$(((selected + 1) % option_count)) ;;
      k) selected=$(((selected - 1 + option_count) % option_count)) ;;
      '')
        ((selected >= region_count)) || chosen=$selected
        printf '\033[2J\033[H'
        selected_region="${region_ids[$chosen]}"
        return 0
        ;;
      q) printf '\033[2J\033[H'; selected_region="$VMBOX_DEFAULT_REGION"; return 0 ;;
      $'\e')
        rest=""
        IFS= read -rsn2 -t 0.1 rest || true
        case "$rest" in
          '[A') selected=$(((selected - 1 + option_count) % option_count)) ;;
          '[B') selected=$(((selected + 1) % option_count)) ;;
          *) printf '\033[2J\033[H'; selected_region="$VMBOX_DEFAULT_REGION"; return 0 ;;
        esac
        ;;
    esac
  done
}



declare -a profile_providers=() profile_sources=() profile_selected=()

profile_has_files() {
  local provider="$1" source="$2" path
  case "$provider" in
    codex)
      [[ -r "$source/auth.json" || -r "$source/config.toml" ]] && return 0
      for path in "$source"/*.config.toml; do [[ -r "$path" ]] && return 0; done
      ;;
    claude)
      [[ "$source" == "$HOME/.claude" && -r "$HOME/.claude.json" ]] && return 0
      [[ -r "$source/.credentials.json" || -r "$source/settings.json" ||
         -r "$source/.claude.json" ]] && return 0
      ;;
  esac
  return 1
}

add_profile_candidate() {
  local provider="$1" source="$2" existing
  [[ -d "$source" && -r "$source" ]] || return 0
  profile_has_files "$provider" "$source" || return 0
  for existing in "${profile_sources[@]:-}"; do
    [[ "$existing" == "$source" ]] && return 0
  done
  profile_providers+=("$provider")
  profile_sources+=("$source")
  profile_selected+=(0)
}

discover_profiles() {
  local path
  profile_providers=()
  profile_sources=()
  profile_selected=()
  [[ -n "${CODEX_HOME:-}" ]] && add_profile_candidate codex "$CODEX_HOME"
  for path in "$HOME"/.codex*; do add_profile_candidate codex "$path" || true; done
  [[ -n "${CLAUDE_CONFIG_DIR:-}" ]] && add_profile_candidate claude "$CLAUDE_CONFIG_DIR"
  for path in "$HOME"/.claude*; do add_profile_candidate claude "$path" || true; done
}

toggle_profile_candidate() {
  local selected_index="$1" provider="${profile_providers[$1]}" i
  if ((profile_selected[selected_index])); then
    profile_selected[selected_index]=0
    return
  fi
  for i in "${!profile_selected[@]}"; do
    [[ "${profile_providers[$i]}" == "$provider" ]] && profile_selected[i]=0
  done
  profile_selected[selected_index]=1
}

add_custom_profile() {
  local provider source
  printf '\033[2J\033[H'
  read -rp "Tool for this profile [codex/claude]: " provider
  case "${provider,,}" in
    codex|claude) provider="${provider,,}" ;;
    *) echo "vmbox: custom profile skipped (unknown tool)" >&2; sleep 1; return ;;
  esac
  read -erp "Profile directory path: " source
  [[ "$source" == '~/'* ]] && source="$HOME/${source#\~/}"
  [[ -f "$source" ]] && source="$(dirname -- "$source")"
  if [[ ! -d "$source" || ! -r "$source" ]]; then
    echo "vmbox: custom profile must be a readable directory" >&2
    sleep 1
    return
  fi
  if ! profile_has_files "$provider" "$source"; then
    echo "vmbox: no supported $provider config or login files found in $source" >&2
    sleep 1
    return
  fi
  add_profile_candidate "$provider" "$source"
}

copy_profile_file() {
  local source="$1" remote_dir="$2" remote_file="$3" mode="${4:-600}"
  local local_sha remote_output remote_sha
  [[ -f "$source" && -r "$source" ]] || return 0
  echo "vmbox: copying $(basename -- "$source") to $remote_file" >&2
  local_sha="$(sha256sum "$source" | awk '{print $1}')"
  remote_output="$(railway ssh "${target[@]}" --service "$service_name" \
    "umask 077; mkdir -p '$remote_dir'; chmod 700 '$remote_dir'; cat > '$remote_file'; chmod '$mode' '$remote_file'; sha256sum '$remote_file'" \
    < "$source")"
  remote_sha="$(grep -Eo '[0-9a-f]{64}' <<<"$remote_output" | tail -1 || true)"
  [[ -n "$remote_sha" && "$remote_sha" == "$local_sha" ]] ||
    die "upload verification failed for $remote_file"
  copied_files=$((copied_files + 1))
}

verify_profile_login() {
  local provider="$1"
  case "$provider" in
    codex)
      if railway ssh "${target[@]}" --service "$service_name" \
        "HOME=/data/home CODEX_HOME=/data/home/.codex codex login status >/dev/null 2>&1" >/dev/null; then
        echo "vmbox: Codex login recognized inside the box" >&2
      else
        echo "vmbox: warning: Codex did not recognize the uploaded login; run 'codex login' inside the box" >&2
      fi
      ;;
    claude)
      if railway ssh "${target[@]}" --service "$service_name" \
        "HOME=/data/home CLAUDE_CONFIG_DIR=/data/home/.claude claude auth status --json 2>/dev/null | jq -e '.loggedIn == true' >/dev/null" >/dev/null; then
        echo "vmbox: Claude login recognized inside the box" >&2
      else
        echo "vmbox: warning: Claude did not recognize the uploaded login; run 'claude auth login' inside the box" >&2
      fi
      ;;
  esac
}

copy_selected_profiles() {
  local i provider source path base copied_profiles=0 copied_files=0
  for i in "${!profile_selected[@]}"; do
    ((profile_selected[i])) || continue
    provider="${profile_providers[$i]}"
    source="${profile_sources[$i]}"
    echo "vmbox: installing selected $provider profile from $source" >&2
    case "$provider" in
      codex)
        copy_profile_file "$source/auth.json" /data/home/.codex /data/home/.codex/auth.json
        copy_profile_file "$source/config.toml" /data/home/.codex /data/home/.codex/config.toml
        for path in "$source"/*.config.toml; do
          [[ -f "$path" ]] || continue
          base="$(basename -- "$path")"
          [[ "$base" =~ ^[A-Za-z0-9._-]+$ ]] || continue
          copy_profile_file "$path" /data/home/.codex "/data/home/.codex/$base"
        done
        [[ ! -r "$source/auth.json" ]] || verify_profile_login codex
        ;;
      claude)
        copy_profile_file "$source/.credentials.json" /data/home/.claude /data/home/.claude/.credentials.json
        copy_profile_file "$source/settings.json" /data/home/.claude /data/home/.claude/settings.json
        if [[ "$source" == "$HOME/.claude" && -r "$HOME/.claude.json" ]]; then
          copy_profile_file "$HOME/.claude.json" /data/home /data/home/.claude.json
        else
          copy_profile_file "$source/.claude.json" /data/home /data/home/.claude.json
        fi
        [[ ! -r "$source/.credentials.json" ]] || verify_profile_login claude
        ;;
    esac
    copied_profiles=$((copied_profiles + 1))
  done
  if ((copied_profiles)); then
    echo "vmbox: installed $copied_profiles agent profile(s), $copied_files file(s) total" >&2
    echo "vmbox: these files persist in /data; treat this box as an authenticated device" >&2
  fi
}

profile_contents() {
  local provider="$1" source="$2" item result=""
  case "$provider" in
    codex)
      [[ -r "$source/auth.json" ]] && result="login"
      [[ -r "$source/config.toml" ]] && result="${result:+$result+}config/MCP"
      for item in "$source"/*.config.toml; do
        [[ -r "$item" ]] && { result="${result:+$result+}profiles"; break; }
      done
      ;;
    claude)
      [[ "$source" == "$HOME/.claude" && -r "$HOME/.claude.json" ]] && result="config/MCP"
      [[ -r "$source/.credentials.json" ]] && result="login"
      if [[ -r "$source/settings.json" || -r "$source/.claude.json" ]]; then
        result="${result:+$result+}config/MCP"
      fi
      ;;
  esac
  printf '%s' "${result:-config}"
}

select_profiles() {
  local upload_after="${1:-1}" selected=0 key rest i marker label contents
  discover_profiles
  ((${#profile_sources[@]})) || {
    echo "vmbox: no local Codex or Claude profiles found" >&2
    return
  }
  if [[ ! -t 0 || ! -t 1 ]]; then
    echo "vmbox: local agent profiles found; upload skipped without an interactive terminal" >&2
    return
  fi

  while true; do
    printf '\033[2J\033[HChoose agent profiles for this box\n'
    echo "Nothing is selected by default. Login and config/MCP files may grant account access."
    echo "↑/↓ or j/k: move  Space: toggle  a: add path  Enter: confirm  q: skip"
    echo "You may select one Codex profile and one Claude profile."
    echo
    for i in "${!profile_sources[@]}"; do
      ((profile_selected[i])) && marker=x || marker=' '
      label="${profile_sources[$i]}"
      [[ "$label" == "$HOME/"* ]] && label="~/${label#"$HOME/"}"
      contents="$(profile_contents "${profile_providers[$i]}" "${profile_sources[$i]}")"
      if ((i == selected)); then
        printf '\033[1;36m> [%s] %-7s %-18s %s\033[0m\n' "$marker" "${profile_providers[$i]}" "$contents" "$label"
      else
        printf '  [%s] %-7s %-18s %s\n' "$marker" "${profile_providers[$i]}" "$contents" "$label"
      fi
    done
    IFS= read -rsn1 key || { profile_selected=(); return; }
    case "$key" in
      ' ') toggle_profile_candidate "$selected" ;;
      a) add_custom_profile; selected=$((${#profile_sources[@]} - 1)) ;;
      j) selected=$(((selected + 1) % ${#profile_sources[@]})) ;;
      k) selected=$(((selected - 1 + ${#profile_sources[@]}) % ${#profile_sources[@]})) ;;
      '')
        printf '\033[2J\033[H'
        [[ "$upload_after" == 0 ]] || copy_selected_profiles
        return
        ;;
      q) profile_selected=(); printf '\033[2J\033[H'; echo "vmbox: agent profile upload skipped" >&2; return ;;
      $'\e')
        rest=""
        IFS= read -rsn2 -t 0.1 rest || true
        case "$rest" in
          '[A') selected=$(((selected - 1 + ${#profile_sources[@]}) % ${#profile_sources[@]})) ;;
          '[B') selected=$(((selected + 1) % ${#profile_sources[@]})) ;;
          *) profile_selected=(); printf '\033[2J\033[H'; echo "vmbox: agent profile upload skipped" >&2; return ;;
        esac
        ;;
    esac
  done
}
declare -a instruction_sources=() instruction_selected=()

add_instruction_candidate() {
  local source="$1" existing
  [[ -f "$source" && -r "$source" ]] || return 0
  [[ "${source,,}" == *.md ]] || return 0
  source="$(realpath -e -- "$source" 2>/dev/null || printf '%s' "$source")"
  for existing in "${instruction_sources[@]:-}"; do
    [[ "$existing" == "$source" ]] && return 0
  done
  instruction_sources+=("$source")
  instruction_selected+=(0)
}

discover_instruction_files() {
  local path
  instruction_sources=()
  instruction_selected=()
  [[ -n "${VMBOX_INSTRUCTIONS_FILE:-}" ]] &&
    add_instruction_candidate "$VMBOX_INSTRUCTIONS_FILE"
  for path in "$PWD"/*.[mM][dD]; do
    add_instruction_candidate "$path"
  done
}

toggle_instruction_file() {
  local selected_index="$1" i
  if ((instruction_selected[selected_index])); then
    instruction_selected[selected_index]=0
    return 0
  fi
  for i in "${!instruction_selected[@]}"; do
    instruction_selected[i]=0
  done
  instruction_selected[selected_index]=1
}

add_custom_instruction_file() {
  local source i
  printf '\033[2J\033[H'
  read -erp "Markdown instructions path: " source
  [[ "$source" == '~/'* ]] && source="$HOME/${source#\~/}"
  if [[ ! -f "$source" || ! -r "$source" || "${source,,}" != *.md ]]; then
    echo "vmbox: instructions must be a readable .md file" >&2
    sleep 1
    return 1
  fi
  add_instruction_candidate "$source"
  for i in "${!instruction_selected[@]}"; do
    instruction_selected[i]=0
  done
  instruction_selected[$((${#instruction_selected[@]} - 1))]=1
}

copy_selected_instruction_file() {
  local i source copied_files=0
  for i in "${!instruction_selected[@]}"; do
    ((instruction_selected[i])) || continue
    source="${instruction_sources[$i]}"
    echo "vmbox: installing shared agent instructions from $source" >&2
    copy_profile_file "$source" /data/workspace /data/workspace/AGENTS.md 644
    copy_profile_file "$source" /data/workspace /data/workspace/CLAUDE.md 644
    echo "vmbox: installed instructions as /data/workspace/AGENTS.md and CLAUDE.md" >&2
    return 0
  done
  echo "vmbox: agent instruction upload skipped (nothing selected)" >&2
}

select_instruction_file() {
  local upload_after="${1:-1}" selected=0 key rest i marker label
  discover_instruction_files
  if [[ ! -t 0 || ! -t 1 ]]; then
    echo "vmbox: agent instruction upload skipped without an interactive terminal" >&2
    return 0
  fi

  while true; do
    printf '\033[2J\033[HChoose shared project instructions for this box\n'
    echo "The selected .md file is copied as both AGENTS.md and CLAUDE.md."
    echo "↑/↓ or j/k: move  Space: toggle  a: add any .md path  Enter: confirm  q: skip"
    echo "Nothing is selected by default. Select at most one file."
    echo
    if ((${#instruction_sources[@]} == 0)); then
      echo "  No .md files found in the current directory; press a to add a path."
    fi
    for i in "${!instruction_sources[@]}"; do
      ((instruction_selected[i])) && marker=x || marker=' '
      label="${instruction_sources[$i]}"
      [[ "$label" == "$PWD/"* ]] && label="./${label#"$PWD/"}"
      [[ "$label" == "$HOME/"* ]] && label="~/${label#"$HOME/"}"
      if ((i == selected)); then
        printf '\033[1;36m> [%s] %s\033[0m\n' "$marker" "$label"
      else
        printf '  [%s] %s\n' "$marker" "$label"
      fi
    done
    IFS= read -rsn1 key || { instruction_selected=(); return 0; }
    case "$key" in
      ' ') ((${#instruction_sources[@]})) && toggle_instruction_file "$selected" ;;
      a)
        if add_custom_instruction_file; then
          selected=$((${#instruction_sources[@]} - 1))
        fi
        ;;
      j) ((${#instruction_sources[@]})) && selected=$(((selected + 1) % ${#instruction_sources[@]})) ;;
      k) ((${#instruction_sources[@]})) && selected=$(((selected - 1 + ${#instruction_sources[@]}) % ${#instruction_sources[@]})) ;;
      '')
        printf '\033[2J\033[H'
        [[ "$upload_after" == 0 ]] || copy_selected_instruction_file
        return 0
        ;;
      q) instruction_selected=(); printf '\033[2J\033[H'; echo "vmbox: agent instruction upload skipped" >&2; return 0 ;;
      $'\e')
        rest=""
        IFS= read -rsn2 -t 0.1 rest || true
        case "$rest" in
          '[A') ((${#instruction_sources[@]})) && selected=$(((selected - 1 + ${#instruction_sources[@]}) % ${#instruction_sources[@]})) ;;
          '[B') ((${#instruction_sources[@]})) && selected=$(((selected + 1) % ${#instruction_sources[@]})) ;;
          *) instruction_selected=(); printf '\033[2J\033[H'; echo "vmbox: agent instruction upload skipped" >&2; return 0 ;;
        esac
        ;;
    esac
  done
}

declare -a github_hosts=() github_users=() github_protocols=() github_selected=()

discover_github_accounts() {
  local output line host="" user protocol last=-1
  github_hosts=()
  github_users=()
  github_protocols=()
  github_selected=()
  command -v gh >/dev/null 2>&1 || return 0
  output="$(gh auth status 2>&1 || true)"
  while IFS= read -r line; do
    if [[ "$line" =~ ^[^[:space:]] ]]; then
      host="$line"
      [[ "$host" =~ ^[A-Za-z0-9.-]+$ ]] || host=""
    elif [[ -n "$host" && "$line" =~ account[[:space:]]+([^[:space:]]+) ]]; then
      user="${BASH_REMATCH[1]}"
      [[ "$user" =~ ^[A-Za-z0-9-]+$ ]] || continue
      github_hosts+=("$host")
      github_users+=("$user")
      github_protocols+=(https)
      github_selected+=(0)
      last=$((${#github_users[@]} - 1))
    elif ((last >= 0)) && [[ "$line" =~ Git[[:space:]]operations[[:space:]]protocol:[[:space:]]+([^[:space:]]+) ]]; then
      protocol="${BASH_REMATCH[1]}"
      [[ "$protocol" == ssh || "$protocol" == https ]] && github_protocols[last]="$protocol"
    fi
  done <<<"$output"
  return 0
}

toggle_github_account() {
  local selected_index="$1" i
  if ((github_selected[selected_index])); then
    github_selected[selected_index]=0
    return 0
  fi
  for i in "${!github_selected[@]}"; do
    github_selected[i]=0
  done
  github_selected[selected_index]=1
}

upload_selected_github_account() {
  local i
  for i in "${!github_selected[@]}"; do
    if ((github_selected[i])); then
      upload_github_account "$i"
      return 0
    fi
  done
  echo "vmbox: GitHub account sync skipped (nothing selected)" >&2
}

upload_github_account() {
  local index="$1" host user protocol token remote_output remote_user
  local git_name git_email identity_synced=0
  host="${github_hosts[$index]}"
  user="${github_users[$index]}"
  protocol="${github_protocols[$index]}"

  if ! railway ssh "${target[@]}" --service "$service_name" \
    "command -v gh >/dev/null 2>&1" >/dev/null; then
    echo "vmbox: warning: GitHub CLI is missing in this deployment; redeploy the box with the current image" >&2
    return 0
  fi
  if ! token="$(gh auth token --hostname "$host" --user "$user" 2>/dev/null)"; then
    echo "vmbox: warning: could not read the local gh token for $user@$host" >&2
    return 0
  fi

  git_name="$(GH_TOKEN="$token" GH_HOST="$host" gh api user --jq '.name // .login' 2>/dev/null || printf '%s' "$user")"
  git_email="$(GH_TOKEN="$token" GH_HOST="$host" gh api user --jq '.email // empty' 2>/dev/null || true)"
  [[ -n "$git_email" ]] || git_email="$user@users.noreply.github.com"

  echo "vmbox: securely syncing GitHub account $user@$host ($protocol)" >&2
  if ! printf '%s\n' "$token" | railway ssh "${target[@]}" --service "$service_name" \
    "umask 077; export HOME=/data/home; unset GH_TOKEN GITHUB_TOKEN; gh auth login --hostname '$host' --git-protocol '$protocol' --with-token >/dev/null && gh auth setup-git --hostname '$host' >/dev/null; chmod 700 /data/home/.config/gh; chmod 600 /data/home/.config/gh/hosts.yml" >/dev/null; then
    unset token
    echo "vmbox: warning: GitHub account sync failed for $user@$host" >&2
    return 0
  fi

  if printf '%s\n%s\n' "$git_name" "$git_email" | railway ssh "${target[@]}" --service "$service_name" \
    'export HOME=/data/home; IFS= read -r name; IFS= read -r email; git config --global user.name "$name"; git config --global user.email "$email"' >/dev/null; then
    identity_synced=1
  fi
  unset token

  if ! remote_output="$(railway ssh "${target[@]}" --service "$service_name" \
    "export HOME=/data/home GH_HOST='$host'; unset GH_TOKEN GITHUB_TOKEN; gh api user --jq .login" 2>/dev/null)"; then
    echo "vmbox: warning: GitHub credentials were stored, but the API login check failed" >&2
    return 0
  fi
  remote_user="$(grep -Fx "$user" <<<"$remote_output" | tail -1 || true)"
  if [[ "$remote_user" == "$user" ]]; then
    if ((identity_synced)); then
      echo "vmbox: GitHub account $user recognized; token permissions and Git commit identity are configured" >&2
    else
      echo "vmbox: GitHub account $user recognized; token permissions are preserved (Git identity sync failed)" >&2
    fi
  else
    echo "vmbox: warning: GitHub API returned a different account after sync" >&2
  fi
}

select_github_account() {
  local upload_after="${1:-1}" selected=0 key rest i marker
  discover_github_accounts
  ((${#github_users[@]})) || {
    echo "vmbox: no authenticated local GitHub CLI accounts found; GitHub sync skipped" >&2
    return 0
  }
  if [[ ! -t 0 || ! -t 1 ]]; then
    echo "vmbox: local GitHub account found; sync skipped without an interactive terminal" >&2
    return 0
  fi

  while true; do
    printf '\033[2J\033[HChoose a GitHub CLI account for this box\n'
    echo "↑/↓ or j/k: move  Space: toggle  Enter: confirm  q: skip"
    echo "Nothing is selected by default. Select at most one account."
    echo "A selected token keeps its existing repository and organization permissions."
    echo
    for i in "${!github_users[@]}"; do
      ((github_selected[i])) && marker=x || marker=' '
      if ((i == selected)); then
        printf '\033[1;36m> [%s] %-24s %-24s %s\033[0m\n' "$marker" "${github_users[$i]}" "${github_hosts[$i]}" "${github_protocols[$i]}"
      else
        printf '  [%s] %-24s %-24s %s\n' "$marker" "${github_users[$i]}" "${github_hosts[$i]}" "${github_protocols[$i]}"
      fi
    done
    IFS= read -rsn1 key || { github_selected=(); return 0; }
    case "$key" in
      ' ') toggle_github_account "$selected" ;;
      j) selected=$(((selected + 1) % ${#github_users[@]})) ;;
      k) selected=$(((selected - 1 + ${#github_users[@]}) % ${#github_users[@]})) ;;
      '')
        printf '\033[2J\033[H'
        [[ "$upload_after" == 0 ]] || upload_selected_github_account
        return 0
        ;;
      q) github_selected=(); printf '\033[2J\033[H'; echo "vmbox: GitHub account sync skipped" >&2; return 0 ;;
      $'\e')
        rest=""
        IFS= read -rsn2 -t 0.1 rest || true
        case "$rest" in
          '[A') selected=$(((selected - 1 + ${#github_users[@]}) % ${#github_users[@]})) ;;
          '[B') selected=$(((selected + 1) % ${#github_users[@]})) ;;
          *) github_selected=(); printf '\033[2J\033[H'; echo "vmbox: GitHub account sync skipped" >&2; return 0 ;;
        esac
        ;;
    esac
  done
}

select_credentials() {
  select_profiles
  select_github_account
}
print_setup_row() {
  local cursor="$1" row="$2" text="$3"
  if ((cursor == row)); then
    printf '\033[1;36m> %s\033[0m\n' "$text"
  else
    printf '  %s\n' "$text"
  fi
}

select_new_box_setup() {
  local selected=0 key rest i marker label contents line old_count
  local component_start region_start profile_start add_profile_row github_start
  local instruction_start add_instruction_row confirm_row row_count index

  component_selected=(1 1 1 1)
  selected_region="$VMBOX_DEFAULT_REGION"
  discover_profiles
  discover_github_accounts
  discover_instruction_files

  if [[ ! -t 0 || ! -t 1 ]]; then
    echo "vmbox: no interactive terminal; using default components/region without credential uploads" >&2
    return 0
  fi

  while true; do
    component_start=0
    region_start=${#component_ids[@]}
    profile_start=$((region_start + ${#region_ids[@]}))
    add_profile_row=$((profile_start + ${#profile_sources[@]}))
    github_start=$((add_profile_row + 1))
    instruction_start=$((github_start + ${#github_users[@]}))
    add_instruction_row=$((instruction_start + ${#instruction_sources[@]}))
    confirm_row=$((add_instruction_row + 1))
    row_count=$((confirm_row + 1))
    ((selected < row_count)) || selected=$confirm_row

    printf '\033[2J\033[HNew box setup: %s\n' "$box_id"
    echo "↑/↓ or j/k: move  Space: select/action  Enter: confirm only on Provision box  q: cancel"
    echo "Nothing is provisioned until you activate Provision box."
    echo

    echo "Components (core tools are always installed)"
    for i in "${!component_ids[@]}"; do
      ((component_selected[i])) && marker=x || marker=' '
      printf -v line '[%s] %s' "$marker" "${component_labels[$i]}"
      print_setup_row "$selected" "$((component_start + i))" "$line"
    done
    echo

    echo "Railway location"
    for i in "${!region_ids[@]}"; do
      [[ "${region_ids[$i]}" == "$selected_region" ]] && marker=x || marker=' '
      printf -v line '[%s] %-20s %s' "$marker" "${region_labels[$i]}" "${region_ids[$i]}"
      print_setup_row "$selected" "$((region_start + i))" "$line"
    done
    echo

    echo "Codex / Claude profiles (optional; one per tool)"
    if ((${#profile_sources[@]} == 0)); then echo "  (none found)"; fi
    for i in "${!profile_sources[@]}"; do
      ((profile_selected[i])) && marker=x || marker=' '
      label="${profile_sources[$i]}"
      [[ "$label" == "$HOME/"* ]] && label="~/${label#"$HOME/"}"
      contents="$(profile_contents "${profile_providers[$i]}" "${profile_sources[$i]}")"
      printf -v line '[%s] %-7s %-18s %s' "$marker" "${profile_providers[$i]}" "$contents" "$label"
      print_setup_row "$selected" "$((profile_start + i))" "$line"
    done
    print_setup_row "$selected" "$add_profile_row" '[ Add agent profile path ]'
    echo

    echo "GitHub CLI account (optional)"
    if ((${#github_users[@]} == 0)); then echo "  (none found)"; fi
    for i in "${!github_users[@]}"; do
      ((github_selected[i])) && marker=x || marker=' '
      printf -v line '[%s] %-24s %-20s %s' "$marker" "${github_users[$i]}" "${github_hosts[$i]}" "${github_protocols[$i]}"
      print_setup_row "$selected" "$((github_start + i))" "$line"
    done
    echo

    echo "Shared AGENTS.md / CLAUDE.md source (optional)"
    if ((${#instruction_sources[@]} == 0)); then echo "  (none found)"; fi
    for i in "${!instruction_sources[@]}"; do
      ((instruction_selected[i])) && marker=x || marker=' '
      label="${instruction_sources[$i]}"
      [[ "$label" == "$PWD/"* ]] && label="./${label#"$PWD/"}"
      [[ "$label" == "$HOME/"* ]] && label="~/${label#"$HOME/"}"
      printf -v line '[%s] %s' "$marker" "$label"
      print_setup_row "$selected" "$((instruction_start + i))" "$line"
    done
    print_setup_row "$selected" "$add_instruction_row" '[ Add Markdown path ]'
    echo
    print_setup_row "$selected" "$confirm_row" '[ Provision box ]'

    IFS= read -rsn1 key || { printf '\033[2J\033[H'; echo "vmbox: setup cancelled" >&2; return 1; }
    case "$key" in
      ' ')
        if ((selected >= component_start && selected < region_start)); then
          index=$((selected - component_start))
          ((component_selected[index])) && component_selected[index]=0 || component_selected[index]=1
        elif ((selected >= region_start && selected < profile_start)); then
          index=$((selected - region_start))
          selected_region="${region_ids[$index]}"
        elif ((selected >= profile_start && selected < add_profile_row)); then
          toggle_profile_candidate "$((selected - profile_start))"
        elif ((selected == add_profile_row)); then
          old_count=${#profile_sources[@]}
          add_custom_profile
          if ((${#profile_sources[@]} > old_count)); then
            selected=$((profile_start + ${#profile_sources[@]} - 1))
          fi
        elif ((selected >= github_start && selected < instruction_start)); then
          toggle_github_account "$((selected - github_start))"
        elif ((selected >= instruction_start && selected < add_instruction_row)); then
          toggle_instruction_file "$((selected - instruction_start))"
        elif ((selected == add_instruction_row)); then
          old_count=${#instruction_sources[@]}
          add_custom_instruction_file || true
          if ((${#instruction_sources[@]} > old_count)); then
            selected=$((instruction_start + ${#instruction_sources[@]} - 1))
          fi
        elif ((selected == confirm_row)); then
          printf '\033[2J\033[H'
          return 0
        fi
        ;;
      '')
        if ((selected == confirm_row)); then
          printf '\033[2J\033[H'
          return 0
        fi
        ;;
      j) selected=$(((selected + 1) % row_count)) ;;
      k) selected=$(((selected - 1 + row_count) % row_count)) ;;
      q) printf '\033[2J\033[H'; echo "vmbox: setup cancelled; nothing provisioned" >&2; return 1 ;;
      $'\e')
        rest=""
        IFS= read -rsn2 -t 0.1 rest || true
        case "$rest" in
          '[A') selected=$(((selected - 1 + row_count) % row_count)) ;;
          '[B') selected=$(((selected + 1) % row_count)) ;;
        esac
        ;;
    esac
  done
}

apply_new_box_setup() {
  copy_selected_profiles
  upload_selected_github_account
  copy_selected_instruction_file
}

select_setup_for_new_box() {
  local tty_fd status
  if [[ ! -t 0 ]] && { exec {tty_fd}<>/dev/tty; } 2>/dev/null; then
    if select_new_box_setup <&"$tty_fd" >&"$tty_fd"; then status=0; else status=$?; fi
    exec {tty_fd}>&-
    return "$status"
  fi
  select_new_box_setup
}


copy_auth_to_box() {
  local service
  box_id="$1"
  validate_box_id "$box_id"
  service_name="$VMBOX_SERVICE_PREFIX$box_id"
  service="$(find_service)"
  if [[ -z "$service" ]]; then
    service_name="$box_id"
    service="$(find_service)"
  fi
  [[ -n "$service" ]] || die "box '$box_id' does not exist; use: vmbox start $box_id"
  [[ "$(jq -r '.status // empty' <<<"$service")" == SUCCESS ]] ||
    die "box '$box_id' is not ready; repair it first with: vmbox start $box_id"
  select_credentials
}

sync_github_to_box() {
  local service
  box_id="$1"
  validate_box_id "$box_id"
  service_name="$VMBOX_SERVICE_PREFIX$box_id"
  service="$(find_service)"
  if [[ -z "$service" ]]; then
    service_name="$box_id"
    service="$(find_service)"
  fi
  [[ -n "$service" ]] || die "box '$box_id' does not exist; use: vmbox start $box_id"
  [[ "$(jq -r '.status // empty' <<<"$service")" == SUCCESS ]] ||
    die "box '$box_id' is not ready; repair it first with: vmbox start $box_id"
  select_github_account
}

stop_box() {
  local service deployment_data active_deployment output status
  box_id="$1"
  validate_box_id "$box_id"
  service_name="$VMBOX_SERVICE_PREFIX$box_id"
  service="$(find_service)"
  if [[ -z "$service" ]]; then
    service_name="$box_id"
    service="$(find_service)"
  fi
  [[ -n "$service" ]] || die "box '$box_id' does not exist"

  status="$(jq -r '.status // "NO_DEPLOYMENT"' <<<"$service")"
  case "$status" in
    BUILDING|DEPLOYING|QUEUED|INITIALIZING|WAITING)
      die "box '$box_id' has a deployment in progress ($status); wait for it, then stop again"
      ;;
  esac

  deployment_data="$(railway deployment list "${target[@]}" \
    --service "$service_name" --limit 100 --json)"
  active_deployment="$(jq -r 'first(.[] | select(.status == "SUCCESS")) | .id // empty' \
    <<<"$deployment_data")"
  if [[ -z "$active_deployment" ]]; then
    echo "Box '$box_id' is already powered down (service and /data preserved)."
    return
  fi

  echo "vmbox: powering down '$service_name'; preserving its service and /data volume" >&2
  if ! output="$(railway down "${target[@]}" --service "$service_name" --yes 2>&1)"; then
    printf '%s\n' "$output" >&2
    die "could not power down box '$box_id'"
  fi
  echo "Box '$box_id' is powered down. Resume and redeploy with: vmbox resume $box_id"
  echo "Files persist, but tmux/Codex processes stopped. Reopen Codex with: codex resume --last"
}

open_box() {
  local requested_action="$1" service created=0 detached=0
  local -a remote_command
  box_id="$2"
  shift 2
  while (($#)); do
    case "$1" in
      -d|--detach) detached=1; shift ;;
      --) shift; break ;;
      *) break ;;
    esac
  done
  remote_command=("$@")
  validate_box_id "$box_id"
  service_name="$VMBOX_SERVICE_PREFIX$box_id"
  service="$(find_service)"

  if [[ -z "$service" && "$requested_action" == resume ]]; then
    service_name="$box_id"
    service="$(find_service)"
  fi

  if [[ -z "$service" ]]; then
    [[ "$requested_action" == start ]] || die "box '$box_id' does not exist; use: vmbox start $box_id"
    created=1
  elif [[ "$(jq -r '.status // empty' <<<"$service")" == "" ]] &&
    ! service_has_deployment_history; then
    echo "vmbox: resuming incomplete first-time setup for '$service_name'" >&2
    created=1
  fi

  if ((created)) && ! select_setup_for_new_box; then
    echo "vmbox: box creation cancelled before provisioning" >&2
    return 0
  fi

  if [[ -z "$service" ]]; then
    create_service
    for _ in {1..10}; do
      service="$(find_service)"
      [[ -n "$service" ]] && break
      sleep 1
    done
    [[ -n "$service" ]] || die "created '$service_name' but could not discover it"
  fi

  if ((created)); then
    apply_selected_components
    apply_selected_region "$selected_region"
  fi
  ensure_ready "$service"
  ensure_persistent_data
  ((created == 0)) || apply_new_box_setup
  attach "$detached" "${remote_command[@]}"
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

clean_all_selected=0
declare -a clean_service_ids=() clean_service_names=()

select_clean_boxes() {
  local list selected=0 key rest i marker all_checked=0 box service_id name status
  local -a rows checked
  list="$(services)"
  mapfile -t rows < <(jq -r --arg prefix "$VMBOX_SERVICE_PREFIX" '
    .[] |
    (.name | if startswith($prefix) then .[($prefix | length):] else . end) as $box |
    [.id, .name, (.status // "NO_DEPLOYMENT"), $box] | @tsv
  ' <<<"$list")
  ((${#rows[@]})) || die "no boxes available to select; use --all to remove orphan volumes"
  [[ -t 0 && -t 1 ]] || die "interactive clean requires a terminal; specify box IDs or --all"
  checked=(0)
  for _ in "${rows[@]}"; do checked+=(0); done
  while true; do
    printf '\033[2J\033[HChoose boxes to permanently delete\n'
    echo "↑/↓ or j/k: move  Space: toggle  Enter: confirm  q: cancel"
    echo "Deleting a box also deletes its persistent /data volume."
    echo
    ((checked[0])) && marker=x || marker=' '
    if ((selected == 0)); then
      printf '\033[1;36m> [%s] ALL BOXES\033[0m\n' "$marker"
    else
      printf '  [%s] ALL BOXES\n' "$marker"
    fi
    for i in "${!rows[@]}"; do
      IFS=$'\t' read -r service_id name status box <<<"${rows[$i]}"
      ((checked[i + 1])) && marker=x || marker=' '
      if ((selected == i + 1)); then
        printf '\033[1;36m> [%s] %-28s %-14s %s\033[0m\n' "$marker" "$name" "$status" "$box"
      else
        printf '  [%s] %-28s %-14s %s\n' "$marker" "$name" "$status" "$box"
      fi
    done
    IFS= read -rsn1 key || return 1
    case "$key" in
      ' ')
        if ((selected == 0)); then
          ((checked[0])) && all_checked=0 || all_checked=1
          for i in "${!checked[@]}"; do checked[i]=$all_checked; done
        else
          ((checked[selected])) && checked[selected]=0 || checked[selected]=1
          checked[0]=1
          for ((i = 1; i < ${#checked[@]}; i++)); do
            ((checked[i])) || { checked[0]=0; break; }
          done
        fi
        ;;
      j) selected=$(((selected + 1) % ${#checked[@]})) ;;
      k) selected=$(((selected - 1 + ${#checked[@]}) % ${#checked[@]})) ;;
      '')
        clean_all_selected=0
        clean_service_ids=()
        clean_service_names=()
        for i in "${!rows[@]}"; do
          ((checked[i + 1])) || continue
          IFS=$'\t' read -r service_id name status box <<<"${rows[$i]}"
          clean_service_ids+=("$service_id")
          clean_service_names+=("$name")
        done
        ((checked[0])) && clean_all_selected=1
        printf '\033[2J\033[H'
        ((${#clean_service_ids[@]})) || { echo "vmbox: clean cancelled (nothing selected)" >&2; return 1; }
        return 0
        ;;
      q) printf '\033[2J\033[H'; echo "vmbox: clean cancelled" >&2; return 1 ;;
      $'\e')
        rest=""
        IFS= read -rsn2 -t 0.1 rest || true
        case "$rest" in
          '[A') selected=$(((selected - 1 + ${#checked[@]}) % ${#checked[@]})) ;;
          '[B') selected=$(((selected + 1) % ${#checked[@]})) ;;
          *) printf '\033[2J\033[H'; echo "vmbox: clean cancelled" >&2; return 1 ;;
        esac
        ;;
    esac
  done
}

add_clean_box_by_id() {
  local requested="$1" service i
  validate_box_id "$requested"
  service_name="$VMBOX_SERVICE_PREFIX$requested"
  service="$(find_service)"
  if [[ -z "$service" ]]; then
    service_name="$requested"
    service="$(find_service)"
  fi
  [[ -n "$service" ]] || die "box '$requested' does not exist"
  for i in "${!clean_service_ids[@]}"; do
    [[ "${clean_service_ids[$i]}" == "$(jq -r '.id' <<<"$service")" ]] && return 0
  done
  clean_service_ids+=("$(jq -r '.id' <<<"$service")")
  clean_service_names+=("$(jq -r '.name' <<<"$service")")
}

clean_boxes() {
  local yes=0 all=0 arg answer i service_count volume_count
  local list volume_list volume_id service_id
  local -a requested=() clean_volume_ids=()
  for arg in "$@"; do
    case "$arg" in
      --yes) yes=1 ;;
      --all) all=1 ;;
      --*) die "unknown clean option '$arg'" ;;
      *) requested+=("$arg") ;;
    esac
  done
  ((all == 0 || ${#requested[@]} == 0)) || die "use either --all or named boxes, not both"

  clean_service_ids=()
  clean_service_names=()
  if ((all)); then
    list="$(services)"
    mapfile -t clean_service_ids < <(jq -r '.[].id' <<<"$list")
    mapfile -t clean_service_names < <(jq -r '.[].name' <<<"$list")
  elif ((${#requested[@]})); then
    for arg in "${requested[@]}"; do add_clean_box_by_id "$arg"; done
  else
    select_clean_boxes || return 0
    ((clean_all_selected == 0)) || all=1
  fi

  volume_list="$(volumes)"
  if ((all)); then
    mapfile -t clean_volume_ids < <(jq -r '.volumes[].id' <<<"$volume_list")
  else
    for i in "${!clean_service_names[@]}"; do
      while IFS= read -r volume_id; do
        [[ -n "$volume_id" ]] || continue
        clean_volume_ids+=("$volume_id")
      done < <(jq -r --arg name "${clean_service_names[$i]}" \
        '.volumes[] | select(.serviceName == $name) | .id' <<<"$volume_list")
    done
  fi

  service_count="${#clean_service_ids[@]}"
  volume_count="${#clean_volume_ids[@]}"
  if ((service_count == 0 && volume_count == 0)); then
    echo "No selected services or active persistent volumes to delete."
    return 0
  fi
  if ((service_count)); then
    echo "Services:" >&2
    for i in "${!clean_service_ids[@]}"; do
      printf '  %s (%s)\n' "${clean_service_names[$i]}" "${clean_service_ids[$i]}" >&2
    done
  fi
  if ((volume_count)); then
    echo "Persistent volumes and /data:" >&2
    for volume_id in "${clean_volume_ids[@]}"; do
      jq -r --arg id "$volume_id" '.volumes[] | select(.id == $id) |
        "  \(.name) (\(.id), \(.currentSizeMB // 0) MB)"' <<<"$volume_list" >&2
    done
  fi
  if ((yes == 0)); then
    [[ -t 0 ]] || die "confirmation required; rerun with --yes"
    read -rp "Permanently delete $service_count service(s) and $volume_count volume(s)? Type 'clean': " answer
    [[ "$answer" == clean ]] || die "cancelled"
  fi

  for service_id in "${clean_service_ids[@]}"; do
    railway service delete "${target[@]}" --service "$service_id" --yes --json >/dev/null
  done
  for volume_id in "${clean_volume_ids[@]}"; do
    delete_volume_if_active "$volume_id"
  done
  echo "Deleted $service_count service(s) and $volume_count active persistent volume(s)."
}

if (($# == 0)); then
  quick_usage
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

  cost)
    [[ $# -le 2 ]] || die "usage: vmbox cost [box-id]"
    show_cost "${2:-}"
    ;;

  resize)
    [[ $# -le 2 ]] || die "usage: vmbox resize [box-id]"
    resize_box "${2:-}"
    ;;

  auth)
    [[ $# -eq 2 ]] || die "usage: vmbox auth <box-id>"
    copy_auth_to_box "$2"
    ;;

  github)
    [[ $# -eq 2 ]] || die "usage: vmbox github <box-id>"
    sync_github_to_box "$2"
    ;;

  stop)
    [[ $# -eq 2 ]] || die "usage: vmbox stop <box-id>"
    stop_box "$2"
    ;;

  new|start|resume)
    [[ $# -ge 2 ]] || die "usage: vmbox $action <box-id> [--detach] [-- COMMAND [ARG...]]"
    [[ "$action" == new ]] && action=start
    open_box "$action" "$2" "${@:3}"
    ;;

  clean)
    clean_boxes "${@:2}"
    ;;

  *)
    if (($# >= 1)); then
      open_box start "$action" "${@:2}"
    else
      usage >&2
      exit 2
    fi
    ;;
esac
