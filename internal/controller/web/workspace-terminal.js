'use strict';
window.openWorkspaceTerminal=function(boxID,session,onStatus,options={}){
 const root=options.root||document.querySelector('#terminal-screen');root.replaceChildren();
 const terminal=new Terminal({cursorBlink:true,fontSize:14,scrollback:5000,convertEol:false}),fit=new FitAddon.FitAddon();
 terminal.loadAddon(fit);terminal.open(root);fit.fit();
 const url=new URL('/v1/logical-boxes/'+encodeURIComponent(boxID)+'/terminal/stream',location.href);url.protocol=location.protocol==='https:'?'wss:':'ws:';url.searchParams.set('session',session);
 const socket=new WebSocket(url);socket.binaryType='arraybuffer';let closed=false,latencyTimer,pendingProbe=null,probeSequence=0;
 const metrics=value=>options.onMetrics?.(value);
 function send(frame){if(socket.readyState!==WebSocket.OPEN)return;if(socket.bufferedAmount>1024*1024){onStatus('Input connection is congested; reconnect. Input was not replayed.');socket.close();return}socket.send(JSON.stringify(frame))}
 function probe(){
  if(closed||document.hidden||socket.readyState!==WebSocket.OPEN||socket.bufferedAmount>65536)return;
  if(pendingProbe){if(performance.now()-pendingProbe.started<10000)return;pendingProbe=null;metrics({ping:null})}
  const id=String(++probeSequence);pendingProbe={id,started:performance.now()};send({probe:id});
 }
 function input(data){const bytes=new TextEncoder().encode(data);for(let i=0;i<bytes.length;i+=16384){const chunk=bytes.subarray(i,i+16384);send({data:btoa(String.fromCharCode(...chunk))})}}
 const data=terminal.onData(input),binary=terminal.onBinary(data=>send({data:btoa(data)})),resize=terminal.onResize(size=>send({cols:size.cols,rows:size.rows}));
 const observer=new ResizeObserver(()=>{if(!closed)fit.fit()});observer.observe(root);
 socket.onopen=()=>{if(closed)return;fit.fit();send({cols:terminal.cols,rows:terminal.rows});onStatus('Connected · '+session);metrics({state:'connected',ping:null});if(options.onMetrics){probe();latencyTimer=setInterval(probe,5000)}if(options.autoFocus!==false)terminal.focus()};
 socket.onmessage=e=>{if(closed)return;if(typeof e.data==='string'){try{const reply=JSON.parse(e.data);if(pendingProbe&&reply.probe===pendingProbe.id){metrics({ping:Math.round(performance.now()-pendingProbe.started)});pendingProbe=null}}catch{}return}if(e.data instanceof ArrayBuffer)terminal.write(new Uint8Array(e.data))};
 socket.onclose=e=>{clearInterval(latencyTimer);pendingProbe=null;if(!closed){metrics({state:'disconnected',ping:null});onStatus('Disconnected · '+(e.reason||'use Reconnect')+'. Input is not replayed.');options.onDisconnect?.()}};
 socket.onerror=()=>{if(!closed)onStatus('Terminal connection failed. Check login and worker runtime, then reconnect.')};
 const keys=options.keys||document.querySelector('.terminal-keys');keys.replaceChildren();
 const copy=document.createElement('button');copy.type='button';copy.textContent='Copy';copy.onclick=async()=>{const value=terminal.getSelection();if(!value){onStatus('Select terminal text before copying.');terminal.focus();return}try{await navigator.clipboard.writeText(value);onStatus('Terminal selection copied.')}catch{onStatus('Clipboard copy was blocked by the browser. Use Ctrl/Cmd+C on the selection.')}};keys.append(copy);
 const paste=document.createElement('button');paste.type='button';paste.textContent='Paste';paste.onclick=async()=>{try{const value=await navigator.clipboard.readText();if(value)terminal.paste(value);terminal.focus()}catch{onStatus('Clipboard paste was blocked by the browser. Use Ctrl/Cmd+V in the terminal.')}};keys.append(paste);
 for(const [label,value] of [['Esc','\x1b'],['Tab','\t'],['Ctrl-C','\x03'],['Ctrl-D','\x04'],['↑','\x1b[A'],['↓','\x1b[B'],['←','\x1b[D'],['→','\x1b[C']]){
  const b=document.createElement('button');b.type='button';b.textContent=label;b.onclick=()=>{input(value);terminal.focus()};keys.append(b);
 }
 for(const [label,fn] of [['Keyboard',()=>terminal.focus()],['Fullscreen',async()=>{try{if(document.fullscreenElement)await document.exitFullscreen();else await root.requestFullscreen()}catch{onStatus('Fullscreen is unavailable in this browser.')}}]]){const b=document.createElement('button');b.textContent=label;b.type='button';b.onclick=fn;keys.append(b)}
 return ()=>{closed=true;clearInterval(latencyTimer);pendingProbe=null;observer.disconnect();data.dispose();binary.dispose();resize.dispose();socket.close();terminal.dispose();keys.replaceChildren()};
};
