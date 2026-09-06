'use strict';
window.openWorkspaceTerminal=function(boxID,session,onStatus){
 const root=document.querySelector('#terminal-screen');root.replaceChildren();
 const terminal=new Terminal({cursorBlink:true,fontSize:14,scrollback:5000,convertEol:false}),fit=new FitAddon.FitAddon();
 terminal.loadAddon(fit);terminal.open(root);fit.fit();
 const url=new URL('/v1/logical-boxes/'+encodeURIComponent(boxID)+'/terminal/stream',location.href);url.protocol=location.protocol==='https:'?'wss:':'ws:';url.searchParams.set('session',session);
 const socket=new WebSocket(url);socket.binaryType='arraybuffer';let closed=false;
 function send(frame){if(socket.readyState!==WebSocket.OPEN)return;if(socket.bufferedAmount>1024*1024){onStatus('Input connection is congested; reconnect. Input was not replayed.');socket.close();return}socket.send(JSON.stringify(frame))}
 function input(data){const bytes=new TextEncoder().encode(data);for(let i=0;i<bytes.length;i+=16384){const chunk=bytes.subarray(i,i+16384);send({data:btoa(String.fromCharCode(...chunk))})}}
 const data=terminal.onData(input),binary=terminal.onBinary(data=>send({data:btoa(data)})),resize=terminal.onResize(size=>send({cols:size.cols,rows:size.rows}));
 const observer=new ResizeObserver(()=>{if(!closed)fit.fit()});observer.observe(root);
 socket.onopen=()=>{fit.fit();send({cols:terminal.cols,rows:terminal.rows});onStatus('Connected · '+session);terminal.focus()};
 socket.onmessage=e=>{if(!closed&&e.data instanceof ArrayBuffer)terminal.write(new Uint8Array(e.data))};
 socket.onclose=e=>{if(!closed)onStatus('Disconnected · '+(e.reason||'use Resume / reconnect')+'. Input is not replayed.')};
 socket.onerror=()=>onStatus('Terminal connection failed. Check login and worker runtime, then reconnect.');
 const keys=document.querySelector('.terminal-keys');keys.replaceChildren();
 for(const [label,value] of [['Esc','\x1b'],['Tab','\t'],['Ctrl-C','\x03'],['Ctrl-D','\x04'],['↑','\x1b[A'],['↓','\x1b[B'],['←','\x1b[D'],['→','\x1b[C']]){
  const b=document.createElement('button');b.type='button';b.textContent=label;b.onclick=()=>{input(value);terminal.focus()};keys.append(b);
 }
 for(const [label,fn] of [['Keyboard',()=>terminal.focus()],['Fullscreen',async()=>{try{if(document.fullscreenElement)await document.exitFullscreen();else await root.requestFullscreen()}catch{onStatus('Fullscreen is unavailable in this browser.')}}]]){const b=document.createElement('button');b.textContent=label;b.type='button';b.onclick=fn;keys.append(b)}
 return ()=>{closed=true;observer.disconnect();data.dispose();binary.dispose();resize.dispose();socket.close();terminal.dispose();keys.replaceChildren()};
};
