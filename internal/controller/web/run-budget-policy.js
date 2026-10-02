'use strict';
// A box's allocated-time limit is separate from its desktop idle policy.
window.VMBoxRunBudgetPolicy = (() => {
 const text=(tag,value)=>{const el=document.createElement(tag);el.textContent=value;return el};
 function mount(root,{boxId,request,adjust,state='',assignmentGeneration='',onPolicy,compact=false}={}){
  if(!root)return;
  const key=boxId+'|'+state+'|'+assignmentGeneration;
  if(root.dataset.budgetKey===key)return;
  root.dataset.budgetKey=key;root.replaceChildren();
  const card=text('section','');card.className='idle-policy run-budget-policy'+(compact?' ip-policy-compact':'');
  const top=text('div','');top.className='idle-policy-top';
  const name=text('strong','Run-time limit');
  const badge=text('span','Loading…');badge.className='idle-policy-badge';top.append(name,badge);
  let toggle=null;
  if(compact){
   const label=text('label','');label.className='idle-policy-switch';
   toggle=document.createElement('input');toggle.type='checkbox';toggle.disabled=true;toggle.setAttribute('aria-label','Run-time limit for this box');
   const track=text('span','');track.setAttribute('aria-hidden','true');label.append(toggle,track);top.append(label);
  }
  const elapsed=text('p','Current run: Loading…');elapsed.className='run-budget-elapsed';
  const remaining=text('p','');remaining.className='run-budget-remaining';remaining.hidden=true;
  const controls=text('div','');controls.className='idle-policy-controls';
  const label=text('label',compact?'Stop after':'Stop in');
  const hours=document.createElement('input');hours.type='number';hours.min='0';hours.max='720';hours.step='0.25';hours.disabled=true;
  hours.setAttribute('aria-label','Hours until this box stops; zero turns the countdown off');
  if(compact){const field=text('span','');field.className='ip-unit-field';field.append(hours,text('span','hours'));label.append(field)}
  else label.append(hours,document.createTextNode(' hours'));
  const save=text('button',compact?'Save':'Start countdown');save.type='button';save.disabled=true;
  controls.append(label,save);
  const actions=text('div','');actions.className='idle-policy-controls run-budget-actions';actions.setAttribute('role','group');actions.setAttribute('aria-label','Current run-time countdown');
  const buttons=[['Reset countdown','reset',0],['+4h','add',4*3600],['+8h','add',8*3600],['+24h','add',24*3600]].map(([caption,action,seconds])=>{
   const button=text('button',caption);button.type='button';button.disabled=true;
   button.title=action==='reset'?'Restart the countdown from the saved limit':'Add time to this run only';
   button.addEventListener('click',()=>void changeCountdown(action,seconds));actions.append(button);return button;
  });
  const note=text('p','0 turns the countdown off. Start countdown saves the hours and starts the timer; +4h / +8h / +24h and Reset change this run only. Elapsed time is unchanged.');note.className='idle-policy-note';
  const status=text('p','Loading run-time limit…');status.className='idle-policy-status';status.setAttribute('role','status');
  const retry=text('button','Retry');retry.type='button';retry.className='idle-policy-retry';retry.hidden=true;
  card.append(top,elapsed,remaining,controls,actions,note,status,retry);root.append(card);
  const current=()=>root.isConnected&&root.dataset.budgetKey===key;
  let elapsedTimer=0,remainingTimer=0,policy=null,pending=false,rememberedSeconds=8*3600;
  function setPending(value){
   pending=value;hours.disabled=value||!policy||(compact&&Number(policy.seconds)===0);save.disabled=value||!policy||(compact&&Number(policy.seconds)===0);
   if(toggle)toggle.disabled=value||!policy;
   const enabled=!value&&policy?.state==='running'&&Number(policy.seconds)>0&&!!policy.deadlineAt;
   for(const button of buttons)button.disabled=!enabled;
  }
  function showElapsed(policy){
   clearTimeout(elapsedTimer);
   if(!current())return;
   if(compact){
    const since=Date.parse(policy.runningSince||''),deadline=Date.parse(policy.deadlineAt||'');
    const running=policy.state==='running'&&Number.isFinite(since)?'Running '+formatDuration(Math.max(0,Math.floor((Date.now()-since)/1000))):'Not running';
    const left=Number(policy.seconds)>0&&Number.isFinite(deadline)?formatDuration(Math.max(0,Math.floor((deadline-Date.now())/1000))):'';
    elapsed.textContent=running+(left?' · stops in '+left:'');
    remaining.textContent=left?'Stops in '+left:'';
    if(policy.state==='running')elapsedTimer=setTimeout(()=>showElapsed(policy),1000);
    return;
   }
   if(policy.state!=='running'){elapsed.textContent='Current run: Not running';return}
   const since=Date.parse(policy.runningSince||'');
   if(!Number.isFinite(since)){elapsed.textContent='Current run: Unavailable';return}
   const total=Math.max(0,Math.floor((Date.now()-since)/1000));
   const days=Math.floor(total/86400),hours=Math.floor(total%86400/3600),minutes=Math.floor(total%3600/60),seconds=total%60;
   elapsed.textContent='Current run: '+[days?days+'d':'',hours?hours+'h':'',minutes?minutes+'m':'',seconds+'s'].filter(Boolean).join(' ');
   elapsedTimer=setTimeout(()=>showElapsed(policy),1000);
  }
  function formatDuration(total){
   const days=Math.floor(total/86400),hours=Math.floor(total%86400/3600),minutes=Math.floor(total%3600/60),seconds=total%60;
   return [days?days+'d':'',hours?hours+'h':'',minutes?minutes+'m':'',(!days&&!hours)?seconds+'s':''].filter(Boolean).join(' ');
  }
  function showRemaining(next){
   clearTimeout(remainingTimer);
   if(!current())return;
   if(compact){remaining.hidden=true;return}
   const seconds=Number(next.seconds)||0;
   if(seconds===0){remaining.hidden=true;remaining.textContent='';return}
   remaining.hidden=false;
   if(next.state!=='running'){remaining.textContent='Countdown starts when this box runs.';return}
   const deadline=Date.parse(next.deadlineAt||'');
   const left=Number.isFinite(deadline)?Math.max(0,Math.floor((deadline-Date.now())/1000)):Math.max(0,Math.floor(Number(next.remainingSeconds)||0));
   remaining.textContent=left>0?'Stops in '+formatDuration(left):'Stopping now…';
   remainingTimer=setTimeout(()=>showRemaining(next),1000);
  }
  function render(next){
   policy=next;
   try{onPolicy&&onPolicy(policy)}catch{}
   const seconds=Number(policy.seconds)||0;
   showElapsed(policy);
   showRemaining(policy);
   if(seconds>0)rememberedSeconds=seconds;
   hours.value=String((seconds||rememberedSeconds)/3600);setPending(false);
   badge.textContent=seconds>0?'On':'Off';badge.dataset.enabled=String(seconds>0);
   if(toggle)toggle.checked=seconds>0;
   if(seconds===0){status.textContent=compact?'':'Run-time limit is off — this box will not stop on a countdown.';return}
   if(policy.state==='running'){
    status.textContent=compact?'':'Countdown active. Start countdown restarts it; +/- actions change this run only.';
   }else status.textContent=compact?'':'Countdown saved. It starts when this box runs.';
  }
  async function load(){
   retry.hidden=true;setPending(true);status.textContent='Loading run-time limit…';
   try{const policy=await request();if(current())render(policy)}
   catch(error){if(current()){clearTimeout(remainingTimer);remaining.hidden=true;badge.textContent='Unavailable';status.textContent=error.message;retry.hidden=false}}
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
  if(toggle)toggle.addEventListener('change',async()=>{
   if(pending||!policy)return;
   const seconds=toggle.checked?rememberedSeconds:0;
   setPending(true);status.textContent='Saving…';
   try{const updated=await request(seconds);if(current())render(updated)}
   catch(error){if(current()){toggle.checked=Number(policy.seconds)>0;setPending(false);status.textContent=error.message}}
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
   }catch(error){if(current()){policy=null;clearTimeout(remainingTimer);remaining.hidden=true;setPending(false);status.textContent=error.message;retry.hidden=false}}
  }
  retry.addEventListener('click',()=>void load());
  void load();
 }
 return {mount};
})();
