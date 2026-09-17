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
 rfb.addEventListener('clipboard',e=>{remoteClipboard=e.detail.text||'';onStatus('Remote clipboard ready. Choose Copy to save it to this device.')});
 const controls=options.controls||document.querySelector('#desktop-controls');controls.replaceChildren();
 if(!viewOnly){
 const copy=document.createElement('button');copy.type='button';copy.textContent='Copy';copy.dataset.action='copy';copy.onclick=async()=>{if(!remoteClipboard){onStatus('Copy or select text in the desktop first.');return}try{await navigator.clipboard.writeText(remoteClipboard);onStatus('Desktop clipboard copied to this device.')}catch{onStatus('Clipboard copy was blocked by the browser.')}};controls.append(copy);
 const paste=document.createElement('button');paste.type='button';paste.textContent='Paste';paste.dataset.action='paste';paste.onclick=async()=>{try{const value=await navigator.clipboard.readText();rfb.clipboardPasteFrom(value);rfb.focus();onStatus('Clipboard sent to the desktop. Use Ctrl+V in the app, or Ctrl+Shift+V in a terminal.')}catch{onStatus('Clipboard paste was blocked by the browser.')}};controls.append(paste);
 const address=document.createElement('button');address.type='button';address.textContent='Address bar';address.onclick=()=>{rfb.sendKey(0xffe3,'ControlLeft',true);rfb.sendKey(0x6c,'KeyL');rfb.sendKey(0xffe3,'ControlLeft',false);rfb.focus()};controls.append(address);
 const text=document.createElement('textarea');text.rows=1;text.placeholder='Type into desktop';text.setAttribute('aria-label','Desktop keyboard input');
 const type=()=>{for(const char of text.value){const code=char.codePointAt(0);rfb.sendKey(code===10?0xff0d:code>255?0x01000000+code:code)}text.value=''};
 text.addEventListener('input',e=>{if(!e.isComposing)type()});text.addEventListener('compositionend',type);text.addEventListener('keydown',e=>{if(e.key==='Backspace'&&!text.value){e.preventDefault();rfb.sendKey(0xff08)}});controls.append(text);
 }
 for(const [label,fn] of [['Fit',()=>{rfb.scaleViewport=!rfb.scaleViewport}],['Fullscreen',async()=>{try{if(document.fullscreenElement)await document.exitFullscreen();else await root.requestFullscreen()}catch{onStatus('Fullscreen unavailable')}}],...(viewOnly?[]:[['Ctrl-Alt-Del',()=>rfb.sendCtrlAltDel()]])]){const b=document.createElement('button');b.textContent=label;b.type='button';b.onclick=fn;controls.append(b)}
 return()=>{closed=true;clearInterval(latencyTimer);pendingProbe=null;rfb.disconnect();controls.replaceChildren()};
};
