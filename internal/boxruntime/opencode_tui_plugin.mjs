import http from 'node:http';
import net from 'node:net';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import {randomUUID} from 'node:crypto';

export default {
  id: 'vmbox-visible-chat',
  tui: async api => {
    const pane = process.env.TMUX_PANE;
    if (!/^%\d+$/.test(pane || '')) return;
    const directory = path.join(os.homedir(), '.local/share/vmbox/opencode-tui');
    await fs.mkdir(directory, {recursive: true, mode: 0o700});
    const socket = path.join(directory, pane.slice(1) + '.sock');
    const instance = randomUUID();
    const managedSession = process.env.VMBOX_CHAT_SESSION;
    const mascotMarker = /^[a-zA-Z0-9_-]{1,128}$/.test(managedSession || '')
      ? path.join(directory, 'mascot-' + managedSession + '.json') : null;
    try {
      const existing = await fs.lstat(socket);
      if (!existing.isSocket()) throw Error('TUI socket path is occupied');
      await new Promise((resolve, reject) => {
        const connection = net.createConnection(socket);
        connection.once('connect', () => {
          connection.destroy();
          const check = http.get({socketPath: socket, path: '/health'}, response => {
            let data = '';
            response.on('data', chunk => { data += chunk; });
            response.on('end', () => {
              try {
                if (JSON.parse(data).pid === process.pid) reject(Error('TUI bridge already running'));
                else resolve();
              } catch { resolve(); }
            });
          });
          check.once('error', resolve);
          check.setTimeout(1000, () => { check.destroy(); resolve(); });
        });
        connection.once('error', error => {
          if (error.code === 'ECONNREFUSED' || error.code === 'ENOENT') resolve();
          else reject(error);
        });
      });
      await fs.unlink(socket).catch(error => { if (error.code !== 'ENOENT') throw error; });
    } catch (error) { if (error.code !== 'ENOENT') throw error; }
    let busy = false;
    const submitted = new Set();
    const visibleSession = () => api.route.current.name === 'session'
      ? api.route.current.params?.sessionID : undefined;
    const accepted = async (sessionID, input) => {
      const messages = await api.client.session.messages({sessionID, limit: 1000});
      if (messages.error || !Array.isArray(messages.data)) throw Error('Cannot inspect native messages');
      const files = input.parts.filter(part => part.type === 'file');
      return messages.data.some(message => message.info?.role === 'user' &&
        message.parts?.some(part => part.type === 'text' && part.text === input.parts[0].text) &&
        message.parts?.filter(part => part.type === 'file').length === files.length);
    };
    const server = http.createServer(async (request, response) => {
      response.setHeader('Content-Type', 'application/json');
      if (request.method === 'GET' && request.url === '/health') {
        response.end(JSON.stringify({pane, pid: process.pid, instance, sessionID: visibleSession()}));
        return;
      }
      if (request.method === 'GET' && request.url === '/mascot-transcript') {
        const sessionID = visibleSession();
        if (!sessionID) { response.writeHead(409).end('{}'); return; }
        try {
          const result = await api.client.session.messages({sessionID, limit: 24});
          if (result.error || !Array.isArray(result.data) || visibleSession() !== sessionID) throw Error('Visible conversation changed');
          const lines = [];
          for (const message of result.data) {
            const role = message.info?.role;
            if (role !== 'user' && role !== 'assistant') continue;
            for (const part of message.parts || []) {
              if (role === 'assistant' && part.type === 'tool' &&
                  (part.state?.status === 'pending' || part.state?.status === 'running')) {
                lines.push('tool: Running tool');
              }
              if (part.type !== 'text' || typeof part.text !== 'string') continue;
              for (const line of part.text.split('\n')) {
                const trimmed = line.trim();
                if (trimmed) lines.push(role + ': ' + trimmed.slice(0, 1000));
              }
            }
          }
          const buffer = Buffer.from(lines.join('\n'));
          response.end(JSON.stringify({instance, text: buffer.subarray(Math.max(0, buffer.length - 8192)).toString('utf8')}));
        } catch { response.writeHead(503).end('{}'); }
        return;
      }
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
        if (typeof input.messageID !== 'string' || !/^[a-zA-Z0-9_-]{1,128}$/.test(input.messageID) ||
            !Array.isArray(input.parts) || !input.parts.length ||
            input.parts[0]?.type !== 'text' || typeof input.parts[0].text !== 'string') throw Error('Invalid prompt');
        const marker = '[VMBox-Delivery-ID: ' + input.messageID + ']';
        if (!input.parts[0].text.includes(marker)) input.parts[0].text += '\n\n' + marker;
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
        // promptAsync returns before the native user item exists. An exact
        // transcript receipt is required before the worker may report delivery.
        if (await accepted(sessionID, input)) {
          response.end(JSON.stringify({sessionID, messageID: input.messageID, instance, accepted: true}));
          return;
        }
        if (input.retryOnly || submitted.has(input.messageID)) {
          response.writeHead(409).end(JSON.stringify({error: 'Native receipt pending'}));
          return;
        }
        submitted.add(input.messageID);
        const result = await api.client.session.promptAsync({sessionID, parts: input.parts});
        if (result.error) throw Error('Prompt submission failed');
        for (let attempt = 0; attempt < 50; attempt++) {
          const current = visibleSession();
          if (current && current !== sessionID) throw Error('Visible conversation changed');
          if (current === sessionID && await accepted(sessionID, input)) {
            response.end(JSON.stringify({sessionID, messageID: input.messageID, instance, accepted: true}));
            return;
          }
          await new Promise(resolve => setTimeout(resolve, 200));
        }
        throw Error('Native receipt pending');
      } catch {
        response.writeHead(502).end(JSON.stringify({error: 'Visible OpenCode prompt submission failed'}));
      } finally { busy = false; }
    });
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(socket, resolve); });
    await fs.chmod(socket, 0o600);
    if (mascotMarker) {
      const temporary = mascotMarker + '.' + instance;
      await fs.writeFile(temporary, JSON.stringify({socket, instance}), {mode: 0o600});
      await fs.rename(temporary, mascotMarker);
    }
    api.lifecycle.onDispose(async () => {
      await new Promise(resolve => server.close(resolve));
      if (mascotMarker) {
        try {
          const current = JSON.parse(await fs.readFile(mascotMarker, 'utf8'));
          if (current.instance === instance) await fs.unlink(mascotMarker);
        } catch {}
      }
    });
  }
};
