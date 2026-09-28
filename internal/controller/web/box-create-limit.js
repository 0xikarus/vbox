'use strict';
// An owner-facing shortcut for the per-box create_agent_box quota.
window.VMBoxCreateLimit = (() => {
 const text=(tag,value)=>{const el=document.createElement(tag);el.textContent=value;return el};
 function mount(root,{boxId,request,onSaved}){
  if(!root||root.dataset.createLimitBox===boxId)return;
  root.dataset.createLimitBox=boxId;root.hidden=true;root.replaceChildren();
  const card=text('section','');card.className='idle-policy box-create-limit';
  const top=text('div','');top.className='idle-policy-top';
  const name=text('strong','Created-box limit');
  const badge=text('span','Loading…');badge.className='idle-policy-badge';top.append(name,badge);
  const controls=text('div','');controls.className='idle-policy-controls';
  const label=text('label','Allow up to');
  const count=document.createElement('input');count.type='number';count.min='1';count.max='100';count.step='1';count.disabled=true;
  count.setAttribute('aria-label','Maximum boxes this box can create');
  label.append(count,document.createTextNode(' boxes total'));
  const save=text('button','Save limit');save.type='button';save.disabled=true;controls.append(label,save);
  const note=text('p','Completed creations still count toward this total.');note.className='idle-policy-note';
  const status=text('p','Loading creation limit…');status.className='idle-policy-status';status.setAttribute('role','status');
  const retry=text('button','Retry');retry.type='button';retry.className='idle-policy-retry';retry.hidden=true;
  card.append(top,controls,note,status,retry);root.append(card);
  const current=()=>root.isConnected&&root.dataset.createLimitBox===boxId;
  function render(policy){
   const cap=policy.capabilities||{},grant=cap.createAgentBox||{};
   const allowed=!!grant.enabled&&!!cap.mcpTools?.enabled&&(cap.mcpTools.allowedTools||[]).includes('create_agent_box');
   root.hidden=!allowed;
   count.value=String(grant.maxBoxes||1);count.disabled=!allowed;save.disabled=!allowed;
   badge.textContent=allowed?String(grant.maxBoxes)+' total':'Off';badge.dataset.enabled=String(allowed);
   status.textContent=allowed?'This box may create up to '+grant.maxBoxes+' boxes total.':'Box creation is off. Enable it in Permissions to edit this limit.';
  }
  async function load(){
   retry.hidden=true;status.textContent='Loading creation limit…';
   try{const policy=await request();if(current())render(policy)}
   catch(error){if(current()){root.hidden=false;badge.textContent='Unavailable';status.textContent=error.message;retry.hidden=false}}
  }
  save.addEventListener('click',async()=>{
   if(!count.reportValidity())return;
   const value=Number(count.value);
   if(!Number.isInteger(value)||value<1||value>100)return;
   count.disabled=true;save.disabled=true;status.textContent='Saving…';
   try{
    // Read the latest full policy before PUT, so this shortcut retains its
    // other permissions even if they changed since the panel was opened.
    const policy=await request();
    if(!current())return;
    const cap=policy.capabilities||{},grant=cap.createAgentBox||{};
    if(!grant.enabled||!cap.mcpTools?.enabled||!(cap.mcpTools.allowedTools||[]).includes('create_agent_box')){render(policy);return}
    const saved=await request({capabilities:{...cap,createAgentBox:{...grant,maxBoxes:value}}});
    if(current()){render(saved);onSaved?.(saved)}
   }catch(error){if(current()){count.disabled=false;save.disabled=false;status.textContent=error.message}}
  });
  retry.addEventListener('click',()=>void load());
  void load();
 }
 return {mount};
})();
