#!/bin/sh
set -eu
mkdir -p "$HOME/.config/vmbox"
exec 8>"$HOME/.config/vmbox/desktop-config.lock"
flock 8

# libfm treats desktop entries as executable files and otherwise asks before
# opening them. Keep the owner's other file-manager preferences intact.
libfm_dir="$HOME/.config/libfm"
libfm_config="$libfm_dir/libfm.conf"
mkdir -p "$libfm_dir"
if [ ! -e "$libfm_config" ] && [ ! -L "$libfm_config" ]; then
  (umask 077; printf '[config]\nquick_exec=1\n' >"$libfm_config")
elif [ -f "$libfm_config" ] && [ ! -L "$libfm_config" ]; then
  if ! awk '
    /^\[config\]$/ { in_config = 1; next }
    /^\[/ { in_config = 0 }
    in_config && /^quick_exec=1$/ { found = 1 }
    END { exit !found }
  ' "$libfm_config"; then
    updated="$(mktemp "$libfm_dir/.libfm.conf.XXXXXX")"
    awk '
      /^\[config\]$/ { in_config = 1; had_config = 1; print; next }
      /^\[/ && in_config && !had_key { print "quick_exec=1"; had_key = 1 }
      /^\[/ { in_config = 0 }
      in_config && /^quick_exec=/ { if (!had_key) print "quick_exec=1"; had_key = 1; next }
      { print }
      END {
        if (in_config && !had_key) print "quick_exec=1"
        if (!had_config) print "[config]\nquick_exec=1"
      }
    ' "$libfm_config" >"$updated"
    chmod --reference="$libfm_config" "$updated"
    mv -f "$updated" "$libfm_config"
  fi
fi
flock -u 8

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
  elif [ "$2" = xterm ] && [ -f "$target" ] && [ ! -L "$target" ] &&
    [ "$(cat "$target")" = "$(printf '[Desktop Entry]\nType=Application\nName=Terminal\nExec=xterm\nIcon=utilities-terminal\nTerminal=false\nStartupNotify=true')" ]; then
    # Upgrade only the exact launcher written by older vmbox versions.
    updated="$(mktemp "$desktop_dir/.vmbox-xterm.XXXXXX")"
    sed 's/^Exec=xterm$/Exec=xterm -fa "DejaVu Sans Mono" -fs 13 -bg "#300a24" -fg "#eeeeec" -cr "#f07746"/' "$target" >"$updated"
    chmod --reference="$target" "$updated"
    mv -f "$updated" "$target"
  fi
}
launcher Chromium chromium chromium "vmbox-runtime desktop-browser"
launcher Terminal xterm utilities-terminal 'xterm -fa "DejaVu Sans Mono" -fs 13 -bg "#300a24" -fg "#eeeeec" -cr "#f07746"'
launcher Files pcmanfm system-file-manager
launcher Blender blender blender

export DISPLAY="${VMBOX_DESKTOP_DISPLAY:-:99}"
exec dbus-run-session -- pcmanfm --profile=vmbox --desktop
