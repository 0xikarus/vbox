'use strict';
window.openWorkspaceTerminal=function(boxID,session,onStatus,options={}){
 const root=options.root||document.querySelector('#terminal-screen');root.replaceChildren();
 const viewOnly=options.viewOnly===true;
 const terminal=new Terminal({cols:80,rows:24,cursorBlink:!viewOnly,disableStdin:viewOnly,fontSize:viewOnly?12:14,lineHeight:viewOnly?1.2:1,scrollback:5000,convertEol:false}),fit=viewOnly?null:new FitAddon.FitAddon();
 if(fit)terminal.loadAddon(fit);terminal.open(root);fit?.fit();
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
 const data=viewOnly?null:terminal.onData(input),binary=viewOnly?null:terminal.onBinary(data=>send({data:btoa(data)})),resize=viewOnly?null:terminal.onResize(size=>send({cols:size.cols,rows:size.rows}));
 const observer=viewOnly?null:new ResizeObserver(()=>{if(!closed)fit.fit()});observer?.observe(root);
 socket.onopen=()=>{if(closed)return;if(!viewOnly){fit.fit();send({cols:terminal.cols,rows:terminal.rows})}onStatus(session?'Connected · '+session:'Connected');metrics({state:'connected',ping:null});if(options.onMetrics){probe();latencyTimer=setInterval(probe,5000)}if(!viewOnly&&options.autoFocus!==false)terminal.focus()};
 socket.onmessage=e=>{if(closed)return;if(typeof e.data==='string'){try{const reply=JSON.parse(e.data);if(pendingProbe&&reply.probe===pendingProbe.id){metrics({ping:Math.round(performance.now()-pendingProbe.started)});pendingProbe=null}}catch{}return}if(e.data instanceof ArrayBuffer)terminal.write(new Uint8Array(e.data))};
 socket.onclose=e=>{clearInterval(latencyTimer);pendingProbe=null;if(!closed){metrics({state:'disconnected',ping:null});onStatus('Disconnected · '+(e.reason||'use Reconnect')+'. Input is not replayed.');options.onDisconnect?.()}};
 socket.onerror=()=>{if(!closed)onStatus('Terminal connection failed. Check login and worker runtime, then reconnect.')};
 const keys=options.keys||document.querySelector('.terminal-keys');keys.replaceChildren();
 const pinnedKeys=options.pinnedKeys||keys;if(pinnedKeys!==keys)pinnedKeys.replaceChildren();
 if(viewOnly)return ()=>{closed=true;clearInterval(latencyTimer);pendingProbe=null;socket.close();terminal.dispose();keys.replaceChildren();if(pinnedKeys!==keys)pinnedKeys.replaceChildren()};
 const copy=document.createElement('button');copy.type='button';copy.textContent='Copy';copy.onclick=async()=>{const value=terminal.getSelection();if(!value){onStatus('Select terminal text before copying.');terminal.focus();return}try{await navigator.clipboard.writeText(value);onStatus('Terminal selection copied.')}catch{onStatus('Clipboard copy was blocked by the browser. Use Ctrl/Cmd+C on the selection.')}};keys.append(copy);
 const paste=document.createElement('button');paste.type='button';paste.textContent='Paste';paste.onclick=async()=>{try{const value=await navigator.clipboard.readText();if(value)terminal.paste(value);terminal.focus()}catch{onStatus('Clipboard paste was blocked by the browser. Use Ctrl/Cmd+V in the terminal.')}};keys.append(paste);
 for(const [label,value] of [['Esc','\x1b'],['Tab','\t'],['Ctrl-C','\x03'],['Ctrl-D','\x04'],['↑','\x1b[A'],['↓','\x1b[B'],['←','\x1b[D'],['→','\x1b[C']]){
  const b=document.createElement('button');b.type='button';b.textContent=label;b.onclick=()=>{input(value);terminal.focus()};keys.append(b);
 }
 const pinned=[['Keyboard','Show the on-screen keyboard','<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="2" y="6" width="20" height="12" rx="2"/><path d="M6 10h.01M10 10h.01M14 10h.01M18 10h.01M7 14h10"/></svg>',()=>terminal.focus()],['Fullscreen','Toggle fullscreen','<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M8 3H5a2 2 0 0 0-2 2v3M16 3h3a2 2 0 0 1 2 2v3M16 21h3a2 2 0 0 0 2-2v-3M8 21H5a2 2 0 0 1-2-2v-3"/></svg>',async()=>{try{if(document.fullscreenElement)await document.exitFullscreen();else await root.requestFullscreen()}catch{onStatus('Fullscreen is unavailable in this browser.')}}]];
 const scrollCue=document.createElement('button');scrollCue.type='button';scrollCue.className='term-key-scroll-cue';scrollCue.hidden=true;pinnedKeys.append(scrollCue);
 const updateScrollCue=()=>{const overflow=keys.scrollWidth>keys.clientWidth+2;scrollCue.hidden=!overflow;if(!overflow)return;const atEnd=keys.scrollLeft+keys.clientWidth>=keys.scrollWidth-2;scrollCue.textContent=atEnd?'‹':'›';scrollCue.setAttribute('aria-label',atEnd?'Previous terminal keys':'More terminal keys');scrollCue.title=scrollCue.getAttribute('aria-label')};
 scrollCue.onclick=()=>{const atEnd=keys.scrollLeft+keys.clientWidth>=keys.scrollWidth-2;keys.scrollBy({left:(atEnd?-1:1)*Math.max(120,keys.clientWidth*.75),behavior:'smooth'})};
 keys.addEventListener('scroll',updateScrollCue,{passive:true});const cueObserver=new ResizeObserver(updateScrollCue);cueObserver.observe(keys);requestAnimationFrame(updateScrollCue);
 for(const [label,title,icon,fn] of pinned){const b=document.createElement('button');b.type='button';b.className='term-key-pinned';b.title=label;b.setAttribute('aria-label',label);b.innerHTML=icon+'<span class="sr-only">'+label+'</span>';b.onclick=fn;pinnedKeys.append(b)}
 return ()=>{closed=true;clearInterval(latencyTimer);pendingProbe=null;observer.disconnect();cueObserver.disconnect();keys.removeEventListener('scroll',updateScrollCue);data?.dispose();binary?.dispose();resize.dispose();socket.close();terminal.dispose();keys.replaceChildren();if(pinnedKeys!==keys)pinnedKeys.replaceChildren()};
};
