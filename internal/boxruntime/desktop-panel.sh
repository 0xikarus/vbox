#!/bin/sh
# Run alongside the existing X server and window manager; never restart either.
set -eu
command -v tint2 >/dev/null 2>&1 || exit 0
panel_dir="${HOME}/.config/vmbox"
mkdir -p "$panel_dir"
# The kernel releases this lock when the panel exits, including after a crash.
exec 9>"$panel_dir/desktop-panel.lock"
flock -n 9 || exit 0
panel_config="$panel_dir/desktop-panel.tint2rc"
if [ ! -e "$panel_config" ]; then
cat >"$panel_config" <<'CONFIG'
# vmbox desktop taskbar; editable by the workspace owner.
rounded = 0
border_width = 0
background_color = #20242b 100
rounded = 4
border_width = 0
background_color = #3b74ad 100
panel_items = T
panel_size = 100% 36
panel_position = bottom center horizontal
panel_padding = 4 3 4
panel_background_id = 1
panel_layer = top
strut_policy = follow_size
autohide = 0
wm_menu = 1
taskbar_mode = single_desktop
taskbar_padding = 0 0 4
taskbar_hide_inactive_tasks = 0
taskbar_hide_different_desktop = 0
task_text = 1
task_icon = 1
task_centered = 0
task_maximum_size = 220 30
task_padding = 8 4 4
task_font = Sans 10
task_font_color = #dddddd 100
task_active_font_color = #ffffff 100
task_active_background_id = 2
task_tooltip = 1
mouse_left = toggle_iconify
mouse_middle = none
mouse_right = none
mouse_scroll_up = prev_task
mouse_scroll_down = next_task
CONFIG
fi
export DISPLAY="${VMBOX_DESKTOP_DISPLAY:-:99}"
exec tint2 -c "$panel_config"
