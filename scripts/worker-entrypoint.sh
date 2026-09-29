#!/usr/bin/env bash

set -euo pipefail

if [ "${1:-}" = "/usr/local/bin/vmbox-shared-worker" ]; then
  exec "$@"
fi
if [ "${VMBOX_WORKER_MODE:-}" = "shared" ]; then
  exec /usr/local/bin/vmbox-shared-worker
fi

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

# Keep agent trust configuration separate from worker lifecycle setup.
source "$(dirname -- "${BASH_SOURCE[0]}")/worker-agent-trust.sh"

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

# Enrollment is provisioned privately outside the workspace. Starting the agent
# is additive; its supervisor only manages its own child and never restarts tmux.
if [[ -f /var/lib/vmbox-worker/config.json && -x /usr/local/bin/vmbox-worker-agent ]]; then
  /usr/local/bin/vmbox-worker-agent --supervise --config /var/lib/vmbox-worker/config.json </dev/null &
fi

if [[ $# -gt 0 ]]; then
	exec sudo -n -H -u vmbox -- env HOME=/data/home USER=vmbox LOGNAME=vmbox SHELL=/bin/bash PATH="/data/home/bin:/data/home/.local/bin:/opt/bun/bin:/opt/foundry/bin:$PATH" "$@"
fi

cd /
exec sudo -n -H -u vmbox -- env HOME=/data/home USER=vmbox LOGNAME=vmbox SHELL=/bin/bash PATH="/data/home/bin:/data/home/.local/bin:/opt/bun/bin:/opt/foundry/bin:$PATH" vmbox-runtime idle
