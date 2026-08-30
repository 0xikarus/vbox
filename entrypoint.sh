#!/usr/bin/env bash

set -euo pipefail

export HOME="${HOME:-/data/home}"
unset GH_TOKEN GITHUB_TOKEN
mkdir -p "$HOME" "$HOME/bin" /data/workspace

configure_agent_trust() {
  local codex_dir="$HOME/.codex" claude_dir="$HOME/.claude"
  local codex_config="$codex_dir/config.toml" claude_settings="$claude_dir/settings.json"
  local tmp

  mkdir -p "$codex_dir" "$claude_dir"
  touch "$codex_config"
  tmp="$(mktemp)"
  awk -v target='[projects."/data/workspace"]' '
    BEGIN { in_target=0; found=0; wrote=0 }
    $0 ~ /^\[/ {
      if (in_target && !wrote) print "trust_level = \"trusted\""
      in_target=($0 == target)
      if (in_target) { found=1; wrote=0 }
      print
      next
    }
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
  install -m 600 "$tmp" "$codex_config"
  rm -f "$tmp"

  if [[ ! -e "$claude_settings" ]]; then
    printf '{"trustedDirectories":["/data/workspace"]}\n' > "$claude_settings"
  elif jq -e 'type == "object"' "$claude_settings" >/dev/null 2>&1; then
    tmp="$(mktemp)"
    jq --arg dir /data/workspace \
      '.trustedDirectories = (((.trustedDirectories // []) + [$dir]) | unique)' \
      "$claude_settings" > "$tmp"
    install -m 600 "$tmp" "$claude_settings"
    rm -f "$tmp"
  else
    echo "vmbox: warning: $claude_settings is not valid JSON; trust entry was not changed" >&2
  fi
  chmod 600 "$codex_config" "$claude_settings" 2>/dev/null || true
}

configure_agent_trust

profile="$HOME/.profile"
bashrc="$HOME/.bashrc"

touch "$profile" "$bashrc"

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
  echo '# end vmbox-service environment'
} >> "$profile"
if ! grep -q '/data/home/.profile' "$bashrc"; then
  echo '[ -f /data/home/.profile ] && . /data/home/.profile' >> "$bashrc"
fi

if [[ "${1:-}" == --configure-agent-trust ]]; then
  exit 0
fi

if [[ $# -gt 0 ]]; then
  exec "$@"
fi

exec sleep infinity
