import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import plugin from '../internal/boxruntime/opencode_tui_plugin.mjs';

function send(socket, body) {
  return new Promise((resolve, reject) => {
    const request = http.request({socketPath: socket, path: '/prompt', method: 'POST'}, response => {
      let text = '';
      response.on('data', chunk => { text += chunk; });
      response.on('end', () => resolve({status: response.statusCode, body: JSON.parse(text)}));
    });
    request.on('error', reject);
    request.end(JSON.stringify(body));
  });
}

test('OpenCode acknowledges only the visible native user item and deduplicates retry', async () => {
  const home = await fs.mkdtemp(path.join(os.tmpdir(), 'vmbox-opencode-bridge-test-'));
  const priorHome = process.env.HOME;
  const priorPane = process.env.TMUX_PANE;
  process.env.HOME = home;
  process.env.TMUX_PANE = '%941';
  const socket = path.join(home, '.local/share/vmbox/opencode-tui/941.sock');
  const messages = [];
  let prompts = 0;
  let dispose;
  const api = {
    route: {current: {name: 'session', params: {sessionID: 'visible-one'}}, navigate(name, params) {
      this.current = {name, params};
    }},
    client: {session: {
      async messages({sessionID}) { return {data: messages.filter(message => message.info.sessionID === sessionID)}; },
      async promptAsync({sessionID, parts}) {
        prompts++;
        setTimeout(() => messages.push({info: {role: 'user', sessionID}, parts}), 50);
        return {data: null};
      },
    }},
    lifecycle: {onDispose(fn) { dispose = fn; }},
  };
  try {
    await plugin.tui(api);
    const input = {messageID: 'test-message-1', parts: [{type: 'text', text: 'hello'}, {type: 'file', mime: 'image/png', filename: 'one.png', url: 'data:image/png;base64,aGVsbG8='}]};
    const first = await send(socket, structuredClone(input));
    assert.equal(first.status, 200);
    assert.equal(first.body.accepted, true);
    assert.equal(first.body.sessionID, 'visible-one');
    assert.equal(first.body.messageID, input.messageID);
    assert.equal(prompts, 1);
    assert.equal(messages[0].parts[1].type, 'file');
    const repeated = await send(socket, {...structuredClone(input), retryOnly: true});
    assert.equal(repeated.status, 200);
    assert.equal(prompts, 1);
    api.route.current = {name: 'session', params: {sessionID: 'visible-two'}};
    const changed = await send(socket, {...structuredClone(input), retryOnly: true});
    assert.equal(changed.status, 409);
    assert.equal(prompts, 1);
  } finally {
    if (dispose) await dispose();
    await fs.rm(home, {recursive: true, force: true});
    if (priorHome === undefined) delete process.env.HOME; else process.env.HOME = priorHome;
    if (priorPane === undefined) delete process.env.TMUX_PANE; else process.env.TMUX_PANE = priorPane;
  }
});
