#!/usr/bin/env python3
"""Add hub Start/Stop actions to the existing password-protected PGX portal."""
import ast
import re
from pathlib import Path

ACTIONS = 'SERVICE_ACTIONS["vbox"] = {"start": ("Start hub", None, False), "stop": ("Stop hub", None, True)}\n'
HELPERS = '''def installed_vbox_hub():
    config = json.loads(open(os.path.expanduser("~/.local/share/vbox-hub/hub.json")).read())
    hub_id = config["id"]
    if not re.fullmatch(r"hub-[a-f0-9]{20}", hub_id):
        raise RuntimeError("Invalid installed hub identity")
    name = "vbox-" + hub_id
    result = subprocess.run(["docker", "inspect", name], capture_output=True, text=True, timeout=10)
    if result.returncode:
        raise RuntimeError("Hub is not installed")
    container = json.loads(result.stdout)[0]
    if container["Config"].get("Labels", {}).get("dev.vmbox.hub") != hub_id:
        raise RuntimeError("Hub ownership check failed")
    return name


def start_vbox_hub():
    # Start only this installed hub; never change Docker's restart policy.
    name = installed_vbox_hub()
    subprocess.run(["docker", "start", name], check=True, capture_output=True, timeout=30)
    for _ in range(30):
        try:
            port_open(8091)
            return "vbox Hub started: http://vbox.pgx.local/"
        except OSError:
            time.sleep(1)
    raise RuntimeError("Hub started but is not responding yet")


def stop_vbox_hub():
    # Stop the manager only, matching `vbox hub shutdown`; keep all workers/data.
    name = installed_vbox_hub()
    subprocess.run(["docker", "stop", "--time", "30", name], check=True, capture_output=True, timeout=45)
    return "vbox Hub stopped. Worker boxes and files are retained. Use Start hub to resume."


'''
DISPATCH = '''            if svc == "vbox" and act in {"start", "stop"}:
                fn = start_vbox_hub if act == "start" else stop_vbox_hub
                return self.reply(200, str(run_action(f"vbox: {act}", fn=fn)))
'''
LEGACY_DISPATCH = '''            if (svc, act) == ("vbox", "start"):
                return self.reply(200, str(run_action("vbox: start", fn=start_vbox_hub)))
'''


def patch(source):
    assert 'if self.path == "/api/action":' in source
    if 'SERVICE_ACTIONS["vbox"] = ' in source:
        source, count = re.subn(r'^SERVICE_ACTIONS\["vbox"\] = [^\n]*\n', lambda _: ACTIONS, source, count=1, flags=re.M)
        assert count == 1
    else:
        assert 'GLOBAL_ACTIONS = ' in source
        source = source.replace('GLOBAL_ACTIONS = ', ACTIONS + '\nGLOBAL_ACTIONS = ', 1)
    anchor = 'def comfy_free():'
    assert anchor in source
    for helper in ['def installed_vbox_hub():', 'def start_vbox_hub():']:
        if helper in source:
            begin = source.index(helper)
            end = source.index(anchor, begin)
            source = source[:begin] + HELPERS + source[end:]
            break
    else:
        source = source.replace(anchor, HELPERS + anchor, 1)
    if LEGACY_DISPATCH in source:
        source = source.replace(LEGACY_DISPATCH, DISPATCH, 1)
    elif DISPATCH not in source:
        anchor = '            if (svc, act) == ("comfy", "free"):'
        assert anchor in source
        source = source.replace(anchor, DISPATCH + anchor, 1)
    ast.parse(source)
    return source


if __name__ == '__main__':
    path = Path.home() / 'ai/home/home.py'
    before = path.read_text()
    after = patch(before)
    path.write_text(after)
    print('Installed authenticated vbox Hub Start/Stop actions.')
