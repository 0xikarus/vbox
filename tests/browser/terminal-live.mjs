// Actual browser -> WebSocket -> worker PTY -> tmux input recorder.
// This harness substitutes routing/SSH only, not the terminal or its responses.
import assert from 'node:assert/strict';
import {mkdtemp,readFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {randomBytes} from 'node:crypto';
import {spawn,execFileSync} from 'node:child_process';
import http from 'node:http';
import {WebSocketServer} from 'ws';
import puppeteer from 'puppeteer-core';
const runtime=process.argv[2];if(!runtime)throw Error('provide built runtime');
const dir=await mkdtemp(join(tmpdir(),'vmbox-browser-terminal-')),env={...process.env,TMUX_TMPDIR:dir,TMUX:'',TERM:'xterm-256color'},fence=randomBytes(32).toString('hex');
const tmux=(...args)=>execFileSync('tmux',args,{env,encoding:'utf8',timeout:5000});
const wait=async fn=>{const end=Date.now()+10000;while(!await fn()){if(Date.now()>end)throw Error('observation timeout');await new Promise(r=>setTimeout(r,50))}};
let browser,server,sockets;
try{
 execFileSync(runtime,['native-bind',fence],{env,timeout:5000});
 const file=join(dir,'received.bin');
 tmux('new-session','-d','-s','recorder','python3','-u','-c',`import os,tty\ntty.setraw(0)\nf=open(${JSON.stringify(file)},'ab',buffering=0)\nos.write(1,b'RECORDER READY\\r\\n')\nwhile True:\n d=os.read(0,4096)\n if not d: break\n f.write(d)\n os.write(1,b'\\r\\x1b[2KRECEIVED '+str(len(d)).encode()+b' bytes\\r\\n')`.replaceAll('\\n','\\n').replaceAll('\n','\n'));
 const inv=JSON.parse(execFileSync(runtime,['native-sessions',fence],{env,encoding:'utf8',timeout:5000})),session=inv.sessions.find(s=>s.name==='recorder');
 server=http.createServer(async(req,res)=>{const path=new URL(req.url,'http://test').pathname;
  res.setHeader('Content-Security-Policy',"default-src 'self'; connect-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'");
  if(path==='/'){res.setHeader('Content-Type','text/html');res.end('<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/xterm.css"><link rel="stylesheet" href="/workspace.css"><p id="status"></p><div class="terminal-keys"></div><div id="terminal-screen"></div><script src="/xterm.js"></script><script src="/xterm-fit.js"></script><script src="/workspace-terminal.js"></script>');return}
  if(['/xterm.css','/workspace.css','/xterm.js','/xterm-fit.js','/workspace-terminal.js'].includes(path)){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');res.end(await readFile('internal/controller/web'+path));return}res.writeHead(404);res.end();
 });
 sockets=new WebSocketServer({server});sockets.on('connection',ws=>{const child=spawn(runtime,['web-terminal',fence,session.id,session.incarnation],{env});child.stdout.on('data',d=>{if(ws.readyState===1)ws.send(d)});child.stderr.resume();ws.on('message',d=>child.stdin.write(Buffer.concat([d,Buffer.from('\n')])));ws.on('close',()=>{child.stdin.end();child.kill()});child.on('exit',()=>ws.close())});
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 browser=await puppeteer.launch({executablePath:'/snap/bin/chromium',headless:true,args:['--no-sandbox']});
 for(const mobile of [false,true]){
  const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.setViewport(mobile?{width:390,height:844,isMobile:true,hasTouch:true}:{width:1280,height:900});await page.goto('http://127.0.0.1:'+server.address().port);
  await page.evaluate(()=>{window.disconnect=openWorkspaceTerminal('box','recorder',s=>document.querySelector('#status').textContent=s)});
  await page.waitForFunction(()=>document.querySelector('#status').textContent.startsWith('Connected'));
  await page.waitForFunction(()=>document.querySelector('.xterm-rows').textContent.includes('RECORDER')||document.querySelector('.xterm-rows').textContent.includes('RECEIVED'));
  const before=(await readFile(file)).length,marker=randomBytes(6).toString('hex')+'zYüß';
  await page.focus('.xterm-helper-textarea');await page.keyboard.type(marker);await page.keyboard.press('Enter');await page.keyboard.press('Backspace');await page.keyboard.press('ArrowUp');await page.keyboard.down('Control');await page.keyboard.press('c');await page.keyboard.up('Control');
  const expected=Buffer.from(marker+'\r\x7f\x1b[A\x03');
  await wait(async()=> (await readFile(file)).length>=before+expected.length);
  assert.deepEqual((await readFile(file)).subarray(before),expected);
  await page.screenshot({path:join(dir,mobile?'mobile.png':'desktop.png')});
  await page.close();await new Promise(r=>setTimeout(r,150));assert.equal((await readFile(file)).length,before+expected.length,'input replayed');assert.deepEqual(errors,[]);
 }
 assert.equal(tmux('has-session','-t','recorder'),'');console.log('PASS: desktop/mobile real browser input bytes, no replay after close, tmux survives. Artifacts: '+dir);
}finally{await browser?.close();if(sockets){for(const ws of sockets.clients)ws.terminate();sockets.close()}if(server)await new Promise(r=>server.close(r));try{tmux('kill-server')}catch{}}
