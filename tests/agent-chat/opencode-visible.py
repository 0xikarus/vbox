import http.server
import json
import os
import pathlib
import subprocess
import threading
import time
import urllib.request

from probe import Model

home = pathlib.Path('/tmp/disposable-visible-opencode')
home.mkdir(mode=0o700)
os.environ.update(HOME=str(home), XDG_CONFIG_HOME=str(home / '.config'), XDG_DATA_HOME=str(home / '.local/share'), XDG_CACHE_HOME=str(home / '.cache'), VMBOX_PROBE_LOG=str(home / 'events.jsonl'), OPENCODE_DISABLE_MODELS_FETCH='true', OPENCODE_DISABLE_AUTOUPDATE='true', OPENAI_API_KEY='disposable-fixture-key')
runtime = '/data/home/bin/vmbox-runtime'
server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Model)
threading.Thread(target=server.serve_forever, daemon=True).start()
config = home / '.config/opencode'
config.mkdir(parents=True)
(config / 'opencode.json').write_text(json.dumps({'model': 'openai/gpt-4o', 'provider': {'openai': {'options': {'baseURL': 'http://127.0.0.1:' + str(server.server_port) + '/v1', 'apiKey': 'disposable-fixture-key'}}}}))
subprocess.run([runtime, 'desktop-register', 'opencode'], check=True)
settings = json.loads((config / 'opencode.json').read_text())
settings['mcp'] = {}
(config / 'opencode.json').write_text(json.dumps(settings))
session = 'opencode-disposable-visible'
subprocess.run(['tmux', 'new-session', '-d', '-s', session, '-x', '120', '-y', '36', 'opencode --auto --hostname 127.0.0.1 --port 24567'], cwd=home, check=True)
desktop = subprocess.Popen(['Xtigervnc', ':98', '-ac', '-SecurityTypes', 'None', '-localhost', '-rfbport', '5998', '-geometry', '1200x800'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
time.sleep(1)
terminal = subprocess.Popen(['xterm', '-display', ':98', '-geometry', '120x36', '-e', 'tmux', 'attach-session', '-t', session], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

def api(route, payload=None):
    request = urllib.request.Request('http://127.0.0.1:24567' + route, data=None if payload is None else json.dumps(payload).encode(), headers={'Content-Type': 'application/json'})
    with urllib.request.urlopen(request, timeout=10) as response:
        return json.load(response)

try:
    for attempt in range(450):
        if list((home / '.local/share/vmbox/opencode-tui').glob('*.sock')):
            break
        time.sleep(.2)
    else:
        raise RuntimeError('Visible TUI bridge did not start')
    old = api('/session', {})
    text = 'DISPOSABLE_VISIBLE_FIRST_MESSAGE'
    subprocess.run([runtime, 'chat-opencode', session], input=json.dumps({'id': 'disposable-visible-first', 'text': text}), text=True, check=True, timeout=30)
    for attempt in range(100):
        screen = subprocess.check_output(['tmux', 'capture-pane', '-p', '-t', session], text=True)
        if text in screen:
            break
        time.sleep(.2)
    else:
        raise RuntimeError('Web message did not appear in visible TUI')
    assert not api('/session/' + old['id'] + '/message'), 'Background session received the prompt'
    visible = next(value for value in api('/session') if value['id'] != old['id'])
    followup = 'DISPOSABLE_VISIBLE_FOLLOWUP'
    subprocess.run([runtime, 'chat-opencode', session], input=json.dumps({'id': 'disposable-visible-followup', 'text': followup}), text=True, check=True, timeout=30)
    for attempt in range(100):
        messages = api('/session/' + visible['id'] + '/message')
        if followup in json.dumps(messages):
            break
        time.sleep(.2)
    else:
        raise RuntimeError('Followup left the visible conversation')
    assert len(api('/session')) == 2, 'Followup created another conversation'
    api('/tui/select-session', {'sessionID': old['id']})
    time.sleep(1)
    selected = 'DISPOSABLE_SELECTED_CONVERSATION'
    subprocess.run([runtime, 'chat-opencode', session], input=json.dumps({'id': 'disposable-visible-selected', 'text': selected}), text=True, check=True, timeout=30)
    for attempt in range(100):
        messages = api('/session/' + old['id'] + '/message')
        if selected in json.dumps(messages):
            break
        time.sleep(.2)
    else:
        raise RuntimeError('Delivery did not follow the selected desktop conversation')
    print('PASS: untouched home screen -> visible conversation; background history remains empty', flush=True)
    print('PASS: followup stays in conversation; selecting another desktop conversation redirects web chat', flush=True)
finally:
    (home / 'screen.txt').write_text(subprocess.check_output(['tmux', 'capture-pane', '-p', '-t', session], text=True))
    subprocess.run(['tmux', 'kill-session', '-t', session], check=False)
    terminal.terminate()
    desktop.terminate()
    server.shutdown()
