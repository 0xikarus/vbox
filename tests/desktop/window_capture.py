"""Run with IMAGE RUNTIME_BINARY; creates and removes one isolated desktop container."""
import base64
import json
import struct
import subprocess
import sys
import time
import uuid


def run(*args, data=None):
    return subprocess.run(args, input=data, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, check=True, timeout=60).stdout


name = 'vmbox-window-disposable-' + uuid.uuid4().hex[:12]
image, runtime = sys.argv[1:]
run('docker', 'run', '-d', '--name', name, '--network', 'none',
    '--label', 'io.vmbox.test=window-capture', '--entrypoint', 'sleep', image, 'infinity')


def worker(*args, data=None):
    return run('docker', 'exec', '-i', name, *args, data=data)


def window(*args):
    return worker('env', 'DISPLAY=:99', 'xdotool', *args).decode().strip()


def capture(arguments):
    request = {'jsonrpc': '2.0', 'id': 1, 'method': 'tools/call',
               'params': {'name': 'capture_window', 'arguments': arguments}}
    response = worker('vmbox-runtime', 'desktop-mcp',
                      data=(json.dumps(request) + '\n').encode())
    return json.loads(response)['result']


try:
    run('docker', 'cp', runtime, name + ':/usr/local/bin/vmbox-runtime')
    fence = 'a' * 64
    worker('vmbox-runtime', 'native-bind', fence)
    worker('vmbox-runtime', 'interactive-start', fence, 'shell-fixture', 'shell')
    worker('vmbox-runtime', 'desktop-start', fence)
    for attempt in range(50):
        try:
            identifier = window('search', '--onlyvisible', '--name', '^vmbox managed session$').splitlines()[0]
            break
        except subprocess.CalledProcessError:
            time.sleep(.2)
    else:
        raise AssertionError('managed desktop terminal did not become visible')
    window('windowmove', identifier, '100', '100')
    window('windowsize', identifier, '500', '300')
    window('windowactivate', '--sync', identifier)
    time.sleep(.3)
    geometry = dict(line.split('=', 1) for line in window('getwindowgeometry', '--shell', identifier).splitlines())
    expected = (int(geometry['WIDTH']), int(geometry['HEIGHT']))
    for arguments in ({}, {'window_id': identifier}, {'window_id': hex(int(identifier))}):
        result = capture(arguments)
        assert not result.get('isError'), result
        content = next(item for item in result['content'] if item['type'] == 'image')
        png = base64.b64decode(content['data'])
        assert png[:8] == b'\x89PNG\r\n\x1a\n'
        assert struct.unpack('>II', png[16:24]) == expected
        assert expected != (1280, 800)
    assert capture({'window_id': '0'})['isError']
    window('windowminimize', identifier)
    time.sleep(.3)
    assert capture({'window_id': identifier})['isError']
    print('PASS: real X11/VNC window PNG, active/decimal/hex selection, minimized-window rejection')
finally:
    run('docker', 'rm', '-f', '-v', name)
