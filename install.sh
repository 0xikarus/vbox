#!/usr/bin/env bash

set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
install_dir="${VMBOX_INSTALL_DIR:-$HOME/.local/bin}"
config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/vmbox"
env_file="$config_dir/env.sh"
update_shell=true
uninstall=false

usage() {
  cat <<'EOF'
Usage: ./install.sh [options]

Options:
  --no-shell-update  Install without editing a shell startup file
  --shell-rc PATH    Update this startup file instead of auto-detecting one
  --uninstall        Remove the CLI and managed shell integration
  -h, --help         Show this help

Environment:
  VMBOX_INSTALL_DIR  Binary directory (default: ~/.local/bin)
  XDG_CONFIG_HOME    Configuration root (default: ~/.config)
EOF
}

shell_rc="${VMBOX_SHELL_RC:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-shell-update)
      update_shell=false
      shift
      ;;
    --shell-rc)
      [[ $# -ge 2 ]] || { echo "Missing path after --shell-rc" >&2; exit 2; }
      shell_rc="$2"
      shift 2
      ;;
    --uninstall)
      uninstall=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "$shell_rc" ]]; then
  case "${SHELL:-/bin/bash}" in
    */zsh) shell_rc="${ZDOTDIR:-$HOME}/.zshrc" ;;
    *) shell_rc="$HOME/.bashrc" ;;
  esac
fi

source_line="[ -f \"$env_file\" ] && . \"$env_file\" # vmbox-service"

remove_source_line() {
  [[ -f "$shell_rc" ]] || return 0
  local filtered
  filtered="$(mktemp "${TMPDIR:-/tmp}/vmbox-rc.XXXXXX")"
  grep -Fvx "$source_line" "$shell_rc" > "$filtered" || true
  chmod --reference="$shell_rc" "$filtered" 2>/dev/null || chmod 0644 "$filtered"
  mv "$filtered" "$shell_rc"
}

if [[ "$uninstall" == true ]]; then
  rm -f "$install_dir/vmbox" "$env_file"
  remove_source_line
  echo "Removed vmbox and its managed shell integration."
  echo "Preserved configuration and box records."
  exit 0
fi

mkdir -p "$install_dir" "$config_dir"
install -m 0755 "$script_dir/vmbox.sh" "$install_dir/vmbox"

if [[ ! -f "$config_dir/config" ]]; then
  install -m 0600 "$script_dir/vmbox.conf.example" "$config_dir/config"
fi

printf 'export PATH=%q:$PATH\n' "$install_dir" > "$env_file"
chmod 0644 "$env_file"

if [[ "$update_shell" == true ]]; then
  mkdir -p "$(dirname "$shell_rc")"
  touch "$shell_rc"
  if ! grep -Fqx "$source_line" "$shell_rc"; then
    printf '\n%s\n' "$source_line" >> "$shell_rc"
  fi
fi

"$install_dir/vmbox" --help >/dev/null

echo "Installed vmbox at $install_dir/vmbox"
echo "Configuration: $config_dir/config"
if [[ "$update_shell" == true ]]; then
  echo "Shell integration: $shell_rc"
  echo "Open a new shell or run: source \"$shell_rc\""
else
  echo "Shell startup file unchanged (--no-shell-update)."
  echo "Add $install_dir to PATH to invoke vmbox by name."
fi
