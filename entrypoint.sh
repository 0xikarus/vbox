#!/usr/bin/env bash

set -euo pipefail

export HOME="${HOME:-/data/home}"
# This script runs both as the container entrypoint (root) and, through
# `--configure-agent-trust`, as the unprivileged vmbox user. Ownership fixes are
# therefore best-effort: as root they repair a fresh volume, and as vmbox they
# are already satisfied.
own_as_workload() {
  id -u vmbox >/dev/null 2>&1 || return 0
  chown vmbox:vmbox "$@" 2>/dev/null || true
}
# Railway mounts a newly created volume with a root-owned mount point.
own_as_workload /data
unset GH_TOKEN GITHUB_TOKEN
mkdir -p "$HOME" "$HOME/bin"

configure_agent_trust() {
  local workspace="${1:-/data/workspace}"
  local codex_dir="$HOME/.codex" claude_dir="$HOME/.claude"
  local codex_config="$codex_dir/config.toml" claude_settings="$claude_dir/settings.json"
  local claude_state="$HOME/.claude.json"
  local tmp

  mkdir -p "$codex_dir" "$claude_dir"
  chmod 700 "$codex_dir" "$claude_dir"
  touch "$codex_config"
  chmod 600 "$codex_config"
  tmp="$(mktemp "$codex_dir/.config.toml.XXXXXX")"
  awk -v target="[projects.\"$workspace\"]" '
    BEGIN {
      in_top=1
      in_target=0
      found=0
      wrote=0
      print "approval_policy = \"never\""
      print "sandbox_mode = \"danger-full-access\""
    }
    $0 ~ /^\[/ {
      if (in_target && !wrote) print "trust_level = \"trusted\""
      in_top=0
      in_target=($0 == target)
      if (in_target) { found=1; wrote=0 }
      print
      next
    }
    in_top && $0 ~ /^[[:space:]]*(approval_policy|sandbox_mode)[[:space:]]*=/ { next }
    in_target && $0 ~ /^[[:space:]]*trust_level[[:space:]]*=/ {
      if (!wrote) print "trust_level = \"trusted\""
      wrote=1
      next
    }
    { print }
    END {
      if (in_target && !wrote) print "trust_level = \"trusted\""
      if (!found) {
        print ""
        print target
        print "trust_level = \"trusted\""
      }
    }
  ' "$codex_config" > "$tmp"
  chmod 600 "$tmp"
  mv -f "$tmp" "$codex_config"

  if [[ ! -e "$claude_settings" ]]; then
    jq -nc --arg dir "$workspace" \
      '{permissions: {defaultMode: "bypassPermissions"}, trustedDirectories: [$dir]}' > "$claude_settings"
  elif jq -e 'type == "object"' "$claude_settings" >/dev/null 2>&1; then
    tmp="$(mktemp "$claude_dir/.settings.json.XXXXXX")"
    jq --arg dir "$workspace" \
      '.trustedDirectories = (((.trustedDirectories // []) + [$dir]) | unique)
       | .permissions = ((.permissions // {}) | .defaultMode = "bypassPermissions")' \
      "$claude_settings" > "$tmp"
    chmod 600 "$tmp"
    mv -f "$tmp" "$claude_settings"
  else
    echo "vmbox: warning: $claude_settings is not valid JSON; agent defaults were not changed" >&2
  fi

  if [[ ! -e "$claude_state" ]]; then
    printf '{}\n' > "$claude_state"
  fi
  if jq -e 'type == "object"' "$claude_state" >/dev/null 2>&1; then
    tmp="$(mktemp "$HOME/.claude.json.XXXXXX")"
    jq --arg dir "$workspace" \
      '.projects = (.projects // {})
       | .projects[$dir] = ((.projects[$dir] // {}) | .hasTrustDialogAccepted = true)' \
      "$claude_state" > "$tmp"
    chmod 600 "$tmp"
    mv -f "$tmp" "$claude_state"
  else
    echo "vmbox: warning: $claude_state is not valid JSON; Claude workspace trust was not changed" >&2
  fi
  chmod 600 "$codex_config" "$claude_settings" "$claude_state" 2>/dev/null || true
  own_as_workload "$codex_dir" "$claude_dir" "$codex_config" "$claude_settings" "$claude_state"
}

if [[ "${1:-}" == --configure-agent-trust ]]; then
  configure_agent_trust "${2:-/data/workspace}"
  exit 0
fi

mkdir -p /data/workspace
configure_agent_trust "${VMBOX_WORKSPACE:-/data/workspace}"

profile="$HOME/.profile"
bashrc="$HOME/.bashrc"

touch "$profile" "$bashrc"
if [[ -f /etc/vmbox/tmux.conf ]]; then
  if [[ ! -e "$HOME/.tmux.conf" ]] || grep -q '^# vmbox managed tmux configuration' "$HOME/.tmux.conf" 2>/dev/null; then
    cp /etc/vmbox/tmux.conf "$HOME/.tmux.conf"
    chmod 600 "$HOME/.tmux.conf"
  fi
fi

sed -i \
  -e '/^# vmbox-service environment$/d' \
  -e '/^# end vmbox-service environment$/d' \
  -e '/^export HOME=\/data\/home$/d' \
  -e '/^export PATH=\/data\/home\/bin:\/data\/home\/\.local\/bin:\/opt\/bun\/bin:\/opt\/foundry\/bin:\$PATH$/d' \
  -e '/^export PATH=\/data\/home\/bin:\/data\/home\/\.local\/bin:\/opt\/foundry\/bin:\$PATH$/d' \
  -e '/^export BUN_INSTALL=\/opt\/bun$/d' \
  -e '/^export FOUNDRY_DIR=\/opt\/foundry$/d' "$profile"
{
  echo '# vmbox-service environment'
  echo 'export HOME=/data/home'
  echo 'export PATH=/data/home/bin:/data/home/.local/bin:/opt/bun/bin:/opt/foundry/bin:$PATH'
  echo 'export BUN_INSTALL=/opt/bun'
  echo 'export FOUNDRY_DIR=/opt/foundry'
  # GUI commands from new interactive shells share the VNC display.
  if command -v Xtigervnc >/dev/null 2>&1 && ! grep -q '^export DISPLAY=' "$profile"; then
    echo 'export DISPLAY=${DISPLAY:-:99}'
  fi
  echo '# end vmbox-service environment'
} >> "$profile"
if ! grep -q '/data/home/.profile' "$bashrc"; then
  echo '[ -f /data/home/.profile ] && . /data/home/.profile' >> "$bashrc"
fi
own_as_workload "$HOME" "$HOME/bin" /data/workspace "$profile" "$bashrc" "$HOME/.tmux.conf"

if [[ $# -gt 0 ]]; then
	exec sudo -n -H -u vmbox -- env HOME=/data/home USER=vmbox LOGNAME=vmbox SHELL=/bin/bash PATH="/data/home/bin:/data/home/.local/bin:/opt/bun/bin:/opt/foundry/bin:$PATH" "$@"
fi

cd /
exec sudo -n -H -u vmbox -- env HOME=/data/home USER=vmbox LOGNAME=vmbox SHELL=/bin/bash PATH="/data/home/bin:/data/home/.local/bin:/opt/bun/bin:/opt/foundry/bin:$PATH" vmbox-runtime idle
