'use strict';
const $=s=>document.querySelector(s),boxID=decodeURIComponent(location.pathname.split('/')[2]||''),bp='/v1/logical-boxes/'+encodeURIComponent(boxID);
let epoch=0,busy=false,allocation=null,allocationKey=crypto.randomUUID();
let closeTerminal=()=>{},terminalAttached=false,terminalBusy=null;
let closeDesktop=()=>{},desktopBusy=false,desktopAttached=false;
let refreshDesktopPreview=()=>{};
let selectedWorkspaceView='',workspaceRole='';
const workspaceUsage=window.VMBoxWorkspaceNav?.init({usageId:'workspace-usage',providersId:'workspace-providers'});
const workspaceMascot=window.VBoxMascot?.Mascot?new window.VBoxMascot.Mascot($('.workspace-avatar-mascot'),boxID):null;
function showLogin(message=''){workspaceUsage?.setOwner(false);$('#login').hidden=false;$('#login-error').textContent=message;$('#login-token').focus()}

let boxSummary=null,controllerPing=null,statsTimer,statsGeneration=0;
const viewerStats={desktop:{state:'disconnected',ping:null},terminal:{state:'disconnected'}};
function renderStats(){
 const row=$('#connection-stats');if(!row)return;
 row.hidden=!boxSummary||boxSummary.state!=='running';
 const current=viewerStats[selectedWorkspaceView]||{state:'connecting'};
 const fields=[['Box',boxSummary?.state||'—'],['Provider',boxSummary?.provider||'—'],['Viewer',(selectedWorkspaceView==='desktop'?'Desktop':'TMUX')+' · '+current.state],['Desktop ping',viewerStats.desktop.ping==null?'—':viewerStats.desktop.ping+' ms'],['Controller',controllerPing==null?'—':controllerPing+' ms']];
 row.replaceChildren(...fields.map(([label,value])=>{const item=document.createElement('span');if(label==='Box'){const dot=document.createElement('i');dot.className='stat-dot';dot.dataset.state=String(value);item.append(dot)}const l=document.createElement('small');l.textContent=label;const v=document.createElement('b');v.textContent=value;if(value==='—'||value==null)v.className='muted';item.append(l,v);if(label==='Desktop ping')item.title='Round trip to the box over the live VNC connection. Includes transport and server response time.';if(label==='Controller')item.title='HTTP round trip to the controller; this does not measure the worker.';return item}));
 const chips=$('#viewer-chips');
 if(chips){
  chips.hidden=row.hidden;
  const bits=[];
  bits.push((selectedWorkspaceView==='desktop'?'Desktop':'TMUX')+' · '+current.state);
  if(viewerStats.desktop.ping!=null)bits.push('Live · '+viewerStats.desktop.ping+' ms');
  if(controllerPing!=null)bits.push('Controller · '+controllerPing+' ms');
  chips.replaceChildren(...bits.map(text=>{const chip=document.createElement('span');chip.className='viewer-chip';chip.textContent=text;return chip}));
 }
}
function recordViewer(view,value){Object.assign(viewerStats[view],value);renderStats()}
function stopStats(){statsGeneration++;clearTimeout(statsTimer);controllerPing=null;renderStats()}
function startStats(){
 stopStats();const generation=statsGeneration;
 async function sample(){
  if(generation!==statsGeneration)return;
  if(!document.hidden){
   const started=performance.now();let ping=null;
   try{const response=await fetch('/healthz',{cache:'no-store',credentials:'same-origin',signal:AbortSignal.timeout(5000)});await response.text();if(response.ok)ping=Math.round(performance.now()-started)}catch{}
   if(generation!==statsGeneration)return;controllerPing=ping;renderStats();
  }
  statsTimer=setTimeout(sample,5000);
 }
 void sample();
}

function workspaceCurrent(version){return version===epoch&&!$('#workspace').hidden}
function showWorkspaceView(view){
 selectedWorkspaceView=view;renderStats();
 const desktop=view==='desktop';
 $('#desktop').hidden=!desktop;$('#terminal').hidden=desktop;
 $('#desktop-tab').setAttribute('aria-selected',String(desktop));
 $('#terminal-tab').setAttribute('aria-selected',String(!desktop));
}
function showInteractiveWorkspace(){$('#workspace').hidden=false;$('#workspace-tabs').hidden=false}
async function ensureTerminal(version=epoch){
 if(!workspaceCurrent(version)||terminalAttached)return terminalAttached;
 if(terminalBusy)return terminalBusy;
 const agent=boxSummary?.defaultAgent||'shell';
 $('#session').textContent='Connecting '+agent+'…';
 const pending=(async()=>{
  try{
   const session=await api(bp+'/sessions/interactive','POST',{agent,reuseExisting:true});if(!workspaceCurrent(version))return false;
   $('#session').textContent='';
   closeTerminal();terminalAttached=true;recordViewer('terminal',{state:'connecting'});
   const dispose=openWorkspaceTerminal(boxID,session.session,message=>{if(workspaceCurrent(version))$('#status').textContent=message},{autoFocus:false,onMetrics:value=>{if(!$('#workspace').hidden)recordViewer('terminal',value)}});
   closeTerminal=()=>{terminalAttached=false;recordViewer('terminal',{state:'disconnected'});dispose()};
   return true;
  }catch(e){if(workspaceCurrent(version))$('#error').textContent=e.message;return false}
 })();
 terminalBusy=pending;
 try{return await pending}finally{if(terminalBusy===pending)terminalBusy=null}
}
function selectTerminal(version=epoch){showWorkspaceView('terminal');return ensureTerminal(version)}

// Keep automatic attempts for this page's lifetime so reconnects cannot loop.
const autoDesktopRequestedForBoxIds=new Set();
function hasBlender(box){
 const tools=box.tools??box.tooling??box.metadata?.tools??[];
 return Array.isArray(tools)&&tools.some(tool=>[typeof tool==='string'?tool:tool?.id,tool?.name,tool?.label].some(label=>typeof label==='string'&&label.trim().toLowerCase()==='blender'));
}
async function startAndAttachDesktop(version){
 $('#desktop-status').textContent='Starting desktop…';
 await api(bp+'/desktop','POST',{});if(!workspaceCurrent(version))return false;
 closeDesktop();desktopAttached=true;recordViewer('desktop',{state:'connecting',ping:null});
 const dispose=openWorkspaceDesktop(boxID,message=>{if(workspaceCurrent(version))$('#desktop-status').textContent=message},{onMetrics:value=>{if(!$('#workspace').hidden)recordViewer('desktop',value)}});
 closeDesktop=()=>{desktopAttached=false;recordViewer('desktop',{state:'disconnected',ping:null});dispose()};
 refreshDesktopPreview();
 return true;
}
async function requestDesktop({enable=false,automatic=false,tryStartBeforeEnable=false}={}){
 if(desktopBusy)return false;
 const version=epoch;desktopBusy=true;
 $('#enable-desktop').disabled=true;$('#start-desktop').disabled=true;
 try{
  if(automatic)$('#desktop-status').textContent=enable?'Blender box detected, enabling and opening desktop…':'Desktop enabled, connecting…';
  if(tryStartBeforeEnable){
   try{return await startAndAttachDesktop(version)}catch{if(!workspaceCurrent(version))return false;enable=true}
  }
  if(enable){
   $('#desktop-status').textContent='Installing desktop packages (up to 3 minutes)…';
   await api(bp+'/desktop/enable','POST',{}, {},190000);if(!workspaceCurrent(version))return false;
   if(!automatic){$('#desktop-status').textContent='Desktop packages ready. Choose Start / reconnect desktop.';return true}
  }
  return await startAndAttachDesktop(version);
 }catch(e){
  if(workspaceCurrent(version))$('#desktop-status').textContent=automatic?'Automatic desktop launch failed. Use the TMUX tab or manual desktop controls. '+e.message:e.message;
  return false;
 }finally{desktopBusy=false;$('#enable-desktop').disabled=false;$('#start-desktop').disabled=false}
}
async function openPreferredView(box,version){
 showInteractiveWorkspace();
 // Select the preferred viewer before waiting for the background TMUX session.
 // On phones the terminal otherwise fills the screen while Desktop connects.
 if(workspaceRole==='owner'&&!selectedWorkspaceView)showWorkspaceView('desktop');
 await ensureTerminal(version);
 if(!workspaceCurrent(version))return;
 if(workspaceRole!=='owner'){await selectTerminal(version);return}
 if(desktopAttached){showWorkspaceView('desktop');return}
 if(terminalAttached&&selectedWorkspaceView==='terminal'){showWorkspaceView('terminal');return}
 if(autoDesktopRequestedForBoxIds.has(boxID)){await selectTerminal(version);return}
 showWorkspaceView('desktop');$('#desktop-status').textContent='Checking desktop availability…';
 let status;
 try{status=await api(bp+'/desktop');if(!workspaceCurrent(version))return}
 catch{
  if(hasBlender(box)){
   autoDesktopRequestedForBoxIds.add(boxID);
   if(await requestDesktop({automatic:true,tryStartBeforeEnable:true}))return;
  }
  await selectTerminal(version);return;
 }
 if(status.enabled||hasBlender(box)){
  autoDesktopRequestedForBoxIds.add(boxID);
  if(await requestDesktop({automatic:true,enable:!status.enabled}))return;
 }
 await selectTerminal(version);
}

$('#terminal-tab').onclick=()=>void selectTerminal();
$('#desktop-tab').onclick=()=>showWorkspaceView('desktop');
$('#enable-desktop').onclick=()=>{if(confirm('Install desktop packages on this worker? This uses additional disk space and downloading may take a few minutes.'))void requestDesktop({enable:true})};
$('#start-desktop').onclick=()=>{showWorkspaceView('desktop');void requestDesktop()};
async function api(path,method='GET',body,headers={},timeout=60000){
 let r;try{r=await fetch(path,{method,credentials:'same-origin',headers:{'Content-Type':'application/json',...headers},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(timeout)})}catch{throw Error('Controller connection interrupted. The operation may still be running. Reload or use Resume / reconnect to check its state. Terminal input is not replayed.')}
 if(r.status===401){showLogin('Please log in to the controller.');throw Error('Please log in to the controller.')}
 if(!r.ok){let e;try{e=await r.json()}catch{}throw Error(e?.error||'Request failed: '+r.status)}return r.status===204?null:r.json();
}
let resourceSnapshot=null,connectionEndpoint='';
function resetBoxSettings(){resourceSnapshot=null;connectionEndpoint='';$('#resource-form').hidden=true;$('#connection-details').hidden=true;$('#resource-status').textContent='';$('#connection-status').textContent=''}
const workspaceResources=$('.workspace-resources');
let resourceUsage=null,resourceUsageLoaded=false,resourcesInView=false,resourceUsageTimer,resourceUsageGeneration=0;
const resourceGiB=bytes=>{const value=bytes/(1024**3);return Number.isInteger(value)?String(value):value.toFixed(1)};
function renderWorkspaceResources(){
 const rows=$('#workspace-resource-rows');rows.replaceChildren();
 const data=resourceUsage,resources=data?.resources||{};
 for(const kind of ['ram','swap','disk']){
  const total=kind==='ram'?Number(resources.memoryMiB||0)*1024**2:kind==='swap'?Number(resources.swapMiB||0)*1024**2:Number.isFinite(data?.diskTotalBytes)?data.diskTotalBytes:Number(resources.diskGiB||0)*1024**3;
  const used=kind==='ram'?data?.memoryUsedBytes:kind==='swap'?data?.swapUsedBytes:data?.diskUsedBytes;
  const measured=Number.isFinite(used)&&used>=0,ratio=measured&&total>0?used/total:null;
  const row=document.createElement('div'),label=document.createElement('span'),value=document.createElement('strong'),track=document.createElement('div'),fill=document.createElement('span');
  row.className='inspect-resource-row';row.dataset.kind=kind;
  label.textContent=kind==='ram'?'RAM':kind==='swap'?'Swap':'Disk';
  value.textContent=(measured?resourceGiB(used):'–')+' / '+(total?resourceGiB(total):'–')+' GB';
  track.className='inspect-resource-track';fill.style.width=(ratio===null?0:Math.max(0,Math.min(100,ratio*100)))+'%';track.append(fill);
  row.classList.toggle('resource-warning',ratio!==null&&ratio>=.85&&(kind==='disk'&&data?.diskEnforced===false||ratio<.95));
  row.classList.toggle('resource-danger',ratio!==null&&ratio>=.95&&!(kind==='disk'&&data?.diskEnforced===false));
  row.append(label,value,track);
  if(kind==='disk'&&data?.diskEnforced===false){const note=document.createElement('small');note.className='inspect-resource-note';note.textContent='Limit not enforced on this shared worker';row.append(note)}
  rows.append(row);
 }
 const context=$('#workspace-resources-context'),hostFreeKnown=Number.isFinite(data?.hostDiskFreeBytes)&&data?.hostDiskTotalBytes>0;
 const host=hostFreeKnown?'Host disk '+resourceGiB(data.hostDiskFreeBytes)+' GB free of '+resourceGiB(data.hostDiskTotalBytes)+' GB':Number.isFinite(data?.hostDiskUsedBytes)&&data?.hostDiskTotalBytes>0?'Host disk '+Math.round(data.hostDiskUsedBytes/data.hostDiskTotalBytes*100)+'%':'';
 const workspace=Number.isFinite(data?.diskWorkspaceBytes)?'Workspace '+resourceGiB(data.diskWorkspaceBytes)+' GB':null,layer=Number.isFinite(data?.diskWritableBytes)?'Writable layer '+resourceGiB(data.diskWritableBytes)+' GB':null;
 const diskAge=data?.diskObservedAt&&Number.isFinite(Date.parse(data.diskObservedAt))?'Disk measured '+new Date(data.diskObservedAt).toLocaleTimeString([], {hour:'numeric',minute:'2-digit'}):null;
 context.textContent=[host,workspace,layer,diskAge,data?.diskPartial?'Disk scan partial':null,data?.diskUnavailableReason||null].filter(Boolean).join(' · ');context.hidden=!context.textContent;
 context.classList.toggle('resource-danger',hostFreeKnown&&data.hostDiskFreeBytes/data.hostDiskTotalBytes<.1);
 const observed=[data?.observedAt,data?.diskObservedAt].filter(value=>value&&Number.isFinite(Date.parse(value))).sort().at(-1);
 $('#workspace-resources-updated').textContent=observed?'Updated '+new Date(observed).toLocaleTimeString([], {hour:'numeric',minute:'2-digit'}):resourceUsageLoaded?'No live data (worker update needed)':'Live metrics load when this card is visible';
 $('#workspace-resources-adjust').hidden=workspaceRole!=='owner'||boxSummary?.provider!=='shared-worker'||boxSummary?.state!=='running';
}
function resourcesVisible(){return resourcesInView&&!document.hidden&&!$('#workspace').hidden&&boxSummary?.state==='running'}
function stopResourceUsage(){resourceUsageGeneration++;clearTimeout(resourceUsageTimer)}
function scheduleResourceUsage(delay=0){clearTimeout(resourceUsageTimer);if(resourcesVisible())resourceUsageTimer=setTimeout(refreshResourceUsage,delay)}
async function refreshResourceUsage(){
 if(!resourcesVisible())return;
 const generation=++resourceUsageGeneration;
 try{const data=await api(bp+'/resources');if(generation===resourceUsageGeneration&&resourcesVisible()){resourceUsage=data;resourceUsageLoaded=true;renderWorkspaceResources()}}
 catch{if(generation===resourceUsageGeneration&&resourcesVisible()){resourceUsage=null;resourceUsageLoaded=true;renderWorkspaceResources()}}
 finally{if(generation===resourceUsageGeneration)scheduleResourceUsage(30000)}
}
new IntersectionObserver(entries=>{resourcesInView=entries[0]?.isIntersecting||false;if(resourcesInView)scheduleResourceUsage(0);else stopResourceUsage()}).observe(workspaceResources);
document.addEventListener('visibilitychange',()=>{if(document.hidden)stopResourceUsage();else scheduleResourceUsage(0)});
window.addEventListener('pagehide',stopResourceUsage);
$('#workspace-resources-adjust').onclick=()=>{const settings=$('#box-settings');settings.open=true;$('#load-resources').click();settings.scrollIntoView({block:'nearest'});settings.querySelector('summary').focus({preventScroll:true})};
function renderForward(){
 const port=Number($('#forward-port').value);
 const valid=Number.isInteger(port)&&port>0&&port<=65535&&/^[a-zA-Z0-9_][a-zA-Z0-9_.-]*@[a-zA-Z0-9][a-zA-Z0-9.-]*$/.test(connectionEndpoint);
 $('#forward-command').textContent=valid?'ssh -N -o ExitOnForwardFailure=yes -L 127.0.0.1:'+port+':127.0.0.1:'+port+' '+connectionEndpoint:'';
 $('#forward-address').textContent=valid?'Local address: http://127.0.0.1:'+port+' (for HTTP applications)':'Enter a port from 1 to 65535. SSH forwarding requires an OpenSSH endpoint.';
}
$('#forward-port').oninput=renderForward;
$('#load-connection').onclick=async()=>{
 const version=epoch;$('#load-connection').disabled=true;$('#connection-details').hidden=true;$('#connection-status').textContent='Resolving current connection…';
 try{
  const result=await api(bp+'/connection');if(!workspaceCurrent(version))return;
  const conn=result.connection;if(!conn?.endpoint)throw Error('No connection address available.');
  const ssh=conn.transport==='openssh';
  connectionEndpoint=ssh?conn.endpoint:'';$('#connection-address').textContent=ssh?'SSH address: '+conn.endpoint:'Worker connection: '+(conn.transport||'unknown')+' through the controller';
  $('#connection-location').textContent='Region: '+(conn.metadata?.vmboxRegion||'Unknown');
  $('#ssh-forwarding').hidden=!ssh;$('#connection-details').hidden=false;
  $('#connection-status').textContent=ssh?'Address resolved for the current worker. Reload after the box moves or resumes.':'Direct worker connection resolved. This endpoint does not provide SSH port forwarding.';renderForward();
 }catch(e){if(workspaceCurrent(version))$('#connection-status').textContent=e.message}
 finally{$('#load-connection').disabled=false}
};
$('#load-resources').onclick=async()=>{
 const version=epoch;resourceSnapshot=null;$('#resource-form').hidden=true;$('#load-resources').disabled=true;$('#resource-status').textContent='Loading configured limits…';
 try{
  const data=await api(bp+'/resources');if(!workspaceCurrent(version))return;
  resourceSnapshot={...data,version};const f=$('#resource-form');f.elements.cpu.value=data.resources.cpu||'';f.elements.memoryMiB.value=data.resources.memoryMiB||'';f.hidden=false;
  $('#resource-status').textContent='Configured slot limits. Live container limits may differ until a later restart.';
 }catch(e){if(workspaceCurrent(version))$('#resource-status').textContent=e.message}
 finally{$('#load-resources').disabled=false}
};
$('#resource-form').onsubmit=async event=>{
 event.preventDefault();const snapshot=resourceSnapshot;if(!snapshot||!workspaceCurrent(snapshot.version)){$('#resource-status').textContent='Reload limits before saving.';return}
 const form=event.target, cpu=Number(form.elements.cpu.value),memoryMiB=Number(form.elements.memoryMiB.value);
 if(!confirm('Set this slot to '+cpu+' vCPU and '+memoryMiB+' MiB RAM? This can change cost. Reducing RAM can terminate applications. No worker restart will be requested.'))return;
 resourceSnapshot=null;form.querySelector('button').disabled=true;$('#load-resources').disabled=true;
 try{
  const result=await api(bp+'/resources','PUT',{slotId:snapshot.slotId,assignmentGeneration:snapshot.assignmentGeneration,cpu,memoryMiB});
  if(workspaceCurrent(snapshot.version))$('#resource-status').textContent=result.message;
 }catch(e){if(workspaceCurrent(snapshot.version))$('#resource-status').textContent=e.message+' Reload limits before retrying.'}
 finally{form.querySelector('button').disabled=false;$('#load-resources').disabled=false}
};
const TRASH_ICON='<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 6h18"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><line x1="10" y1="11" x2="10" y2="17"/><line x1="14" y1="11" x2="14" y2="17"/></svg>';
// Only offer an action the box state can actually satisfy: never connect or
// resume a box that is still being created or is mid-transition.
function boxPhase(state){if(state==='running')return 'running';if(state==='failed')return 'failed';if(state==='reserved'||state==='attaching')return 'creating';if(state==='deleting')return 'deleting';if(state==='hibernating'||state==='draining')return 'transitioning';return 'stopped'}
let deletePending=false;
function statusLine(b){const phase=boxPhase(b.state);const hint=phase==='creating'?'being created; connect becomes available when it is running':phase==='transitioning'?'transitioning; this page updates automatically':phase==='deleting'?'being deleted':'';return [b.state,b.restorationState,b.failureReason,hint].filter(Boolean).join(' · ')}
function applyBoxState(b){
 boxSummary=b;renderStats();
 if(b.state!=='running'){stopResourceUsage();resourceUsage=null;resourceUsageLoaded=false}renderWorkspaceResources();scheduleResourceUsage(0);
 $('.workspace-top-chat').href='/chat#box='+encodeURIComponent(b.id);
 if(workspaceMascot){const mood=b.state==='hibernated'?'sleeping':b.state==='failed'?'angry':['reserved','attaching'].includes(b.state)?'waking':'idle';workspaceMascot.jump(mood)}
 $('#name').textContent=b.name;document.title='vbox / workspace / '+b.name;
 const phase=boxPhase(b.state),owner=workspaceRole==='owner',retryable=phase==='failed'&&b.restorationState==='attach-timed-out'&&!b.volumeId?.startsWith('pending:'),connectable=phase==='running'||phase==='stopped'||retryable;
 $('#status').textContent=statusLine(b);
 $('#connect').hidden=!connectable;
 $('#connect').textContent=phase==='running'?'Reconnect viewers':retryable?'Retry wake':'Resume box';
 $('#hibernate').hidden=!(phase==='running'&&owner);
 $('#box-settings').hidden=!(phase==='running'&&owner);
 $('#box-contacts').hidden=!owner;
 $('#workspace-idle-policy').hidden=!owner;
 if(owner)window.VMBoxIdlePolicy?.mount($('#workspace-idle-policy'),{boxId:b.id,boxName:b.name,request:seconds=>api(bp+'/idle-policy',seconds===undefined?'GET':'PUT',seconds===undefined?undefined:{seconds})});
 $('#workspace-run-budget-policy').hidden=!owner;
 if(owner)window.VMBoxRunBudgetPolicy?.mount($('#workspace-run-budget-policy'),{boxId:b.id,
  request:seconds=>api(bp+'/run-budget-policy',seconds===undefined?'GET':'PUT',seconds===undefined?undefined:{seconds}),
  adjust:(action,seconds,expectedDeadlineAt)=>api(bp+'/run-budget-policy/adjust','POST',{action,seconds,expectedDeadlineAt})});
 $('#delete-box').hidden=!owner||!(phase==='running'||phase==='stopped'||phase==='failed');
 $('#delete-box').disabled=deletePending||phase==='deleting';
 $('#lifecycle-note').textContent={
  running:'Closing this page leaves the box running. Hibernate stops its processes and retains workspace files.',
  stopped:'This page does not start the box. Resume box explicitly requests compute; workspace files remain saved.',
  failed:retryable?'The timed-out wake can be retried on its original worker slot. Your workspace files are retained.':'This box failed to start. Read the reason above before deleting the box and its workspace.',
  creating:'This box is being created. This page updates automatically; connecting becomes available when it is running.',
  transitioning:'This box is transitioning. This page updates automatically.',
  deleting:'This box is being deleted. Its workspace volume will be removed.'
 }[phase];
}
function showSleepingWorkspace(message){
 stopStats();$('#workspace').hidden=false;$('#workspace-tabs').hidden=true;$('#terminal').hidden=true;$('#desktop').hidden=true;$('#hibernate').hidden=true;
 $('#session').textContent=message||'Saved workspace. Resume the box to connect.';
}
// Watch a box that is already in motion (creating, hibernating, draining,
// deleting) instead of offering a resume action it cannot honour.
async function observeBox(box,version){
 const deadline=Date.now()+180000;
 for(;;){
  if(version!==epoch)return;
  const phase=boxPhase(box.state);
  if(phase==='running'){startStats();await openPreferredView(box,version);return}
  if(phase==='stopped'){showSleepingWorkspace();return}
  if(phase==='failed'){showSleepingWorkspace(box.failureReason?('Box failed: '+box.failureReason):'Box failed to start.');return}
  $('#status').textContent=statusLine(box);
  if(Date.now()>deadline)throw Error('Still '+box.state+'. This page stopped watching; reload to check again.');
  await new Promise(r=>setTimeout(r,2000));if(version!==epoch)return;
  try{box=await api(bp)}catch(err){if(phase==='deleting'){location.assign('/');return}throw err}
  if(version!==epoch)return;applyBoxState(box);
 }
}
async function connect(resume=false){
 if(busy)return;resetBoxSettings();busy=true;const version=++epoch;$('#connect').disabled=true;$('#error').textContent='';
 try{
  let box=await api(bp);if(version!==epoch)return;applyBoxState(box);
  const phase=boxPhase(box.state);
  if(phase==='running'){startStats();await openPreferredView(box,version);return}
  if(phase==='stopped'||phase==='failed'){
   if(!resume){showSleepingWorkspace();return}
   if(phase==='failed')allocation=null;
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
   box=await api(bp);if(version!==epoch)return;applyBoxState(box);
   startStats();await openPreferredView(box,version);return;
  }
  await observeBox(box,version);
 }catch(e){if(version===epoch)$('#error').textContent=e.message}finally{busy=false;$('#connect').disabled=false}
}
$('#connect').onclick=()=>void connect(true);
$('#login').onsubmit=async e=>{e.preventDefault();$('#login-error').textContent='';try{await api('/v1/browser-session','POST',{}, {Authorization:'Bearer '+e.target.elements.token.value});e.target.reset();workspaceRole=(await api('/v1/whoami')).role;workspaceUsage?.setOwner(workspaceRole==='owner');$('#login').hidden=true;$('#error').textContent='';await connect(false)}catch(e){showLogin(e.message)}};
$('#logout').onclick=async()=>{epoch++;stopStats();stopResourceUsage();closeTerminal();closeDesktop();try{await api('/v1/browser-session','DELETE');workspaceUsage?.setOwner(false);$('#workspace').hidden=true;$('#status').textContent='Logged out. The box was not stopped.';showLogin()}catch(e){$('#error').textContent=e.message}};
$('#hibernate').onclick=async()=>{
 if(busy||!confirm('Hibernate this box? Running processes will stop; workspace files are retained.'))return;
 const version=++epoch;stopStats();closeTerminal();closeDesktop();allocation=null;allocationKey=crypto.randomUUID();selectedWorkspaceView='';$('#terminal-screen').replaceChildren();busy=true;
 try{const box=await api(bp+'/hibernate','POST',{});if(version!==epoch)return;applyBoxState(box);await observeBox(box,version)}
 catch(e){if(version===epoch)$('#error').textContent=e.message}
 finally{busy=false}
};
// Owner-only, and only offered for boxes that can actually be deleted.
$('#delete-box').innerHTML=TRASH_ICON;
$('#delete-box').onclick=async()=>{
 if(deletePending||busy||!boxSummary)return;
 const box=boxSummary;
 if(!confirm('Delete box "'+box.name+'" and its workspace volume? Running processes will stop and all files in the volume will be permanently deleted. This cannot be undone.'))return;
 const version=epoch;deletePending=true;$('#delete-box').disabled=true;$('#error').textContent='';
 try{await api(bp+'/volume','DELETE',{confirmation:box.name});if(version!==epoch)return;deletePending=false;await connect(false)}
 catch(e){if(version===epoch){deletePending=false;$('#delete-box').disabled=false;$('#error').textContent=e.message}}
};
window.addEventListener('pagehide',()=>{epoch++;stopStats();closeTerminal();closeDesktop()});
(async()=>{try{workspaceRole=(await api('/v1/whoami')).role;workspaceUsage?.setOwner(workspaceRole==='owner');$('#login').hidden=true;await connect(false)}catch(e){showLogin(e.message==='Please log in to the controller.'?'':e.message)}})();

{
 const logs=$('#worker-logs'),button=$('#load-worker-logs'),status=$('#worker-log-status'),output=$('#worker-log-output');
 const load=async()=>{
  button.disabled=true;status.textContent='Loading recent logs…';
  try{
   const response=await fetch(bp+'/logs?tail=200',{credentials:'same-origin',cache:'no-store',signal:AbortSignal.timeout(60000)});
   const text=await response.text();
   if(!response.ok){let message;try{message=JSON.parse(text).error}catch{}throw Error(message||'Logs request failed: '+response.status)}
   output.textContent=text||'No worker log lines returned.';status.textContent=response.headers.get('X-Vmbox-Logs-Partial')==='true'?'Available lines loaded; the provider read reached its 15-second limit.':'Latest 200 lines loaded.';
  }catch(e){status.textContent=e.message}
  finally{button.disabled=false}
 };
 button.onclick=()=>void load();
 logs.addEventListener('toggle',()=>{if(logs.open&&!output.textContent)void load()});
}

// Secret values are sent directly to the private manager, never through chat.
const secretForm=document.querySelector('#secret-form');
if(secretForm){
 const secretStatus=document.querySelector('#secret-status');
 async function loadSecrets(){
  try{
   const values=await api(bp+'/secrets');
   const list=document.querySelector('#secret-list');list.replaceChildren();
   for(const value of values){
    const row=document.createElement('li');row.textContent=value.key+' · '+value.origin+' · '+value.status+' ';
    const fill=document.createElement('button');fill.type='button';fill.textContent='Fill focused password';
    fill.onclick=async()=>{fill.disabled=true;try{await api(bp+'/secrets/'+encodeURIComponent(value.key)+'/type','POST');secretStatus.textContent='Password filled. The form has not been submitted.'}catch(e){secretStatus.textContent=e.message}finally{fill.disabled=false}};
    row.append(fill);
    if(value.status==='pending'){
     const accepted=document.createElement('button');accepted.type='button';accepted.textContent='Mark accepted by site';
     accepted.onclick=async()=>{accepted.disabled=true;try{await api(bp+'/secrets/'+encodeURIComponent(value.key)+'/confirm','POST');await loadSecrets();secretStatus.textContent='Secret marked confirmed.'}catch(e){secretStatus.textContent=e.message;accepted.disabled=false}};
     row.append(accepted);
    }
    const remove=document.createElement('button');remove.type='button';remove.textContent='Remove';
    remove.onclick=async()=>{if(!confirm('Remove this saved secret? Existing website sessions remain active.'))return;try{await api(bp+'/secrets/'+encodeURIComponent(value.key),'DELETE');await loadSecrets()}catch(e){secretStatus.textContent=e.message}};
    row.append(remove);list.append(row);
   }
  }catch(e){secretStatus.textContent=e.message}
 }
 document.querySelector('#load-secrets').onclick=()=>void loadSecrets();
 secretForm.elements.generate.onchange=()=>{secretForm.elements.value.disabled=secretForm.elements.generate.checked;if(secretForm.elements.generate.checked)secretForm.elements.value.value=''};
 secretForm.onsubmit=async event=>{
  event.preventDefault();const submit=secretForm.querySelector('button');submit.disabled=true;
  const request={key:secretForm.elements.key.value,origin:secretForm.elements.origin.value,generate:secretForm.elements.generate.checked,value:secretForm.elements.value.value};
  secretForm.elements.value.value='';
  try{const result=await api(bp+'/secrets','POST',request);secretStatus.textContent=result.created?'Secret saved.':'Existing secret retained.';await loadSecrets()}
  catch(e){secretStatus.textContent=e.message}finally{request.value='';submit.disabled=false}
 };
}

// Preview pixels always come from worker capture, never the VNC canvas.
const preview=document.querySelector('#desktop-preview');
if(preview){
 let timer,url='',pending=false,rerun=false,captured='';
 const image=document.querySelector('#desktop-thumbnail'),label=document.querySelector('#thumbnail-status');
 async function refreshPreview(){
  clearTimeout(timer);
  if(!preview.open || document.hidden || document.querySelector('#workspace').hidden)return;
  if(pending){rerun=true;return}
  pending=true;
  try{
   const response=await fetch(bp+'/desktop/screenshot?thumbnail=true',{credentials:'same-origin',cache:'no-store',signal:AbortSignal.timeout(20000)});
   if(!response.ok)throw Error('Preview unavailable or box asleep.');
   const blob=await response.blob();
   if(!preview.open || document.querySelector('#workspace').hidden)return;
   if(url)URL.revokeObjectURL(url);
   url=URL.createObjectURL(blob);image.src=url;image.hidden=false;
   captured=response.headers.get('X-Captured-At')||new Date().toISOString();
   label.textContent='Captured '+new Date(captured).toLocaleTimeString();
  }catch{label.textContent=captured?'Last capture '+new Date(captured).toLocaleTimeString()+' · stale / offline':'Preview unavailable. Sleeping boxes stay asleep.'}
  finally{pending=false;if(preview.open){timer=setTimeout(refreshPreview,rerun?0:10000);rerun=false}}
 }
 refreshDesktopPreview=()=>{if(preview.open)void refreshPreview()};
 preview.addEventListener('toggle',()=>{clearTimeout(timer);if(preview.open)void refreshPreview()});
 document.addEventListener('visibilitychange',()=>{clearTimeout(timer);if(!document.hidden)void refreshPreview()});
 window.addEventListener('pagehide',()=>{clearTimeout(timer);if(url)URL.revokeObjectURL(url)});
 document.querySelector('#logout').addEventListener('click',()=>{preview.open=false;clearTimeout(timer);if(url)URL.revokeObjectURL(url);url='';image.removeAttribute('src');image.hidden=true;captured=''});
}

const importForm=document.querySelector('#browser-import-form');
if(importForm){
 const message=document.querySelector('#browser-import-status');
 const load=async()=>{
  try{
   const values=await api(bp+'/browser/imports');const list=document.querySelector('#browser-import-list');list.replaceChildren();
   for(const value of values){
    const row=document.createElement('li');row.textContent=value.origins.join(', ')+' · '+value.status+' ';
    for(const [label,method,suffix]of [['Apply','POST','/apply'],['Remove saved import','DELETE','']]){
     const button=document.createElement('button');button.type='button';button.textContent=label;
     button.onclick=async()=>{button.disabled=true;try{await api(bp+'/browser/imports/'+encodeURIComponent(value.id)+suffix,method,undefined,{},75000);await load();message.textContent=method==='DELETE'?'Saved import removed.':'Browser state applied.'}catch(e){message.textContent=e.message;button.disabled=false}};row.append(button);
    }
    list.append(row);
   }
  }catch(e){message.textContent=e.message}
 };
 document.querySelector('#load-browser-imports').onclick=()=>void load();
 importForm.onsubmit=async event=>{
  event.preventDefault();const file=importForm.elements.state.files[0];if(!file)return;
  if(file.size>1048576){message.textContent='State file must be at most 1 MiB.';return}
  const button=importForm.querySelector('button');button.disabled=true;
  try{const data=JSON.parse(await file.text());await api(bp+'/browser/imports','POST',data);importForm.reset();await load();message.textContent='Private browser import saved.'}
  catch(e){message.textContent=e instanceof SyntaxError?'Invalid JSON state file.':e.message}
  finally{button.disabled=false}
 };
}

const privateRequests=document.querySelector('#private-secret-requests');
if(privateRequests){
 let timer,signature='';
 const refresh=async()=>{
  clearTimeout(timer);
  if(document.querySelector('#workspace').hidden||document.hidden){timer=setTimeout(refresh,3000);return}
  try{
   const requests=(await api(bp+'/secret-requests')).filter(r=>r.status==='pending');
   const next=JSON.stringify(requests.map(r=>[r.key,r.origin]));
   if(document.querySelector('#workspace').hidden)return;
   if(next!==signature){
    signature=next;privateRequests.replaceChildren();
    for(const request of requests){
     const form=document.createElement('form'),label=document.createElement('label');label.textContent='Private password requested: '+request.key+' for '+request.origin+' ';
     const input=document.createElement('input');input.type='password';input.autocomplete='current-password';input.required=true;input.maxLength=4096;label.append(input);form.append(label);
     const submit=document.createElement('button');submit.textContent='Provide privately';const cancel=document.createElement('button');cancel.type='button';cancel.textContent='Cancel request';form.append(submit,cancel);
     const status=document.createElement('p');status.setAttribute('role','status');form.append(status);
     const send=async cancelled=>{submit.disabled=true;cancel.disabled=true;const payload=cancelled?{cancel:true}:{value:input.value};input.value='';try{await api(bp+'/secret-requests/'+encodeURIComponent(request.key),'POST',payload);signature='';await refresh()}catch(e){status.textContent=e.message}finally{payload.value='';submit.disabled=false;cancel.disabled=false}};
     form.onsubmit=e=>{e.preventDefault();void send(false)};cancel.onclick=()=>void send(true);privateRequests.append(form);
    }
    const summary=document.querySelector('#agent-secrets > summary');
    if(summary)summary.textContent=requests.length?'Agent secrets · '+requests.length+' pending request'+(requests.length===1?'':'s'):'Agent secrets';
   }
  }catch{}
  timer=setTimeout(refresh,3000);
 };
 window.addEventListener('pagehide',()=>clearTimeout(timer));
 document.querySelector('#logout').addEventListener('click',()=>{clearTimeout(timer);signature='';privateRequests.replaceChildren()});
 void refresh();
}

const importedCredentials=document.querySelector('#imported-credentials');
if(importedCredentials){
 const list=document.querySelector('#imported-credential-list'),status=document.querySelector('#imported-credential-status');
 importedCredentials.addEventListener('toggle',async()=>{
  if(!importedCredentials.open)return;
  const version=epoch;list.replaceChildren();status.textContent='Loading…';
  try{
   const result=await api(bp+'/imported-credentials');
   if(version!==epoch||!importedCredentials.open)return;
   for(const ref of result.profiles){const row=document.createElement('li');row.textContent=ref.application+' · '+ref.name;list.append(row)}
   status.textContent=result.profiles.length?(result.verified?'Imported during provisioning. Current login validity is not checked.':'Selected at creation; import completion was not recorded for this box.'):'No imported login profiles recorded. Manually added logins are not listed.';
  }catch(e){if(version===epoch)status.textContent=e.message}
 });
 document.querySelector('#logout').addEventListener('click',()=>{importedCredentials.open=false;list.replaceChildren();status.textContent=''});
}

const contactsPanel=document.querySelector('#box-contacts');
if(contactsPanel){
 const status=$('#contact-status'),list=$('#contact-list'),tagLabel=$('#box-tags'),protectionLabel=$('#contact-protection-label'),toggleProtection=$('#contact-toggle-protection');
 let protectedBox=false;
 async function loadContacts(){
  if(workspaceRole!=='owner'){contactsPanel.hidden=true;return}
  contactsPanel.hidden=false;status.textContent='Loading…';
  try{
   const [contacts,protection,tagResult]=await Promise.all([api(bp+'/contacts'),api(bp+'/protection'),api(bp+'/tags')]);
   protectedBox=!!protection.protected;
   tagLabel.textContent=(tagResult.tags||[]).join(', ')||'None';
   protectionLabel.textContent=protectedBox?'Protected — agents cannot see or message this box':'Not protected';
   toggleProtection.textContent=protectedBox?'Remove protection':'Protect box';
   list.replaceChildren();
   if(!contacts.length){const empty=document.createElement('li');empty.textContent='No other eligible boxes.';list.append(empty)}
   for(const contact of contacts){
    const row=document.createElement('li'),label=document.createElement('span'),access=document.createElement('button'),added=contact.override==='allow';
    label.textContent=contact.contactName+' · '+(contact.contactState||'unknown')+' · '+contact.reason;access.type='button';access.textContent=added?'Remove contact':'Add contact';access.setAttribute('aria-label',(added?'Remove ':'Add ')+contact.contactName+(added?' from':' to')+' contacts');
    access.onclick=async()=>{access.disabled=true;try{await api(bp+'/contacts','PUT',{contact:contact.contactBoxId,state:added?'inherit':'allow'});await loadContacts()}catch(e){status.textContent=e.message;access.disabled=false}};
    row.append(label,document.createTextNode(' '),access);list.append(row);
   }
   status.textContent='Direct contacts are available through get_contacts. All contacts is managed in this box’s permissions; protected boxes remain hidden.';
  }catch(e){status.textContent=e.message}
 }
 contactsPanel.addEventListener('toggle',()=>{if(contactsPanel.open)void loadContacts()});
 toggleProtection.onclick=async()=>{toggleProtection.disabled=true;try{await api(bp+'/protection','PUT',{protected:!protectedBox});status.textContent='Protection saved.';await loadContacts()}catch(e){status.textContent=e.message}finally{toggleProtection.disabled=false}};
 $('#edit-box-tags').onclick=async()=>{const current=tagLabel.textContent==='None'?'':tagLabel.textContent,value=prompt('Labels for this box (comma separated)',current);if(value===null)return;try{await api(bp+'/tags','PUT',{tags:value.split(',').map(tag=>tag.trim()).filter(Boolean)});await loadContacts()}catch(e){status.textContent=e.message}};
 document.querySelector('#logout').addEventListener('click',()=>{contactsPanel.open=false;list.replaceChildren();status.textContent=''});
}
