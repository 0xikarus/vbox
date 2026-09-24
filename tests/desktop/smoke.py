"""Opt-in real desktop test: python3 tests/desktop/smoke.py IMAGE.
Creates two disposable, network-isolated containers; never mounts user files.
"""
import base64
import json
import struct
import subprocess
import sys
import time
import uuid

image = sys.argv[1]
containers = []

def run(*args, data=None, check=True):
    return subprocess.run(args, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=check, timeout=60)

def worker(box, *args, data=None, check=True):
    return run('docker', 'exec', '-i', box, *args, data=data, check=check)

def action(box, fence, **value):
    return worker(box, 'vmbox-runtime', 'desktop-input', fence, data=json.dumps(value).encode())

try:
    for index in range(2):
        name = 'vmbox-desktop-disposable-' + uuid.uuid4().hex[:12]
        run('docker', 'run', '-d', '--name', name, '--label', 'io.vmbox.test=desktop-mvp', '--network', 'none', image)
        containers.append(name)
        fence = ('a' if index == 0 else 'b') * 64
        worker(name, 'vmbox-runtime', 'native-bind', fence)
        worker(name, 'vmbox-runtime', 'interactive-start', fence, 'shell-fixture', 'shell')
        # Reopening the viewer must not launch a second terminal or shell.
        worker(name, 'vmbox-runtime', 'desktop-start', fence)
        time.sleep(.5)
        windows = worker(name, 'env', 'DISPLAY=:99', 'xdotool', 'search', '--name', '^vmbox managed session:').stdout.splitlines()
        assert len(windows) == 1, ('visible terminal count', len(windows))
        for attempt in range(50):
            command = worker(name, 'tmux', 'display-message', '-p', '-t', 'shell-fixture', '#{pane_current_command}').stdout.strip()
            clients = worker(name, 'tmux', 'list-clients', '-F', '#{session_name}').stdout
            if command == b'bash' and b'shell-fixture' in clients:
                break
            time.sleep(.1)
        else:
            raise AssertionError('visible terminal did not attach to the shell')
        action(name, fence, action='click', x=150, y=160)
        action(name, fence, action='type', text='printf DESKTOP_FIXTURE_OK')
        action(name, fence, action='key', keys=['Return'])
        time.sleep(.2)
        screen = worker(name, 'tmux', 'capture-pane', '-p', '-t', 'shell-fixture').stdout
        assert b'DESKTOP_FIXTURE_OK' in screen, 'desktop keyboard did not reach the managed shell: '+screen.decode(errors='replace')
        action(name, fence, action='drag', x=150, y=160, toX=250, toY=160)
        action(name, fence, action='scroll', x=250, y=160, text='down', count=2)
        screenshot = worker(name, 'vmbox-runtime', 'desktop-screenshot', fence).stdout
        assert screenshot[:8] == b'\x89PNG\r\n\x1a\n'
        assert struct.unpack('>II', screenshot[16:24]) == (1280, 800)
        thumb = worker(name, 'vmbox-runtime', 'desktop-thumbnail', fence).stdout
        assert struct.unpack('>II', thumb[16:24]) == (320, 200)
        action(name, fence, action='move', x=130+index*100, y=170)
        cursor = worker(name, 'env', 'DISPLAY=:99', 'xdotool', 'getmouselocation', '--shell').stdout
        assert ('X='+str(130+index*100)).encode() in cursor
        wrong = worker(name, 'vmbox-runtime', 'desktop-screenshot', ('b' if index == 0 else 'a')*64, check=False)
        assert wrong.returncode != 0 and not wrong.stdout
        action(name, fence, action='pause')
        idle = json.loads(worker(name, 'vmbox-runtime', 'desktop-idle', fence).stdout)
        assert idle['seconds'] == 0, 'human takeover must prevent inactivity shutdown'
        blocked = worker(name, 'vmbox-runtime', 'desktop-input', fence, data=b'{"action":"move","x":1,"y":1}', check=False)
        assert blocked.returncode != 0
        # Screenshot is still available during takeover, without any VNC canvas.
        worker(name, 'vmbox-runtime', 'desktop-thumbnail', fence)
        action(name, fence, action='resume')
        messages = [
            {'jsonrpc':'2.0','id':1,'method':'initialize','params':{'protocolVersion':'2025-06-18'}},
            {'jsonrpc':'2.0','method':'notifications/initialized'},
            {'jsonrpc':'2.0','id':2,'method':'tools/list'},
            {'jsonrpc':'2.0','id':3,'method':'tools/call','params':{'name':'take_screenshot','arguments':{}}},
        ]
        output = worker(name, 'vmbox-runtime', 'desktop-mcp', data=('\n'.join(map(json.dumps,messages))+'\n').encode()).stdout
        replies = [json.loads(line) for line in output.splitlines()]
        assert len(replies[1]['result']['tools']) == 10
        picture = replies[2]['result']['content'][0]
        assert picture['type'] == 'image' and base64.b64decode(picture['data'])[:8] == screenshot[:8]
    cursor = worker(containers[0], 'env', 'DISPLAY=:99', 'xdotool', 'getmouselocation', '--shell').stdout
    assert b'X=130' in cursor, 'other box moved the first cursor'
    print('PASS: two isolated desktops, visible reusable terminals, in-box PNG/thumbnail, input/takeover, assignment fencing, MCP image exchange')
finally:
    for name in containers:
        run('docker', 'rm', '-f', '-v', name, check=False)
