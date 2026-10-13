'use strict';
(()=>{
 const $=s=>document.querySelector(s),link=location.hash.slice(1),q='?link='+encodeURIComponent(link);
 let desktop,terminal,liveKey='',frameURL,stopped=false;
 function stop(){desktop?.();terminal?.();desktop=terminal=null;liveKey=''}
 async function api(path){const r=await fetch('/v1/task-links/'+path+q,{credentials:'same-origin',cache:'no-store'});if(!r.ok)throw Error('This link is unavailable, expired or revoked.');return r}
 async function refresh(){
  try{
   const state=await(await api('state')).json();if(stopped)return;
   $('#task-title').textContent=state.name;$('#task-state').textContent=state.state+(state.queuePosition?' · Queue position '+state.queuePosition:'');
   const view=state.kind!=='control';$('#task-access').textContent=view?'View only':'Control access to this task’s desktop and terminal';
   const running=state.state==='running'&&state.box?.state==='running',key=running?state.box.id+':'+state.session:'';
   if(running&&key!==liveKey){stop();liveKey=key;$('#final-screen').hidden=true;$('#final-terminal').hidden=true;$('#desktop-screen').hidden=false;$('#terminal-screen').hidden=false;
    desktop=openWorkspaceDesktop(state.box.id,text=>$('#desktop-status').textContent=text,{viewOnly:view,streamURL:'/v1/task-links/desktop/stream'+q});
    if(state.session)terminal=openWorkspaceTerminal(state.box.id,state.session,text=>$('#terminal-status').textContent=text,{viewOnly:view,streamURL:'/v1/task-links/terminal/stream'+q});
   }else if(!running){stop();$('#desktop-screen').hidden=true;$('#terminal-screen').hidden=true;
    if(state.state!=='queued'&&state.state!=='starting'){
     const image=await fetch('/v1/task-links/snapshot'+q,{cache:'no-store'});if(image.ok){if(frameURL)URL.revokeObjectURL(frameURL);frameURL=URL.createObjectURL(await image.blob());$('#final-screen').src=frameURL;$('#final-screen').hidden=false}
     const text=await fetch('/v1/task-links/snapshot'+q+'&source=terminal',{cache:'no-store'});if(text.ok){$('#final-terminal').textContent=await text.text();$('#final-terminal').hidden=false}
    }
   }
  }catch(error){stop();$('#task-state').textContent=error.message;$('#final-screen').hidden=true;$('#final-terminal').textContent='';$('#desktop-screen').replaceChildren();$('#terminal-screen').replaceChildren()}
 }
 $('#reconnect').onclick=()=>{stop();void refresh()};void refresh();const timer=setInterval(refresh,5000);
 addEventListener('pagehide',()=>{stopped=true;clearInterval(timer);stop();if(frameURL)URL.revokeObjectURL(frameURL)},{once:true});
})();
