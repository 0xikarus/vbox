#!/usr/bin/env bash

set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
install_dir="${VMBOX_INSTALL_DIR:-$HOME/.local/bin}"
config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/vmbox"

mkdir -p "$install_dir" "$config_dir"
install -m 0755 "$script_dir/vmbox.sh" "$install_dir/vmbox"

if [[ ! -f "$config_dir/config" ]]; then
  install -m 0600 "$script_dir/vmbox.conf.example" "$config_dir/config"
fi

echo "Installed vmbox at $install_dir/vmbox"
echo "Configuration: $config_dir/config"

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) echo "Add this to ~/.bashrc: export PATH=\"$install_dir:\$PATH\"" ;;
esac
