'use strict';
const $=s=>document.querySelector(s),boxID=decodeURIComponent(location.pathname.split('/')[2]||''),bp='/v1/logical-boxes/'+encodeURIComponent(boxID);
let epoch=0,busy=false,allocation=null,allocationKey=crypto.randomUUID();
let closeTerminal=()=>{};
let closeDesktop=()=>{};
let runID=new URLSearchParams(location.search).get('run');
let runTimer,attachedRunSession='';
async function inspectRun(version){
 if(version!==epoch)return;
 try{
  const run=await api('/v1/run-once/'+encodeURIComponent(runID));if(version!==epoch)return;
  if(run.boxId!==boxID)throw Error('Run does not belong to this box.');
  if(run.task?.finishedAt){
   const task=run.task;closeTerminal();attachedRunSession='';
   $('#workspace').hidden=false;$('#desktop').hidden=true;$('#hibernate').hidden=true;
   $('#name').textContent=task.boxName||'Run once results';
   $('#session').textContent='One-shot '+task.agent+' · '+task.state;
   $('#status').textContent=[task.exitCode!=null?'Exit code '+task.exitCode:task.signal?'Signal '+task.signal:task.state,run.boxDeleted?'Box deleted; saved results retained.':'Finished; box cleanup pending.'].join(' · ');
   $('#connect').textContent='Refresh results';
   const output=document.createElement('pre');output.textContent=task.output||'No output recorded.';
   if(task.outputTruncated)output.append(document.createTextNode('\n[Saved output truncated]'));
   $('#terminal-screen').replaceChildren(output);
   if(!run.boxDeleted)runTimer=setTimeout(()=>inspectRun(version),5000);
   return;
  }
  const box=await api(bp);if(version!==epoch)return;state(box);$('#workspace').hidden=false;
  $('#desktop').hidden=true;$('#hibernate').hidden=true;$('#connect').textContent='Reconnect / check results';
  const task=run.task;
  $('#session').textContent=task?'One-shot '+task.agent+' · '+task.session:'Waiting for the task';
  $('#status').textContent=[box.state,box.restorationState,task?.state,task?.exitCode!=null?'exit '+task.exitCode:'',run.failure,box.failureReason].filter(Boolean).join(' · ');
  if(task?.state==='running'&&box.state==='running'&&attachedRunSession!==task.session){
   closeTerminal();attachedRunSession=task.session;
   closeTerminal=openWorkspaceTerminal(boxID,task.session,message=>{if(version===epoch)$('#status').textContent=message});
  }
  runTimer=setTimeout(()=>inspectRun(version),2000);
 }catch(e){if(version===epoch)$('#error').textContent=e.message}
}
// Keep attempts for this page's lifetime: reconnect/flicker must not retry installs.
const autoDesktopRequestedForBoxIds=new Set();
let desktopBusy=false,desktopAttached=false,workspaceRole='';
function hasBlender(box){
 const tools=box.tools??box.tooling??box.metadata?.tools??[];
 return Array.isArray(tools)&&tools.some(tool=>[typeof tool==='string'?tool:tool?.id,tool?.name,tool?.label].some(label=>typeof label==='string'&&label.trim().toLowerCase()==='blender'));
}
function desktopCurrent(version){return version===epoch&&!runID&&!$('#workspace').hidden&&!$('#desktop').hidden}
async function requestDesktop({enable=false,automatic=false}={}){
 if(desktopBusy)return;
 const version=epoch;desktopBusy=true;
 $('#enable-desktop').disabled=true;$('#start-desktop').disabled=true;
 try{
  if(automatic){
   $('#desktop-status').textContent='Blender box detected, auto-opening desktop…';
   const status=await api(bp+'/desktop');if(!desktopCurrent(version))return;
   enable=!status.enabled;
  }
  if(enable){
   $('#desktop-status').textContent='Installing desktop packages (up to 3 minutes)…';
   await api(bp+'/desktop/enable','POST',{}, {},190000);if(!desktopCurrent(version))return;
   if(!automatic){$('#desktop-status').textContent='Desktop packages ready. Choose Start / reconnect desktop.';return}
  }
  $('#desktop-status').textContent='Starting desktop…';
  // Runtime start is idempotent and reuses an already active desktop session.
  await api(bp+'/desktop','POST',{});if(!desktopCurrent(version))return;
  closeDesktop();desktopAttached=true;
  const dispose=openWorkspaceDesktop(boxID,message=>{if(desktopCurrent(version))$('#desktop-status').textContent=message});
  closeDesktop=()=>{desktopAttached=false;dispose()};
 }catch(e){
  if(desktopCurrent(version))$('#desktop-status').textContent=automatic?'Automatic desktop launch failed. Use the terminal or manual desktop controls. '+e.message:e.message;
 }finally{desktopBusy=false;$('#enable-desktop').disabled=false;$('#start-desktop').disabled=false}
}
function maybeAutoDesktop(box,version){
 if(!desktopCurrent(version)||workspaceRole!=='owner'||box.state!=='running'||box.failureReason||desktopBusy||desktopAttached||autoDesktopRequestedForBoxIds.has(boxID)||!hasBlender(box))return;
 autoDesktopRequestedForBoxIds.add(boxID);
 void requestDesktop({automatic:true});
}
$('#enable-desktop').onclick=()=>{if(confirm('Install desktop packages on this worker? This uses additional disk space and downloading may take a few minutes.'))void requestDesktop({enable:true})};
$('#start-desktop').onclick=()=>requestDesktop();
async function api(path,method='GET',body,headers={},timeout=60000){
 let r;try{r=await fetch(path,{method,credentials:'same-origin',headers:{'Content-Type':'application/json',...headers},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(timeout)})}catch{throw Error('Controller connection interrupted. The operation may still be running. Reload or use Resume / reconnect to check its state. Terminal input is not replayed.')}
 if(r.status===401){$('#login').hidden=false;throw Error('Please log in to the controller.')}
 if(!r.ok){let e;try{e=await r.json()}catch{}throw Error(e?.error||'Request failed: '+r.status)}return r.status===204?null:r.json();
}
function state(b){$('#name').textContent=b.name;document.title=b.name+' · vmbox';$('#status').textContent=[b.state,b.restorationState,b.failureReason].filter(Boolean).join(' · ')}
async function connect(){
 if(runID){clearTimeout(runTimer);closeTerminal();attachedRunSession='';$('#error').textContent='';await inspectRun(++epoch);return}
 if(busy)return;busy=true;const version=++epoch;$('#connect').disabled=true;$('#error').textContent='';
 try{
  const run=await api(bp+'/run-once');if(version!==epoch)return;
  if(run?.id){runID=run.id;history.replaceState(null,'','?run='+encodeURIComponent(runID));await inspectRun(version);return}
  let box=await api(bp);if(version!==epoch)return;state(box);$('#workspace').hidden=false;
  if(box.state!=='running'){
   if(!allocation)allocation=await api(bp+'/allocate','POST',{leaseOwner:'web'},{'Idempotency-Key':allocationKey});
   const deadline=Date.now()+180000;
   while(allocation.state!=='ready'){
    if(version!==epoch)return;
    $('#status').textContent=[box.state,allocation.phase,allocation.state,allocation.queuePosition?'queue '+allocation.queuePosition:''].filter(Boolean).join(' · ');
    if(['failed','cancelled'].includes(allocation.state)){const message=allocation.failureReason||allocation.state;allocation=null;allocationKey=crypto.randomUUID();throw Error(message)}
    if(Date.now()>deadline)throw Error('Resume observation timed out. Reconnect to check the same request; the controller may still be working.');
    await new Promise(r=>setTimeout(r,1500));if(version!==epoch)return;
    allocation=await api('/v1/allocations/'+encodeURIComponent(allocation.requestId));
   }
   box=await api(bp);if(version!==epoch)return;state(box);
  }
  const session=await api(bp+'/sessions/interactive','POST',{agent:'shell',reuseShell:true});if(version!==epoch)return;
  $('#session').textContent='Persistent shell: '+session.session;
  closeTerminal();closeTerminal=openWorkspaceTerminal(boxID,session.session,message=>{if(version===epoch)$('#status').textContent=message});
  maybeAutoDesktop(box,version);
 }catch(e){if(version===epoch)$('#error').textContent=e.message}finally{busy=false;$('#connect').disabled=false}
}
$('#connect').onclick=connect;
$('#login').onsubmit=async e=>{e.preventDefault();try{await api('/v1/browser-session','POST',{}, {Authorization:'Bearer '+e.target.elements.token.value});e.target.reset();workspaceRole=(await api('/v1/whoami')).role;$('#login').hidden=true;await connect()}catch(e){$('#error').textContent=e.message}};
$('#logout').onclick=async()=>{epoch++;closeTerminal();closeDesktop();try{await api('/v1/browser-session','DELETE');$('#workspace').hidden=true;$('#login').hidden=false;$('#status').textContent='Logged out. The box was not stopped.'}catch(e){$('#error').textContent=e.message}};
$('#hibernate').onclick=async()=>{if(busy||!confirm('Hibernate this box? Running processes will stop; workspace files are retained.'))return;epoch++;closeTerminal();closeDesktop();try{state(await api(bp+'/hibernate','POST',{}));allocation=null;allocationKey=crypto.randomUUID();$('#session').textContent='';$('#terminal-screen').replaceChildren()}catch(e){$('#error').textContent=e.message}};
window.addEventListener('pagehide',()=>{epoch++;clearTimeout(runTimer);closeTerminal();closeDesktop()});
(async()=>{try{workspaceRole=(await api('/v1/whoami')).role;$('#login').hidden=true;await connect()}catch(e){$('#error').textContent=e.message}})();
