// Real desktop/runtime transport test; HTTP routing/auth are not exercised here.
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import http from 'node:http';
import {execFileSync,spawn} from 'node:child_process';
import {randomBytes} from 'node:crypto';
import {WebSocketServer} from 'ws';
import puppeteer from 'puppeteer-core';
const container=process.argv[2];if(!container)throw Error('provide disposable test container name');
const docker=(...args)=>execFileSync('docker',['exec',container,...args],{timeout:25000,encoding:'utf8'});
let fence;try{fence=docker('tmux','show-option','-gv','@vmbox_assignment').trim()}catch{fence=randomBytes(32).toString('hex')}
docker('vmbox-runtime','native-bind',fence);docker('vmbox-runtime','desktop-start',fence);
const server=http.createServer(async(req,res)=>{
 const path=new URL(req.url,'http://test').pathname;
 if(path==='/'){res.setHeader('Content-Type','text/html');res.end('<div id="desktop-controls"></div><p id="status"></p><div id="desktop-screen"></div><script src="/novnc.js"></script><script src="/workspace-desktop.js"></script>');return}
 if(['/novnc.js','/workspace-desktop.js'].includes(path)){res.setHeader('Content-Type','text/javascript');res.end(await readFile('internal/controller/web'+path));return}
 res.writeHead(404);res.end();
});
const sockets=new WebSocketServer({server});let streamBytes=0;
sockets.on('connection',ws=>{const child=spawn('docker',['exec','-i',container,'vmbox-runtime','desktop-stream',fence]);child.stdout.on('data',data=>{streamBytes+=data.length;if(ws.readyState===1)ws.send(data)});child.stderr.resume();ws.on('message',data=>child.stdin.write(data));ws.on('close',()=>{child.stdin.end();child.kill()});child.on('exit',()=>ws.close())});
await new Promise(r=>server.listen(0,'127.0.0.1',r));
let browser;
try{
 browser=await puppeteer.launch({executablePath:'/snap/bin/chromium',headless:true,args:['--no-sandbox']});
 const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));await page.setViewport({width:1280,height:900});
 await page.goto('http://127.0.0.1:'+server.address().port);
 await page.evaluate(()=>{document.querySelector('#desktop-screen').style.height='800px';window.disconnect=openWorkspaceDesktop('test',s=>document.querySelector('#status').textContent=s)});
 await page.waitForFunction(()=>document.querySelector('#status').textContent==='Desktop connected',{timeout:15000});
 await page.waitForFunction(()=>document.querySelector('canvas')?.width>0);
 await page.waitForFunction(()=>{const c=document.querySelector('canvas'),pixels=c.getContext('2d').getImageData(0,0,c.width,c.height).data;let light=0;for(let i=0;i<pixels.length;i+=16)if(pixels[i]>80&&pixels[i+1]>80&&pixels[i+2]>80)light++;return light>1000},{timeout:20000});
 await page.screenshot({path:'/tmp/vmbox-desktop-live.png'});
 // Browser keyboard events enter Firefox through the VNC canvas, not a shell
 // command or a fake agent response. Inspect the captured built-in page too.
 await page.click('#desktop-controls button');
 await page.focus('#desktop-controls textarea');
 await page.keyboard.type('about:robots');await page.keyboard.press('Enter');
 await new Promise(r=>setTimeout(r,1500));
 await page.screenshot({path:'/tmp/vmbox-desktop-navigation.png'});
 assert(streamBytes>0,'no framebuffer output');assert.deepEqual(errors,[]);
 await page.evaluate(()=>window.disconnect());
 // Reconnection must find the same desktop tmux session, not create a new one.
 const before=docker('tmux','display-message','-p','-t','vmbox-desktop','#{pane_pid}');
 docker('vmbox-runtime','desktop-start',fence);
 assert.equal(docker('tmux','display-message','-p','-t','vmbox-desktop','#{pane_pid}'),before);
 console.log('PASS: real VNC handshake/framebuffer, no browser errors, desktop process survives viewer disconnect. Screenshot: /tmp/vmbox-desktop-live.png');
}finally{await browser?.close();for(const ws of sockets.clients)ws.terminate();sockets.close();await new Promise(r=>server.close(r))}
