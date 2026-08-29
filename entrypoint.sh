#!/usr/bin/env bash

set -euo pipefail

export HOME="${HOME:-/data/home}"
mkdir -p "$HOME" "$HOME/bin" /data/workspace

profile="$HOME/.profile"
bashrc="$HOME/.bashrc"

touch "$profile" "$bashrc"

if ! grep -q '# vmbox-service environment' "$profile"; then
  {
    echo '# vmbox-service environment'
    echo 'export HOME=/data/home'
    echo 'export PATH=/data/home/bin:/data/home/.local/bin:/opt/foundry/bin:$PATH'
    echo 'export FOUNDRY_DIR=/opt/foundry'
  } >> "$profile"
fi

if ! grep -q '/data/home/.profile' "$bashrc"; then
  echo '[ -f /data/home/.profile ] && . /data/home/.profile' >> "$bashrc"
fi

if [[ $# -gt 0 ]]; then
  exec "$@"
fi

exec sleep infinity
