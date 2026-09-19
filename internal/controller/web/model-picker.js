'use strict';
(()=>{
 let sequence=0;
 // These are CLI model IDs/aliases, not account entitlements. Claude Code
 // recommends aliases that follow its available releases; Codex publishes the
 // exact CLI IDs. Keep the input editable for account-specific model names.
 // https://code.claude.com/docs/en/model-config
 // https://learn.chatgpt.com/docs/models
 const suggested={
  claude:['sonnet','opus','haiku','sonnet[1m]','opus[1m]'],
  codex:['gpt-6-astra','gpt-5.6-sol','gpt-5.6-terra','gpt-5.6-luna'],
 };
 function optionsFor(application,profileModels=[]){return [...(suggested[application]||[]),...profileModels]}
 function create(input){
  const wrapper=document.createElement('span');wrapper.className='model-picker';
  input.parentNode.insertBefore(wrapper,input);wrapper.append(input);
  const options=document.createElement('div');options.className='model-picker-options';options.id='model-picker-'+(++sequence);options.setAttribute('role','listbox');options.hidden=true;wrapper.append(options);
  const toggle=document.createElement('button');toggle.type='button';toggle.className='model-picker-toggle';toggle.textContent='▾';toggle.title='Show available models';toggle.setAttribute('aria-label','Show available models');toggle.setAttribute('aria-controls',options.id);toggle.setAttribute('aria-expanded','false');wrapper.append(toggle);
  input.autocomplete='off';input.removeAttribute('list');input.setAttribute('role','combobox');input.setAttribute('aria-autocomplete','list');input.setAttribute('aria-controls',options.id);input.setAttribute('aria-expanded','false');
  let models=[],active=-1,showingAll=false;
  const visible=()=>[...options.querySelectorAll('.model-picker-option:not([hidden])')];
  function choose(value){input.value=value;close();input.dispatchEvent(new Event('change',{bubbles:true}));input.focus()}
  function render(){
   const query=showingAll?'':input.value.trim().toLowerCase();options.replaceChildren();active=-1;
   for(const model of models){
    const button=document.createElement('button');button.type='button';button.className='model-picker-option';button.setAttribute('role','option');button.textContent=model;button.hidden=!!query&&!model.toLowerCase().includes(query);button.addEventListener('mousedown',event=>event.preventDefault());button.addEventListener('click',()=>choose(model));options.append(button);
   }
   const shown=visible();options.hidden=!shown.length;input.setAttribute('aria-expanded',String(shown.length>0));toggle.setAttribute('aria-expanded',String(shown.length>0));
  }
  function open(all=false){if(input.disabled)return;showingAll=all;render()}
  function close(){options.hidden=true;input.setAttribute('aria-expanded','false');toggle.setAttribute('aria-expanded','false');active=-1;showingAll=false}
  function move(step){
   const shown=visible();if(!shown.length)return;
   active=(active+step+shown.length)%shown.length;
   shown.forEach((option,index)=>option.classList.toggle('active',index===active));shown[active].scrollIntoView({block:'nearest'});
  }
  input.addEventListener('focus',()=>open());input.addEventListener('click',()=>open());input.addEventListener('input',()=>{showingAll=false;render()});
  toggle.addEventListener('click',()=>{if(!options.hidden&&showingAll)close();else open(true)});
  input.addEventListener('keydown',event=>{
   if(event.key==='ArrowDown'||event.key==='ArrowUp'){event.preventDefault();if(options.hidden)open();move(event.key==='ArrowDown'?1:-1)}
   else if(event.key==='Enter'&&!options.hidden&&active>=0){event.preventDefault();choose(visible()[active].textContent)}
   else if(event.key==='Escape'&&!options.hidden){event.stopPropagation();close()}
  });
  document.addEventListener('pointerdown',event=>{if(!wrapper.contains(event.target))close()});
  return {
   setOptions(values){models=[...new Set((values||[]).map(value=>String(value).trim()).filter(Boolean))].sort((a,b)=>a.localeCompare(b));if(!options.hidden)render()},
   setValue(value){input.value=value||'';toggle.disabled=input.disabled;close()},
   open,
  };
 }
 window.VMBoxModelPicker={create,optionsFor};
})();
