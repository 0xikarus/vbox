'use strict';
window.openWorkspaceDesktop=function(boxID,onStatus){
 const root=document.querySelector('#desktop-screen');root.replaceChildren();
 const url=new URL('/v1/logical-boxes/'+encodeURIComponent(boxID)+'/desktop/stream',location.href);url.protocol=location.protocol==='https:'?'wss:':'ws:';
 const rfb=new NoVNC.default(root,url.href);rfb.scaleViewport=true;rfb.resizeSession=false;let closed=false;
 rfb.addEventListener('connect',()=>onStatus('Desktop connected'));
 rfb.addEventListener('disconnect',e=>{if(!closed)onStatus(e.detail.clean?'Desktop disconnected. Reconnect to return.':'Desktop connection failed; check runtime and authentication.')});
 rfb.addEventListener('credentialsrequired',()=>{rfb.disconnect();onStatus('Unexpected desktop authentication request; check the private VNC configuration.')});
 const controls=document.querySelector('#desktop-controls');controls.replaceChildren();
 const address=document.createElement('button');address.type='button';address.textContent='Address bar';address.onclick=()=>{rfb.sendKey(0xffe3,'ControlLeft',true);rfb.sendKey(0x6c,'KeyL');rfb.sendKey(0xffe3,'ControlLeft',false);rfb.focus()};controls.append(address);
 const text=document.createElement('textarea');text.rows=1;text.placeholder='Type into desktop';text.setAttribute('aria-label','Desktop keyboard input');
 const type=()=>{for(const char of text.value){const code=char.codePointAt(0);rfb.sendKey(code===10?0xff0d:code>255?0x01000000+code:code)}text.value=''};
 text.addEventListener('input',e=>{if(!e.isComposing)type()});text.addEventListener('compositionend',type);text.addEventListener('keydown',e=>{if(e.key==='Backspace'&&!text.value){e.preventDefault();rfb.sendKey(0xff08)}});controls.append(text);
 for(const [label,fn] of [['Fit',()=>{rfb.scaleViewport=!rfb.scaleViewport}],['Fullscreen',async()=>{try{if(document.fullscreenElement)await document.exitFullscreen();else await root.requestFullscreen()}catch{onStatus('Fullscreen unavailable')}}],['Ctrl-Alt-Del',()=>rfb.sendCtrlAltDel()]]){const b=document.createElement('button');b.textContent=label;b.type='button';b.onclick=fn;controls.append(b)}
 return()=>{closed=true;rfb.disconnect();controls.replaceChildren()};
};
