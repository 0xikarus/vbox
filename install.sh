#!/usr/bin/env bash

set -euo pipefail

root="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
bin="${VMBOX_INSTALL_DIR:-$HOME/.local/bin}"
config="${XDG_CONFIG_HOME:-$HOME/.config}/vmbox"
credentials="$config/credentials"
bundle="${XDG_DATA_HOME:-$HOME/.local/share}/vmbox/service"
runtime_assets="${XDG_DATA_HOME:-$HOME/.local/share}/vmbox/runtime"
rc="${VMBOX_SHELL_RC:-}"
update_rc=1
save_token=0
uninstall=0
go_cli=0

usage() {
  echo "Usage: ./install.sh [--go-cli] [--workspace-token] [--no-shell-update] [--shell-rc PATH] [--uninstall]"
}

update_railway() {
  command -v railway >/dev/null 2>&1 || {
    echo "Railway CLI is required: https://docs.railway.com/guides/cli" >&2
    exit 1
  }

  echo "Updating Railway CLI..."
  railway upgrade --yes >/dev/null 2>&1 || true
  if railway usage projects --help >/dev/null 2>&1; then
    echo "Railway CLI: $(railway --version)"
    return
  fi

  # Some older global installs remain pinned after Railway's self-upgrade.
  if command -v bun >/dev/null 2>&1; then
    bun add -g @railway/cli@latest
  elif command -v npm >/dev/null 2>&1; then
    npm install --global @railway/cli@latest
  else
    echo "Railway CLI is too old and neither Bun nor npm is available to update it." >&2
    exit 1
  fi

  railway usage projects --help >/dev/null 2>&1 || {
    echo "The active Railway CLI is still too old: $(command -v railway)" >&2
    exit 1
  }
  echo "Railway CLI: $(railway --version)"
}

while (($#)); do
  case "$1" in
    --workspace-token) save_token=1 ;;
    --go-cli) go_cli=1 ;;
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

printf -v quoted_bin '%q' "$bin"
path_line="export PATH=$quoted_bin:\$PATH # vmbox-service"

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
	rm -f "$runtime_assets/vmbox-runtime-linux-amd64" "$runtime_assets/vmbox-runtime-linux-arm64" "$runtime_assets/vmbox-entrypoint"
	rmdir "$runtime_assets" 2>/dev/null || true
	rm -f "${XDG_DATA_HOME:-$HOME/.local/share}/vmbox/railway-ssh/ssh" "$config/railway-known-hosts"
	rmdir "${XDG_DATA_HOME:-$HOME/.local/share}/vmbox/railway-ssh" 2>/dev/null || true
  rm -f "$bundle/Dockerfile" "$bundle/entrypoint.sh" "$bundle/railway.json" "$bundle/.dockerignore"
  rm -f "$bundle/.railway/config.json"
  rmdir "$bundle/.railway" "$bundle" 2>/dev/null || true
  clean_rc
  echo "Removed vmbox, its deployment bundle, and token; configuration was preserved."
  exit
fi

if ((go_cli == 0 || save_token)); then
  update_railway
fi

mkdir -p "$bin" "$config" "$bundle" "$runtime_assets"
chmod 700 "$config"
if ((go_cli)); then
  command -v go >/dev/null 2>&1 || {
    echo "Go 1.26 or newer is required for --go-cli." >&2
    exit 1
  }
  go_tmp="$(mktemp "$bin/.vmbox-go.XXXXXX")"
  if ! (cd "$root" && go build -trimpath -o "$go_tmp" ./cmd/vmbox); then
    rm -f "$go_tmp"
    exit 1
  fi
	chmod 755 "$go_tmp"
	mv -f "$go_tmp" "$bin/vmbox"
	for runtime_arch in amd64 arm64; do
		runtime_tmp="$(mktemp "$runtime_assets/.vmbox-runtime-linux-$runtime_arch.XXXXXX")"
		if ! (cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH="$runtime_arch" go build -trimpath -ldflags='-s -w' -o "$runtime_tmp" ./cmd/vmbox-runtime); then
			rm -f "$runtime_tmp"
			exit 1
		fi
		chmod 755 "$runtime_tmp"
		mv -f "$runtime_tmp" "$runtime_assets/vmbox-runtime-linux-$runtime_arch"
	done
	install -m 755 "$root/entrypoint.sh" "$runtime_assets/vmbox-entrypoint"
else
  install -m 755 "$root/vmbox.sh" "$bin/vmbox"
fi
install -m 755 "$root/entrypoint.sh" "$bundle/entrypoint.sh"
install -m 644 "$root/Dockerfile" "$root/.dockerignore" "$bundle/"
# Remove deprecated Config as Code left by older vmbox installations.
rm -f "$bundle/railway.json" "$bundle/railway.toml"
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
  credential_tmp="$(mktemp "$config/.credentials.XXXXXX")"
  printf 'export RAILWAY_API_TOKEN=%q\n' "$token" > "$credential_tmp"
  chmod 600 "$credential_tmp"
  mv -f "$credential_tmp" "$credentials"
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
echo "Configuration: $config/config.json"
((save_token)) && echo "Workspace token: $credentials (mode 0600)"
((update_rc)) && echo "Open a new shell or run: source \"$rc\"" || echo "Add $bin to PATH."

cat <<'EOF'

Quick start:
  vmbox new <name>

To leave Codex running inside tmux:
  1. Press Ctrl-a
  2. Release both keys
  3. Press d

Reconnect later:
  vmbox <name>
  vmbox resume        # selectable list

Check a detached task:
  vmbox task-status <name> [run-id]

Power down compute but preserve /data:
  vmbox stop <box-id>

After a power-down, tmux processes are gone; use `codex resume --last`.

Run `vmbox help` at any time for the complete command guide.
EOF
