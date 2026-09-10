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
 // Keep Alt held while the picker is open so repeated clicks walk the whole
 // Openbox window list instead of toggling between the two most recent windows.
 let switching=false;
 const windows=document.createElement('button');windows.type='button';windows.textContent='Windows';windows.setAttribute('aria-expanded','false');
 const switcher=document.createElement('span');switcher.hidden=true;switcher.setAttribute('role','group');switcher.setAttribute('aria-label','Switch desktop window');
 function finishSwitch(cancel=false){
  if(!switching)return;
  if(cancel)rfb.sendKey(0xff1b,'Escape');
  rfb.sendKey(0xffe9,'AltLeft',false);switching=false;switcher.hidden=true;windows.setAttribute('aria-expanded','false');
 }
 function cycleWindow(reverse=false){
  if(closed)return;
  if(!switching){rfb.sendKey(0xffe9,'AltLeft',true);switching=true;switcher.hidden=false;windows.setAttribute('aria-expanded','true')}
  if(reverse)rfb.sendKey(0xffe1,'ShiftLeft',true);
  try{rfb.sendKey(0xff09,'Tab')}finally{if(reverse)rfb.sendKey(0xffe1,'ShiftLeft',false)}
 }
 windows.onclick=()=>{if(switching){finishSwitch();rfb.focus()}else cycleWindow()};
 for(const [label,fn] of [['Previous',()=>cycleWindow(true)],['Next',()=>cycleWindow()],['Select window',()=>{finishSwitch();rfb.focus()}],['Cancel',()=>{finishSwitch(true);rfb.focus()}]]){const b=document.createElement('button');b.type='button';b.textContent=label;b.onclick=fn;switcher.append(b)}
 switcher.onkeydown=e=>{if(e.key==='Escape'){e.preventDefault();finishSwitch(true);windows.focus()}};
 const cancelSwitch=()=>finishSwitch(true),selectSwitch=()=>finishSwitch();
 const otherControl=e=>{if(e.target!==windows&&!switcher.contains(e.target))finishSwitch()};
 const leaveViewer=e=>{if(!controls.contains(e.target)&&!root.contains(e.target))finishSwitch(true)};
 controls.addEventListener('click',otherControl,true);
 document.addEventListener('pointerdown',leaveViewer,true);document.addEventListener('focusin',leaveViewer);
 window.addEventListener('blur',cancelSwitch);root.addEventListener('pointerdown',selectSwitch,true);
 const copy=document.createElement('button');copy.type='button';copy.textContent='Copy';copy.onclick=async()=>{if(!remoteClipboard){onStatus('Copy or select text in the desktop first.');return}try{await navigator.clipboard.writeText(remoteClipboard);onStatus('Desktop clipboard copied to this device.')}catch{onStatus('Clipboard copy was blocked by the browser.')}};controls.append(copy);
 const paste=document.createElement('button');paste.type='button';paste.textContent='Paste';paste.onclick=async()=>{try{const value=await navigator.clipboard.readText();rfb.clipboardPasteFrom(value);rfb.focus();onStatus('Clipboard pasted into the desktop.')}catch{onStatus('Clipboard paste was blocked by the browser.')}};controls.append(paste);
 const address=document.createElement('button');address.type='button';address.textContent='Address bar';address.onclick=()=>{rfb.sendKey(0xffe3,'ControlLeft',true);rfb.sendKey(0x6c,'KeyL');rfb.sendKey(0xffe3,'ControlLeft',false);rfb.focus()};controls.append(address);
 controls.append(windows,switcher);
 const text=document.createElement('textarea');text.rows=1;text.placeholder='Type into desktop';text.setAttribute('aria-label','Desktop keyboard input');
 const type=()=>{for(const char of text.value){const code=char.codePointAt(0);rfb.sendKey(code===10?0xff0d:code>255?0x01000000+code:code)}text.value=''};
 text.addEventListener('input',e=>{if(!e.isComposing)type()});text.addEventListener('compositionend',type);text.addEventListener('keydown',e=>{if(e.key==='Backspace'&&!text.value){e.preventDefault();rfb.sendKey(0xff08)}});controls.append(text);
 for(const [label,fn] of [['Fit',()=>{rfb.scaleViewport=!rfb.scaleViewport}],['Fullscreen',async()=>{try{if(document.fullscreenElement)await document.exitFullscreen();else await root.requestFullscreen()}catch{onStatus('Fullscreen unavailable')}}],['Ctrl-Alt-Del',()=>rfb.sendCtrlAltDel()]]){const b=document.createElement('button');b.textContent=label;b.type='button';b.onclick=fn;controls.append(b)}
 return()=>{finishSwitch(true);closed=true;controls.removeEventListener('click',otherControl,true);document.removeEventListener('pointerdown',leaveViewer,true);document.removeEventListener('focusin',leaveViewer);window.removeEventListener('blur',cancelSwitch);root.removeEventListener('pointerdown',selectSwitch,true);rfb.disconnect();controls.replaceChildren()};
};
