#!/bin/sh
set -eu
umask 077
: "${VMBOX_FACTORY_DATA_DIR:?persistent Tasks data directory required}"
case "$VMBOX_FACTORY_DATA_DIR" in
  /*) test "$VMBOX_FACTORY_DATA_DIR" != / ;;
  *) echo 'Tasks data directory must be absolute' >&2; exit 1 ;;
esac
mkdir -p "$VMBOX_FACTORY_DATA_DIR/private"
chmod 0700 "$VMBOX_FACTORY_DATA_DIR/private"
if [ -n "${VMBOX_FACTORY_SSH_PRIVATE_KEY_B64:-}" ]; then
  task_key=$(mktemp "$VMBOX_FACTORY_DATA_DIR/private/.identity.XXXXXX")
  trap 'rm -f "$task_key"' EXIT HUP INT TERM
  printf '%s' "$VMBOX_FACTORY_SSH_PRIVATE_KEY_B64" | base64 -d > "$task_key"
  test -s "$task_key"
  chmod 0600 "$task_key"
  mv -f "$task_key" "$VMBOX_FACTORY_DATA_DIR/private/identity"
  trap - EXIT HUP INT TERM
  export VMBOX_FACTORY_SSH_IDENTITY="$VMBOX_FACTORY_DATA_DIR/private/identity"
  unset VMBOX_FACTORY_SSH_PRIVATE_KEY_B64
fi
export VMBOX_FACTORY_SSH_KNOWN_HOSTS="${VMBOX_FACTORY_SSH_KNOWN_HOSTS:-$VMBOX_FACTORY_DATA_DIR/private/known_hosts}"
export VMBOX_FACTORY_LISTEN="${VMBOX_FACTORY_LISTEN:-:${PORT:-8090}}"
exec /usr/local/bin/vmbox-factory
