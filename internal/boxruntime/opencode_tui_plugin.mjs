import http from 'node:http';
import net from 'node:net';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';

export default {
  id: 'vmbox-visible-chat',
  tui: async api => {
    const pane = process.env.TMUX_PANE;
    if (!/^%\d+$/.test(pane || '')) return;
    const directory = path.join(os.homedir(), '.local/share/vmbox/opencode-tui');
    await fs.mkdir(directory, {recursive: true, mode: 0o700});
    const socket = path.join(directory, pane.slice(1) + '.sock');
    try {
      const existing = await fs.lstat(socket);
      if (!existing.isSocket()) throw Error('TUI socket path is occupied');
      await new Promise((resolve, reject) => {
        const connection = net.createConnection(socket);
        connection.once('connect', () => { connection.destroy(); reject(Error('TUI bridge already running')); });
        connection.once('error', error => {
          if (error.code === 'ECONNREFUSED' || error.code === 'ENOENT') resolve();
          else reject(error);
        });
      });
      await fs.unlink(socket).catch(error => { if (error.code !== 'ENOENT') throw error; });
    } catch (error) { if (error.code !== 'ENOENT') throw error; }
    let busy = false;
    const server = http.createServer(async (request, response) => {
      response.setHeader('Content-Type', 'application/json');
      if (request.method === 'GET' && request.url === '/health') { response.end('{}'); return; }
      if (request.method !== 'POST' || request.url !== '/prompt') { response.writeHead(404).end('{}'); return; }
      if (busy) { response.writeHead(409).end('{}'); return; }
      busy = true;
      try {
        let body = '';
        for await (const chunk of request) {
          body += chunk;
          if (body.length > 100 * 1024 * 1024) throw Error('Request too large');
        }
        const input = JSON.parse(body);
        if (!Array.isArray(input.parts) || !input.parts.length) throw Error('Invalid prompt');
        let sessionID;
        const route = api.route.current;
        if (route.name === 'session') sessionID = route.params?.sessionID;
        else if (route.name === 'home') {
          const created = await api.client.session.create({});
          if (created.error || !created.data?.id) throw Error('Session creation failed');
          if (api.route.current.name !== 'home') throw Error('Visible conversation changed');
          sessionID = created.data.id;
          api.route.navigate('session', {sessionID});
        }
        if (!sessionID) throw Error('No visible conversation');
        const result = await api.client.session.promptAsync({sessionID, parts: input.parts});
        if (result.error) throw Error('Prompt submission failed');
        response.end(JSON.stringify({sessionID}));
      } catch {
        response.writeHead(502).end(JSON.stringify({error: 'Visible OpenCode prompt submission failed'}));
      } finally { busy = false; }
    });
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(socket, resolve); });
    await fs.chmod(socket, 0o600);
    api.lifecycle.onDispose(() => new Promise(resolve => server.close(resolve)));
  }
};
