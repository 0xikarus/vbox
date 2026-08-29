#!/usr/bin/env bash

set -euo pipefail

root="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
bin="${VMBOX_INSTALL_DIR:-$HOME/.local/bin}"
config="${XDG_CONFIG_HOME:-$HOME/.config}/vmbox"
credentials="$config/credentials"
bundle="${XDG_DATA_HOME:-$HOME/.local/share}/vmbox/service"
rc="${VMBOX_SHELL_RC:-}"
update_rc=1
save_token=0
uninstall=0

usage() {
  echo "Usage: ./install.sh [--workspace-token] [--no-shell-update] [--shell-rc PATH] [--uninstall]"
}

while (($#)); do
  case "$1" in
    --workspace-token) save_token=1 ;;
    --no-shell-update) update_rc=0 ;;
    --shell-rc) shift; rc="${1:?Missing path after --shell-rc}" ;;
    --uninstall) uninstall=1 ;;
    -h|--help) usage; exit ;;
    *) usage >&2; echo "Unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done

if [[ -z "$rc" ]]; then
  [[ "${SHELL:-}" == */zsh ]] && rc="${ZDOTDIR:-$HOME}/.zshrc" || rc="$HOME/.bashrc"
fi

path_line="export PATH=\"$bin:\$PATH\" # vmbox-service"

clean_rc() {
  [[ -f "$rc" ]] || return
  local tmp
  tmp="$(mktemp)"
  grep -Fv '# vmbox-service' "$rc" > "$tmp" || true
  cat "$tmp" > "$rc"
  rm -f "$tmp"
}

if ((uninstall)); then
  rm -f "$bin/vmbox" "$config/env.sh" "$credentials"
  rm -f "$bundle/Dockerfile" "$bundle/entrypoint.sh" "$bundle/railway.json" "$bundle/.dockerignore"
  rm -f "$bundle/.railway/config.json"
  rmdir "$bundle/.railway" "$bundle" 2>/dev/null || true
  clean_rc
  echo "Removed vmbox, its deployment bundle, and token; configuration was preserved."
  exit
fi

mkdir -p "$bin" "$config" "$bundle"
install -m 755 "$root/vmbox.sh" "$bin/vmbox"
install -m 755 "$root/entrypoint.sh" "$bundle/entrypoint.sh"
install -m 644 "$root/Dockerfile" "$root/railway.json" "$root/.dockerignore" "$bundle/"
[[ -f "$config/config" ]] || install -m 600 "$root/vmbox.conf.example" "$config/config"

if ((save_token)); then
  token="${RAILWAY_API_TOKEN:-}"
  if [[ -z "$token" ]]; then
    [[ -t 0 ]] || { echo "Run this option in a terminal to enter the token securely." >&2; exit 1; }
    read -rsp "Railway workspace token: " token
    echo
  fi
  [[ -n "$token" ]] || { echo "Token cannot be empty." >&2; exit 1; }
  if ! (
    unset RAILWAY_TOKEN
    export RAILWAY_API_TOKEN="$token"
    . "$config/config"
    railway service list \
      --project "$VMBOX_PROJECT_ID" \
      --environment "$VMBOX_ENVIRONMENT_ID" \
      --json >/dev/null
  ); then
    echo "Token was rejected or cannot access the configured Railway project." >&2
    echo "No credential was changed." >&2
    exit 1
  fi
  printf 'export RAILWAY_API_TOKEN=%q\n' "$token" > "$credentials"
  chmod 600 "$credentials"
  unset token
fi

if ((update_rc)); then
  mkdir -p "$(dirname -- "$rc")"
  touch "$rc"
  clean_rc
  printf '\n%s\n' "$path_line" >> "$rc"
fi

"$bin/vmbox" --help >/dev/null
echo "Installed $bin/vmbox"
echo "Configuration: $config/config"
((save_token)) && echo "Workspace token: $credentials (mode 0600)"
((update_rc)) && echo "Open a new shell or run: source \"$rc\"" || echo "Add $bin to PATH."
