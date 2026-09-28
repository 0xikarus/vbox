'use strict';
// A box's allocated-time limit is separate from its desktop idle policy.
window.VMBoxRunBudgetPolicy = (() => {
 const text=(tag,value)=>{const el=document.createElement(tag);el.textContent=value;return el};
 function mount(root,{boxId,request}){
  if(!root || root.dataset.budgetBox===boxId)return;
  root.dataset.budgetBox=boxId;root.replaceChildren();
  const card=text('section','');card.className='idle-policy run-budget-policy';
  const top=text('div','');top.className='idle-policy-top';
  const name=text('strong','Run-time limit');
  const badge=text('span','Loading…');badge.className='idle-policy-badge';top.append(name,badge);
  const controls=text('div','');controls.className='idle-policy-controls';
  const label=text('label','Limit after');
  const hours=document.createElement('input');hours.type='number';hours.min='0';hours.max='720';hours.step='0.25';hours.disabled=true;
  hours.setAttribute('aria-label','Allocated run hours before hibernation; zero turns the limit off');
  label.append(hours,document.createTextNode(' hours'));
  const save=text('button','Save limit');save.type='button';save.disabled=true;
  controls.append(label,save);
  const note=text('p','Counts only while the box is running. 0 turns this limit off. Saving starts a new countdown for the current run.');note.className='idle-policy-note';
  const status=text('p','Loading run-time limit…');status.className='idle-policy-status';status.setAttribute('role','status');
  const retry=text('button','Retry');retry.type='button';retry.className='idle-policy-retry';retry.hidden=true;
  card.append(top,controls,note,status,retry);root.append(card);
  const current=()=>root.isConnected&&root.dataset.budgetBox===boxId;
  function render(policy){
   const seconds=Number(policy.seconds)||0;
   hours.value=String(seconds/3600);hours.disabled=false;save.disabled=false;
   badge.textContent=seconds>0?'On':'Off';badge.dataset.enabled=String(seconds>0);
   if(seconds===0){status.textContent='Run-time limit is off for this box.';return}
   if(policy.state==='running'){
    const left=Math.max(0,Math.ceil(Number(policy.remainingSeconds||0)/360)/10);
    status.textContent='Current run: about '+left+' hours left before hibernation.';
   }else status.textContent='Limit saved. The countdown starts when this box runs.';
  }
  async function load(){
   retry.hidden=true;status.textContent='Loading run-time limit…';
   try{const policy=await request();if(current())render(policy)}
   catch(error){if(current()){badge.textContent='Unavailable';status.textContent=error.message;retry.hidden=false}}
  }
  save.addEventListener('click',async()=>{
   if(!hours.reportValidity())return;
   const value=Number(hours.value);
   if(!Number.isFinite(value)||value<0||value>720)return;
   const seconds=Math.round(value*3600);
   hours.disabled=true;save.disabled=true;status.textContent='Saving…';
   try{const policy=await request(seconds);if(current())render(policy)}
   catch(error){if(current()){hours.disabled=false;save.disabled=false;status.textContent=error.message}}
  });
  retry.addEventListener('click',()=>void load());
  void load();
 }
 return {mount};
})();
