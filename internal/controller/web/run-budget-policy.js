'use strict';
// A box's allocated-time limit is separate from its desktop idle policy.
window.VMBoxRunBudgetPolicy = (() => {
 const text=(tag,value)=>{const el=document.createElement(tag);el.textContent=value;return el};
 function mount(root,{boxId,request,adjust,state='',assignmentGeneration=''}){
  if(!root)return;
  const key=boxId+'|'+state+'|'+assignmentGeneration;
  if(root.dataset.budgetKey===key)return;
  root.dataset.budgetKey=key;root.replaceChildren();
  const card=text('section','');card.className='idle-policy run-budget-policy';
  const top=text('div','');top.className='idle-policy-top';
  const name=text('strong','Run-time limit');
  const badge=text('span','Loading…');badge.className='idle-policy-badge';top.append(name,badge);
  const elapsed=text('p','Current run: Loading…');elapsed.className='run-budget-elapsed';
  const controls=text('div','');controls.className='idle-policy-controls';
  const label=text('label','Limit after');
  const hours=document.createElement('input');hours.type='number';hours.min='0';hours.max='720';hours.step='0.25';hours.disabled=true;
  hours.setAttribute('aria-label','Allocated run hours before hibernation; zero turns the limit off');
  label.append(hours,document.createTextNode(' hours'));
  const save=text('button','Save limit');save.type='button';save.disabled=true;
  controls.append(label,save);
  const actions=text('div','');actions.className='idle-policy-controls run-budget-actions';actions.setAttribute('role','group');actions.setAttribute('aria-label','Current run-time countdown');
  const buttons=[['Reset countdown','reset',0],['+4h','add',4*3600],['+8h','add',8*3600],['+24h','add',24*3600]].map(([caption,action,seconds])=>{
   const button=text('button',caption);button.type='button';button.disabled=true;
   button.title=action==='reset'?'Restart the countdown from the saved limit':'Add time to this run only';
   button.addEventListener('click',()=>void changeCountdown(action,seconds));actions.append(button);return button;
  });
  const note=text('p','0 turns the limit off. Save or Reset restarts the countdown; added time applies only to this run. Current run elapsed time is unchanged.');note.className='idle-policy-note';
  const status=text('p','Loading run-time limit…');status.className='idle-policy-status';status.setAttribute('role','status');
  const retry=text('button','Retry');retry.type='button';retry.className='idle-policy-retry';retry.hidden=true;
  card.append(top,elapsed,controls,actions,note,status,retry);root.append(card);
  const current=()=>root.isConnected&&root.dataset.budgetKey===key;
  let elapsedTimer=0,policy=null,pending=false;
  function setPending(value){
   pending=value;hours.disabled=value||!policy;save.disabled=value||!policy;
   const enabled=!value&&policy?.state==='running'&&Number(policy.seconds)>0&&!!policy.deadlineAt;
   for(const button of buttons)button.disabled=!enabled;
  }
  function showElapsed(policy){
   clearTimeout(elapsedTimer);
   if(!current())return;
   if(policy.state!=='running'){elapsed.textContent='Current run: Not running';return}
   const since=Date.parse(policy.runningSince||'');
   if(!Number.isFinite(since)){elapsed.textContent='Current run: Unavailable';return}
   const total=Math.max(0,Math.floor((Date.now()-since)/1000));
   const days=Math.floor(total/86400),hours=Math.floor(total%86400/3600),minutes=Math.floor(total%3600/60),seconds=total%60;
   elapsed.textContent='Current run: '+[days?days+'d':'',hours?hours+'h':'',minutes?minutes+'m':'',seconds+'s'].filter(Boolean).join(' ');
   elapsedTimer=setTimeout(()=>showElapsed(policy),1000);
  }
  function render(next){
   policy=next;
   const seconds=Number(policy.seconds)||0;
   showElapsed(policy);
   hours.value=String(seconds/3600);setPending(false);
   badge.textContent=seconds>0?'On':'Off';badge.dataset.enabled=String(seconds>0);
   if(seconds===0){status.textContent='Run-time limit is off for this box.';return}
   if(policy.state==='running'){
    const left=Math.max(0,Math.ceil(Number(policy.remainingSeconds||0)/360)/10);
    status.textContent='Current run: about '+left+' hours left before hibernation.';
   }else status.textContent='Limit saved. The countdown starts when this box runs.';
  }
  async function load(){
   retry.hidden=true;setPending(true);status.textContent='Loading run-time limit…';
   try{const policy=await request();if(current())render(policy)}
   catch(error){if(current()){badge.textContent='Unavailable';status.textContent=error.message;retry.hidden=false}}
  }
  save.addEventListener('click',async()=>{
   if(pending)return;
   if(!hours.reportValidity())return;
   const value=Number(hours.value);
   if(!Number.isFinite(value)||value<0||value>720)return;
   const seconds=Math.round(value*3600);
   setPending(true);status.textContent='Saving…';
   try{const policy=await request(seconds);if(current())render(policy)}
   catch(error){if(current()){setPending(false);status.textContent=error.message}}
  });
  async function changeCountdown(action,seconds){
   if(pending||!policy?.deadlineAt)return;
   const deadline=policy.deadlineAt;
   setPending(true);status.textContent=action==='reset'?'Resetting countdown…':'Adding time…';
   try{
    const updated=await adjust(action,seconds,deadline);
    if(!current())return;
    render(updated);
    status.textContent=(action==='reset'?'Countdown reset. ':'Added '+seconds/3600+' hours. ')+status.textContent;
   }catch(error){if(current()){policy=null;setPending(false);status.textContent=error.message;retry.hidden=false}}
  }
  retry.addEventListener('click',()=>void load());
  void load();
 }
 return {mount};
})();
