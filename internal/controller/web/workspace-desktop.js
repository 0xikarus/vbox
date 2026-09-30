'use strict';
window.openWorkspaceDesktop=function(boxID,onStatus,options={}){
 const viewOnly=options.viewOnly===true;
 const root=options.root||document.querySelector('#desktop-screen');root.replaceChildren();
 const url=new URL('/v1/logical-boxes/'+encodeURIComponent(boxID)+'/desktop/stream',location.href);url.protocol=location.protocol==='https:'?'wss:':'ws:';
 const rfb=new NoVNC.default(root,url.href);rfb.scaleViewport=true;rfb.resizeSession=false;rfb.showDotCursor=true;rfb.viewOnly=viewOnly;let closed=false;
 let latencyTimer,pendingProbe=null,probeSequence=0;
 const metrics=value=>options.onMetrics?.(value);
 function probe(){
  if(closed||document.hidden)return;
  if(pendingProbe){if(performance.now()-pendingProbe.started<10000)return;pendingProbe=null;metrics({ping:null})}
  const payload='vmbox:'+String(++probeSequence),started=performance.now();
  if(rfb.requestLatencyProbe?.(payload))pendingProbe={payload,started};
 }
 rfb.addEventListener('fenceresponse',e=>{if(!closed&&pendingProbe?.payload===e.detail.payload){metrics({ping:Math.round(performance.now()-pendingProbe.started)});pendingProbe=null}});
 rfb.addEventListener('connect',()=>{if(closed)return;onStatus('Desktop connected');metrics({state:'connected',ping:null});if(options.onMetrics){probe();latencyTimer=setInterval(probe,5000)}});
 rfb.addEventListener('disconnect',e=>{clearInterval(latencyTimer);pendingProbe=null;if(!closed){metrics({state:'disconnected',ping:null});onStatus(e.detail.clean?'Desktop disconnected. Reconnect to return.':'Desktop connection failed; check runtime and authentication.');options.onDisconnect?.()}});
 rfb.addEventListener('credentialsrequired',()=>{rfb.disconnect();onStatus('Unexpected desktop authentication request; check the private VNC configuration.')});
 let remoteClipboard='';
 rfb.addEventListener('clipboard',e=>{if(viewOnly)return;remoteClipboard=e.detail.text||'';onStatus('Remote clipboard ready. Choose Copy to save it to this device.')});
 const controls=options.controls||document.querySelector('#desktop-controls');controls.replaceChildren();
 const typeBar=options.typeBar;
 const icons={
  Type:'<rect x="2" y="6" width="20" height="12" rx="2"/><path d="M6 10h.01M10 10h.01M14 10h.01M18 10h.01M7 14h10"/>',
  Copy:'<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3"/>',
  Paste:'<rect x="5" y="5" width="14" height="16" rx="2"/><path d="M9 5V3h6v2M9 11h6M9 15h4"/>',
  'Address bar':'<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M7 6.5h.01M10 6.5h.01M8 14h8"/>',
  'Ctrl-Alt-Del':'<path d="M4 7h16v10H4zM8 12h8M12 9v6"/>',
  Fit:'<path d="M8 3H5a2 2 0 0 0-2 2v3M16 3h3a2 2 0 0 1 2 2v3M8 21H5a2 2 0 0 1-2-2v-3M16 21h3a2 2 0 0 0 2-2v-3"/>',
  Fullscreen:'<path d="M8 3H5a2 2 0 0 0-2 2v3M16 3h3a2 2 0 0 1 2 2v3M8 21H5a2 2 0 0 1-2-2v-3M16 21h3a2 2 0 0 0 2-2v-3"/>',
 };
 function chip(label,fn,action){const button=document.createElement('button');button.type='button';button.dataset.action=action||label.toLowerCase().replaceAll(' ','-');if(typeBar){button.innerHTML='<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">'+icons[label]+'</svg><span>'+label+'</span>'}else button.textContent=label;button.onclick=fn;controls.append(button);return button}
 if(!viewOnly){
 const sendText=value=>{for(const char of value){const code=char.codePointAt(0);rfb.sendKey(code===10?0xff0d:code>255?0x01000000+code:code)}rfb.focus()};
 if(typeBar){
  typeBar.replaceChildren();typeBar.hidden=true;
  const field=document.createElement('input');field.type='text';field.placeholder='Type into desktop';field.setAttribute('aria-label','Type into desktop');
  const send=document.createElement('button');send.type='button';send.textContent='↑';send.setAttribute('aria-label','Send text to desktop');
  send.onclick=()=>{if(field.value){sendText(field.value);field.value=''}field.focus()};
  field.onkeydown=event=>{if(event.key==='Enter'&&!event.isComposing){event.preventDefault();send.click()}};
  typeBar.append(field,send);
  chip('Type',()=>{typeBar.hidden=!typeBar.hidden;if(!typeBar.hidden)field.focus()},'type');
 }
 chip('Copy',async()=>{if(!remoteClipboard){onStatus('Copy or select text in the desktop first.');return}try{await navigator.clipboard.writeText(remoteClipboard);onStatus('Desktop clipboard copied to this device.')}catch{onStatus('Clipboard copy was blocked by the browser.')}},'copy');
 chip('Paste',async()=>{try{const value=await navigator.clipboard.readText();rfb.clipboardPasteFrom(value);rfb.focus();onStatus('Clipboard sent to the desktop. Use Ctrl+V in the app, or Ctrl+Shift+V in a terminal.')}catch{onStatus('Clipboard paste was blocked by the browser.')}},'paste');
 chip('Address bar',()=>{rfb.sendKey(0xffe3,'ControlLeft',true);rfb.sendKey(0x6c,'KeyL');rfb.sendKey(0xffe3,'ControlLeft',false);rfb.focus()},'address');
 chip('Ctrl-Alt-Del',()=>rfb.sendCtrlAltDel(),'ctrl-alt-del');
 if(!typeBar){const text=document.createElement('textarea');text.rows=1;text.placeholder='Type into desktop';text.setAttribute('aria-label','Desktop keyboard input');const type=()=>{sendText(text.value);text.value=''};text.addEventListener('input',e=>{if(!e.isComposing)type()});text.addEventListener('compositionend',type);text.addEventListener('keydown',e=>{if(e.key==='Backspace'&&!text.value){e.preventDefault();rfb.sendKey(0xff08)}});controls.append(text)}
 }
 chip('Fit',()=>{rfb.scaleViewport=!rfb.scaleViewport});
 chip('Fullscreen',async()=>{try{if(document.fullscreenElement)await document.exitFullscreen();else await root.requestFullscreen()}catch{onStatus('Fullscreen unavailable')}});
 return()=>{closed=true;clearInterval(latencyTimer);pendingProbe=null;rfb.disconnect();controls.replaceChildren();if(typeBar){typeBar.replaceChildren();typeBar.hidden=true}};
};
