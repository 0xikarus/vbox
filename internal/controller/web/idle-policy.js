'use strict';
// One per-box control used by chat details, management details and workspaces.
window.VMBoxIdlePolicy = (() => {
 const text=(tag,value)=>{const el=document.createElement(tag);el.textContent=value;return el};
 function mount(root,{boxId,boxName='',request}){
  if(!root || root.dataset.idleBox===boxId)return;
  root.dataset.idleBox=boxId;root.replaceChildren();
  const card=text('section','');card.className='idle-policy';
  const top=text('div','');top.className='idle-policy-top';
  const name=text('strong','Automatic hibernation');
  const badge=text('span','Loading…');badge.className='idle-policy-badge';
  const label=text('label','');label.className='idle-policy-switch';
  const toggle=document.createElement('input');toggle.type='checkbox';toggle.disabled=true;
  toggle.setAttribute('aria-label','Automatic hibernation for '+(boxName||'this box'));
  const track=text('span','');track.setAttribute('aria-hidden','true');
  label.append(toggle,track);top.append(name,badge,label);
  const controls=text('div','');controls.className='idle-policy-controls';
  const hoursLabel=text('label','Idle after');
  const hours=document.createElement('input');hours.type='number';hours.min='0.25';hours.max='168';hours.step='0.25';hours.value='4';hours.required=true;hours.disabled=true;
  hours.setAttribute('aria-label','Idle hours before hibernation');
  hoursLabel.append(hours,document.createTextNode(' hours'));
  const save=text('button','Save hours');save.type='button';save.disabled=true;
  controls.append(hoursLabel,save);
  const note=text('p','Managed tasks, pending private requests and human takeover pause the idle timer.');note.className='idle-policy-note';
  const status=text('p','Loading idle policy…');status.className='idle-policy-status';status.setAttribute('role','status');
  const retry=text('button','Retry');retry.type='button';retry.className='idle-policy-retry';retry.hidden=true;
  card.append(top,controls,note,status,retry);root.append(card);
  const storageKey='vmbox.idleHours.'+boxId;
  const remembered=()=>{try{const value=Number(localStorage.getItem(storageKey));return value>=.25&&value<=168?value:4}catch{return 4}};
  const remember=value=>{try{localStorage.setItem(storageKey,String(value))}catch{}};
  let seconds=0,pending=false,targetSeconds=0;
  const current=()=>root.isConnected&&root.dataset.idleBox===boxId;
  function applyPolicy(policy){
   seconds=policy.seconds;
   if(seconds===0&&policy.resumeSeconds>0)hours.value=String(policy.resumeSeconds/3600);
   render();
  }
  function render(){
   const visible=pending?targetSeconds:seconds;
   toggle.checked=visible>0;badge.textContent=visible>0?'On':'Off';badge.dataset.enabled=String(visible>0);
   if(visible>0){hours.value=String(visible/3600);remember(visible/3600)}
   else if(!hours.value || Number(hours.value)===0)hours.value=String(remember());
   toggle.disabled=pending;hours.disabled=pending||visible===0;save.disabled=pending||visible===0;
  }
  async function update(next){
   if(pending)return;
   targetSeconds=next;pending=true;render();status.textContent='Saving…';
   try{
    const policy=await request(next);
    if(!current())return;
    applyPolicy(policy);
    status.textContent=seconds>0?'Hibernates after '+hours.value+' idle hours.':'Automatic hibernation is off for this box.';
   }catch(error){
    if(!current())return;
    render();status.textContent=error.message;
   }finally{pending=false;if(current())render()}
  }
  toggle.addEventListener('change',()=>{
   if(!toggle.checked){if(seconds>0)remember(seconds/3600);void update(0);return}
   const value=Number(hours.value)||remember();void update(Math.round(value*3600));
  });
  save.addEventListener('click',()=>{
   if(!hours.reportValidity())return;
   void update(Math.round(Number(hours.value)*3600));
  });
  async function load(){
   retry.hidden=true;status.textContent='Loading idle policy…';
   try{
    const policy=await request();if(!current())return;
    applyPolicy(policy);status.textContent=seconds>0?'Hibernates after '+hours.value+' idle hours.':'Automatic hibernation is off for this box.';
   }catch(error){if(current()){badge.textContent='Unavailable';status.textContent=error.message;retry.hidden=false}}
  }
  retry.addEventListener('click',()=>void load());
  void load();
 }
 return {mount};
})();
