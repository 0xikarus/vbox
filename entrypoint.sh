#!/usr/bin/env bash

set -euo pipefail

export HOME="${HOME:-/data/home}"
unset GH_TOKEN GITHUB_TOKEN
mkdir -p "$HOME" "$HOME/bin" /data/workspace

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

if [[ $# -gt 0 ]]; then
  exec "$@"
fi

exec sleep infinity
