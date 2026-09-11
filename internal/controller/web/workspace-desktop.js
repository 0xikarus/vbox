'use strict';
window.openWorkspaceDesktop=function(boxID,onStatus,options={}){
 const root=options.root||document.querySelector('#desktop-screen');root.replaceChildren();
 const url=new URL('/v1/logical-boxes/'+encodeURIComponent(boxID)+'/desktop/stream',location.href);url.protocol=location.protocol==='https:'?'wss:':'ws:';
 const rfb=new NoVNC.default(root,url.href);rfb.scaleViewport=true;rfb.resizeSession=false;rfb.showDotCursor=true;let closed=false;
 rfb.addEventListener('connect',()=>onStatus('Desktop connected'));
 rfb.addEventListener('disconnect',e=>{if(!closed){onStatus(e.detail.clean?'Desktop disconnected. Reconnect to return.':'Desktop connection failed; check runtime and authentication.');options.onDisconnect?.()}});
 rfb.addEventListener('credentialsrequired',()=>{rfb.disconnect();onStatus('Unexpected desktop authentication request; check the private VNC configuration.')});
 let remoteClipboard='';
 rfb.addEventListener('clipboard',e=>{remoteClipboard=e.detail.text||'';onStatus('Remote clipboard ready. Choose Copy to save it to this device.')});
 const controls=options.controls||document.querySelector('#desktop-controls');controls.replaceChildren();
 // Complete each shortcut in one handler. Never hold remote modifiers while
 // waiting for another browser click (Alt changes the meaning of VNC clicks).
 function shortcut(key,code,reverse=false){
  if(closed)return;
  try{
   rfb.sendKey(0xffe9,'AltLeft',true);
   if(reverse)rfb.sendKey(0xffe1,'ShiftLeft',true);
   rfb.sendKey(key,code,true);
  }finally{
   rfb.sendKey(key,code,false);
   if(reverse)rfb.sendKey(0xffe1,'ShiftLeft',false);
   rfb.sendKey(0xffe9,'AltLeft',false);
  }
  rfb.focus();
 }
 const copy=document.createElement('button');copy.type='button';copy.textContent='Copy';copy.onclick=async()=>{if(!remoteClipboard){onStatus('Copy or select text in the desktop first.');return}try{await navigator.clipboard.writeText(remoteClipboard);onStatus('Desktop clipboard copied to this device.')}catch{onStatus('Clipboard copy was blocked by the browser.')}};controls.append(copy);
 const paste=document.createElement('button');paste.type='button';paste.textContent='Paste';paste.onclick=async()=>{try{const value=await navigator.clipboard.readText();rfb.clipboardPasteFrom(value);rfb.focus();onStatus('Clipboard pasted into the desktop.')}catch{onStatus('Clipboard paste was blocked by the browser.')}};controls.append(paste);
 const address=document.createElement('button');address.type='button';address.textContent='Address bar';address.onclick=()=>{rfb.sendKey(0xffe3,'ControlLeft',true);rfb.sendKey(0x6c,'KeyL');rfb.sendKey(0xffe3,'ControlLeft',false);rfb.focus()};controls.append(address);
 for(const [label,hint,fn] of [
  ['Previous window','Cycle backward through open windows',()=>shortcut(0xff09,'Tab',true)],
  ['Next window','Switch to the next recent window',()=>shortcut(0xff09,'Tab')],
  ['Window actions','Open the active window menu for stacking, moving and resizing',()=>shortcut(0x20,'Space')],
 ]){const b=document.createElement('button');b.type='button';b.textContent=label;b.title=hint;b.onclick=fn;controls.append(b)}
 const text=document.createElement('textarea');text.rows=1;text.placeholder='Type into desktop';text.setAttribute('aria-label','Desktop keyboard input');
 const type=()=>{for(const char of text.value){const code=char.codePointAt(0);rfb.sendKey(code===10?0xff0d:code>255?0x01000000+code:code)}text.value=''};
 text.addEventListener('input',e=>{if(!e.isComposing)type()});text.addEventListener('compositionend',type);text.addEventListener('keydown',e=>{if(e.key==='Backspace'&&!text.value){e.preventDefault();rfb.sendKey(0xff08)}});controls.append(text);
 for(const [label,fn] of [['Fit',()=>{rfb.scaleViewport=!rfb.scaleViewport}],['Fullscreen',async()=>{try{if(document.fullscreenElement)await document.exitFullscreen();else await root.requestFullscreen()}catch{onStatus('Fullscreen unavailable')}}],['Ctrl-Alt-Del',()=>rfb.sendCtrlAltDel()]]){const b=document.createElement('button');b.textContent=label;b.type='button';b.onclick=fn;controls.append(b)}
 return()=>{closed=true;rfb.disconnect();controls.replaceChildren()};
};
