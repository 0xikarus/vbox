'use strict';

// A visible terminal for one fixed login command. This is not a general shell.
window.openProfileLoginTerminal=function(id,root){
 root.replaceChildren();
 const terminal=new Terminal({cursorBlink:true,fontSize:14,scrollback:500,convertEol:false,theme:{background:'#151b26',foreground:'#f3f6fa',black:'#e7edf6',brightBlack:'#b8c5d7',blue:'#91b9fa',brightBlue:'#b8d2ff',magenta:'#e9b6f7',brightMagenta:'#f1d0fa'}});
 const fit=new FitAddon.FitAddon();terminal.loadAddon(fit);terminal.open(root);fit.fit();
 const observer=new ResizeObserver(()=>fit.fit());observer.observe(root);
 let socket,closed=false,retries=0,retryTimer=0;
 const url=new URL('/v1/login-profiles/browser/'+encodeURIComponent(id)+'/terminal',location.href);
 url.protocol=location.protocol==='https:'?'wss:':'ws:';
 const input=terminal.onData(data=>{if(socket?.readyState===WebSocket.OPEN)socket.send(new TextEncoder().encode(data))});
 const resize=terminal.onResize(size=>{if(socket?.readyState===WebSocket.OPEN)socket.send(JSON.stringify({cols:size.cols,rows:size.rows}))});
 function connect(){
  if(closed)return;
  socket=new WebSocket(url);socket.binaryType='arraybuffer';
  socket.onopen=()=>{retries=0;fit.fit();socket.send(JSON.stringify({cols:terminal.cols,rows:terminal.rows}));terminal.focus()};
  socket.onmessage=event=>{if(event.data instanceof ArrayBuffer)terminal.write(new Uint8Array(event.data))};
  socket.onclose=()=>{if(!closed&&retries++<8)retryTimer=setTimeout(connect,1000)};
 }
 connect();
 return ()=>{closed=true;clearTimeout(retryTimer);observer.disconnect();input.dispose();resize.dispose();socket?.close();terminal.dispose();root.replaceChildren()};
};
