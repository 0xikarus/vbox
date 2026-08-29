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
  vmbox <box-id> [-- COMMAND [ARG...]]
  vmbox new <box-id> [-- COMMAND [ARG...]]
  vmbox help
  vmbox list
  vmbox ls
  vmbox cost [box-id]
  vmbox auth <box-id>
  vmbox github <box-id>
  vmbox start <box-id> [-- COMMAND [ARG...]]
  vmbox resume <box-id> [-- COMMAND [ARG...]]
  vmbox stop <box-id>
  vmbox clean [--yes]

Each box is a Railway service named vmbox-<box-id>. `<box-id>`, `new`, and
`start` create, deploy, and attach to the service when it does not exist;
otherwise they attach to the existing box. `stop` removes its active deployment
but preserves the service and /data volume; `resume` deploys it again. Running
processes do not survive a stop. `clean` deletes every service and persistent
volume in the configured project after confirmation. `cost` shows accrued
costs for the current Railway billing period.

On a new box, choose one detected Codex profile and one Claude profile. Their
login and portable config files, including MCP settings, persist under /data.
`vmbox auth <box-id>` opens the same picker again.
A new box also offers a local GitHub CLI account. Its token, repository/org
permissions, HTTPS/SSH protocol, and Git commit identity are configured remotely.

Append `-- COMMAND [ARG...]` to start a command directly inside the tmux session.
Its standard input remains connected to the caller.

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

validate_box_id() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] ||
    die "invalid box ID '$1'; use letters, numbers, dots, dashes, or underscores"
}

services() { railway service list "${target[@]}" --json; }
volumes() {
  railway volume "${target[@]}" list --json | jq '
    .volumes = ((.volumes // []) |
      map(select(.isPendingDeletion != true and (.deletedAt // null) == null)))
  '
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

link_service() {
  local args=("${target[@]}") output
  [[ -n "${1:-}" ]] && args+=(--service "$1")
  if ! output="$(cd "$bundle" && railway link "${args[@]}" --json 2>&1)"; then
    printf '%s\n' "$output" >&2
    die "could not select the configured Railway project"
  fi
}

create_service() {
  local output
  [[ -f "$bundle/Dockerfile" ]] || die "deployment bundle missing; rerun install.sh"
  echo "vmbox: provisioning Railway service '$service_name'" >&2
  link_service ""
  if ! output="$(cd "$bundle" && railway add --service "$service_name" --json 2>&1)"; then
    printf '%s\n' "$output" >&2
    die "could not create Railway service '$service_name'"
  fi
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
  local service="$1" status deploy_result deployment_id output volume_added=0

  if ! jq -e 'any(.volumes[]?; .mountPath == "/data")' <<<"$service" >/dev/null; then
    echo "vmbox: attaching persistent /data volume" >&2
    link_service "$service_name"
    if ! output="$(cd "$bundle" && railway volume add --mount-path /data --json 2>&1)"; then
      printf '%s\n' "$output" >&2
      die "could not attach /data to '$service_name'"
    fi
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
  tmux_help
  echo >&2
  echo "Connecting now. Later, resume this box with: vmbox resume $box_id" >&2
  if (($#)); then
    echo "vmbox: starting forwarded command in tmux: $1" >&2
    railway ssh "${target[@]}" --service "$service_name" --session "$box_id" "$@"
  else
    railway ssh "${target[@]}" --service "$service_name" --session "$box_id"
  fi
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
    jq -r '.services[] |
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
  local selected=0 key rest i marker label contents
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
    echo "↑/↓ or j/k: move  Space: toggle  a: add path  Enter: upload  q: skip"
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
    IFS= read -rsn1 key || return
    case "$key" in
      ' ') toggle_profile_candidate "$selected" ;;
      a) add_custom_profile; selected=$((${#profile_sources[@]} - 1)) ;;
      j) selected=$(((selected + 1) % ${#profile_sources[@]})) ;;
      k) selected=$(((selected - 1 + ${#profile_sources[@]}) % ${#profile_sources[@]})) ;;
      '') printf '\033[2J\033[H'; copy_selected_profiles; return ;;
      q) printf '\033[2J\033[H'; echo "vmbox: agent profile upload skipped" >&2; return ;;
      $'\e')
        rest=""
        IFS= read -rsn2 -t 0.1 rest || true
        case "$rest" in
          '[A') selected=$(((selected - 1 + ${#profile_sources[@]}) % ${#profile_sources[@]})) ;;
          '[B') selected=$(((selected + 1) % ${#profile_sources[@]})) ;;
          *) printf '\033[2J\033[H'; echo "vmbox: agent profile upload skipped" >&2; return ;;
        esac
        ;;
    esac
  done
}
declare -a github_hosts=() github_users=() github_protocols=()

discover_github_accounts() {
  local output line host="" user protocol last=-1
  github_hosts=()
  github_users=()
  github_protocols=()
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
      last=$((${#github_users[@]} - 1))
    elif ((last >= 0)) && [[ "$line" =~ Git[[:space:]]operations[[:space:]]protocol:[[:space:]]+([^[:space:]]+) ]]; then
      protocol="${BASH_REMATCH[1]}"
      [[ "$protocol" == ssh || "$protocol" == https ]] && github_protocols[last]="$protocol"
    fi
  done <<<"$output"
  return 0
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
    "umask 077; export HOME=/data/home; gh auth login --hostname '$host' --git-protocol '$protocol' --with-token >/dev/null && gh auth setup-git --hostname '$host' >/dev/null; chmod 700 /data/home/.config/gh; chmod 600 /data/home/.config/gh/hosts.yml" >/dev/null; then
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
    "HOME=/data/home GH_HOST='$host' gh api user --jq .login" 2>/dev/null)"; then
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
  local selected=0 key rest i
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
    echo "↑/↓ or j/k: move  Enter: securely sync account/token  q: skip"
    echo "The selected token keeps its existing repository and organization permissions."
    echo
    for i in "${!github_users[@]}"; do
      if ((i == selected)); then
        printf '\033[1;36m> %-24s %-24s %s\033[0m\n' "${github_users[$i]}" "${github_hosts[$i]}" "${github_protocols[$i]}"
      else
        printf '  %-24s %-24s %s\n' "${github_users[$i]}" "${github_hosts[$i]}" "${github_protocols[$i]}"
      fi
    done
    IFS= read -rsn1 key || return 0
    case "$key" in
      j) selected=$(((selected + 1) % ${#github_users[@]})) ;;
      k) selected=$(((selected - 1 + ${#github_users[@]}) % ${#github_users[@]})) ;;
      '') printf '\033[2J\033[H'; upload_github_account "$selected"; return 0 ;;
      q) printf '\033[2J\033[H'; echo "vmbox: GitHub account sync skipped" >&2; return 0 ;;
      $'\e')
        rest=""
        IFS= read -rsn2 -t 0.1 rest || true
        case "$rest" in
          '[A') selected=$(((selected - 1 + ${#github_users[@]}) % ${#github_users[@]})) ;;
          '[B') selected=$(((selected + 1) % ${#github_users[@]})) ;;
          *) printf '\033[2J\033[H'; echo "vmbox: GitHub account sync skipped" >&2; return 0 ;;
        esac
        ;;
    esac
  done
}

select_credentials() {
  select_profiles
  select_github_account
}
select_credentials_for_new_box() {
  local tty_fd
  if [[ ! -t 0 ]] && { exec {tty_fd}<>/dev/tty; } 2>/dev/null; then
    select_credentials <&"$tty_fd" >&"$tty_fd"
    exec {tty_fd}>&-
  else
    select_credentials
  fi
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
  local requested_action="$1" service created=0
  local -a remote_command
  box_id="$2"
  shift 2
  if [[ "${1:-}" == -- ]]; then shift; fi
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
    create_service
    for _ in {1..10}; do
      service="$(find_service)"
      [[ -n "$service" ]] && break
      sleep 1
    done
    [[ -n "$service" ]] || die "created '$service_name' but could not discover it"
    created=1
  fi

  ensure_ready "$service"
  ((created == 0)) || select_credentials_for_new_box
  attach "${remote_command[@]}"
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

  cost)
    [[ $# -le 2 ]] || die "usage: vmbox cost [box-id]"
    show_cost "${2:-}"
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
    [[ $# -ge 2 ]] || die "usage: vmbox $action <box-id> [-- COMMAND [ARG...]]"
    [[ "$action" == new ]] && action=start
    open_box "$action" "$2" "${@:3}"
    ;;

  clean)
    [[ $# -eq 1 || ($# -eq 2 && "${2:-}" == --yes) ]] || die "usage: vmbox clean [--yes]"
    list="$(services)"
    volume_list="$(volumes)"
    count="$(jq 'length' <<<"$list")"
    volume_count="$(jq '.volumes | length' <<<"$volume_list")"
    if [[ "$count" == 0 && "$volume_count" == 0 ]]; then
      echo "No services or persistent volumes to delete."
      exit
    fi

    if ((count)); then
      echo "Services:" >&2
      jq -r '.[] | "  \(.name) (\(.id))"' <<<"$list" >&2
    fi
    if ((volume_count)); then
      echo "Persistent volumes and /data:" >&2
      jq -r '.volumes[] | "  \(.name) (\(.id), \(.currentSizeMB // 0) MB)"' <<<"$volume_list" >&2
    fi
    if [[ "${2:-}" != --yes ]]; then
      [[ -t 0 ]] || die "confirmation required; use: vmbox clean --yes"
      read -rp "Permanently delete $count service(s) and $volume_count volume(s), including all /data? Type 'clean': " answer
      [[ "$answer" == clean ]] || die "cancelled"
    fi

    while IFS= read -r service_id; do
      railway service delete "${target[@]}" --service "$service_id" --yes --json >/dev/null
    done < <(jq -r '.[].id' <<<"$list")

    # Refresh after deleting services so already-removed volumes are not targeted.
    volume_list="$(volumes)"
    while IFS= read -r volume_id; do
      delete_volume_if_active "$volume_id"
    done < <(jq -r '.volumes[].id' <<<"$volume_list")
    echo "Deleted $count service(s) and all active project volumes."
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
