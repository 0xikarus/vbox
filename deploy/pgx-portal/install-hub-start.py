#!/usr/bin/env python3
"""Add the hub Start action to the existing password-protected PGX portal."""
import ast
from pathlib import Path


def patch(source):
    if 'def start_vbox_hub():' in source:
        return source
    anchor = 'GLOBAL_ACTIONS = '
    assert anchor in source and 'if self.path == "/api/action":' in source
    source = source.replace(anchor, 'SERVICE_ACTIONS["vbox"] = {"start": ("Start hub", None, False)}\n\n' + anchor, 1)
    helper = '''def start_vbox_hub():
    # Start only this installed hub; never change Docker's restart policy.
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
    subprocess.run(["docker", "start", name], check=True, capture_output=True, timeout=30)
    for _ in range(30):
        try:
            port_open(8091)
            return "vbox Hub started: http://vbox.pgx.local/"
        except OSError:
            time.sleep(1)
    raise RuntimeError("Hub started but is not responding yet")


'''
    source = source.replace('def comfy_free():', helper + 'def comfy_free():', 1)
    anchor = '            if (svc, act) == ("comfy", "free"):'
    assert anchor in source
    source = source.replace(anchor, '            if (svc, act) == ("vbox", "start"):\n                return self.reply(200, str(run_action("vbox: start", fn=start_vbox_hub)))\n' + anchor, 1)
    ast.parse(source)
    return source


if __name__ == '__main__':
    path = Path.home() / 'ai/home/home.py'
    before = path.read_text()
    after = patch(before)
    path.write_text(after)
    print('Installed the authenticated vbox Hub Start action.')
