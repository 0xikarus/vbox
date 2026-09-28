#!/usr/bin/env bash

# Sourced by worker-entrypoint.sh after own_as_workload is defined.
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
