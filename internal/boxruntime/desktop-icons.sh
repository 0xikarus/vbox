#!/bin/sh
set -eu
mkdir -p "$HOME/.config/vmbox"
exec 9>"$HOME/.config/vmbox/desktop-icons.lock"
flock -n 9 || exit 0

desktop_dir="$(xdg-user-dir DESKTOP 2>/dev/null || true)"
# Some existing homes disable the Desktop directory by pointing it at HOME.
case "$desktop_dir" in
  ""|"$HOME") desktop_dir="$HOME/Desktop" ;;
esac
mkdir -p "$desktop_dir"
xdg-user-dirs-update --set DESKTOP "$desktop_dir"

launcher() {
  command -v "$2" >/dev/null 2>&1 || return 0
  target="$desktop_dir/vmbox-$2.desktop"
  # Preserve owner edits and do not follow or replace existing symlinks.
  if [ ! -e "$target" ] && [ ! -L "$target" ]; then
    (umask 077; set -C; cat >"$target" <<EOF
[Desktop Entry]
Type=Application
Name=$1
Exec=${4:-$2}
Icon=$3
Terminal=false
StartupNotify=true
EOF
    )
    chmod 700 "$target"
  fi
}
launcher Chromium chromium chromium "vmbox-runtime desktop-browser"
launcher Terminal xterm utilities-terminal
launcher Files pcmanfm system-file-manager
launcher Blender blender blender

export DISPLAY=:99
exec dbus-run-session -- pcmanfm --profile=vmbox --desktop
