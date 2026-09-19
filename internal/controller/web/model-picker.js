'use strict';
(()=>{
 // Claude Code exposes these documented aliases to CLI logins. An Anthropic
 // API model list cannot establish entitlement for a Claude CLI OAuth login.
 // https://code.claude.com/docs/en/model-config
 // https://platform.claude.com/docs/en/models/overview
 // Codex IDs are documented CLI choices, not an account-specific model/list.
 // https://learn.chatgpt.com/docs/models
 const suggested={
  claude:['sonnet','opus','haiku','best','fable','sonnet[1m]','opus[1m]','claude-sonnet-5','claude-opus-5','claude-haiku-4-5-20251001','claude-fable-5-1'],
  codex:['gpt-6-astra','gpt-5.6-sol','gpt-5.6-terra','gpt-5.6-luna'],
 };
 const names={
  sonnet:'Claude Sonnet (latest available)',opus:'Claude Opus (latest available)',haiku:'Claude Haiku (latest available)',
  best:'Best available (Fable or Opus)',fable:'Claude Fable 5.1 (if enabled)',
  'sonnet[1m]':'Claude Sonnet · 1M context','opus[1m]':'Claude Opus · 1M context',
  'claude-sonnet-5':'Claude Sonnet 5 · pinned','claude-opus-5':'Claude Opus 5 · pinned',
  'claude-haiku-4-5-20251001':'Claude Haiku 4.5 · pinned','claude-fable-5-1':'Claude Fable 5.1 · pinned (if enabled)',
  'gpt-6-astra':'GPT-6 Astra','gpt-5.6-sol':'GPT-5.6 Sol',
  'gpt-5.6-terra':'GPT-5.6 Terra','gpt-5.6-luna':'GPT-5.6 Luna',
 };
 const make=(tag,className,text)=>{const element=document.createElement(tag);element.className=className;if(text!==undefined)element.textContent=text;return element};
 const normalize=values=>[...new Map((values||[]).map(value=>{
  const id=String(typeof value==='string'?value:value?.id||'').trim();
  return [id,{id,label:String(typeof value==='string'?names[id]||id:value?.label||names[id]||id)}];
 }).filter(([id])=>id)).values()];
 function optionsFor(application,profileModels=[]){return [...(suggested[application]||[]),...profileModels]}
 function create(input){
  input.type='hidden';input.required=false;
  const wrapper=make('span','model-picker');input.parentNode.insertBefore(wrapper,input);wrapper.append(input);
  const openButton=make('button','model-picker-open','Choose model');openButton.type='button';openButton.setAttribute('aria-haspopup','dialog');wrapper.append(openButton);
  const dialog=make('dialog','model-picker-dialog');dialog.setAttribute('aria-label','Choose model');
  const header=make('div','model-picker-header');
  const title=make('h2','model-picker-title','Choose model');
  const closeButton=make('button','model-picker-close','×');closeButton.type='button';closeButton.setAttribute('aria-label','Close model picker');
  header.append(title,closeButton);
  const source=make('p','model-picker-source');
  const search=make('input','model-picker-search');search.type='search';search.placeholder='Search models';search.setAttribute('aria-label','Search models');
  const list=make('div','model-picker-list');list.setAttribute('role','listbox');
  const empty=make('p','model-picker-empty','No models match. Enter an exact model ID below.');
  const custom=make('div','model-picker-custom');
  const exact=make('input','model-picker-exact');exact.type='text';exact.maxLength=200;exact.placeholder='Exact model ID';exact.setAttribute('aria-label','Exact model ID');
  const useExact=make('button','model-picker-use-exact','Use exact ID');useExact.type='button';custom.append(exact,useExact);
  dialog.append(header,source,search,list,empty,custom);document.body.append(dialog);
  let application='',models=[],fallback=[],loader=null,requestVersion=0;
  const fallbackSource=()=>application==='claude'?'Documented Claude Code choices · account access checked at launch':application==='codex'?'Documented Codex CLI choices · account access checked at launch':'Saved profile models';
  const current=()=>input.value.trim();
  function display(){openButton.textContent=current()||'Choose model';openButton.disabled=input.disabled}
  function choose(id){input.value=id;display();dialog.close();input.dispatchEvent(new Event('change',{bubbles:true}));openButton.focus()}
  function render(){
   const query=search.value.trim().toLowerCase();list.replaceChildren();let shown=0;
   for(const model of models){
    if(query&&!(`${model.label} ${model.id}`.toLowerCase().includes(query)))continue;
    const button=make('button','model-picker-option');button.type='button';button.dataset.model=model.id;button.setAttribute('role','option');button.setAttribute('aria-selected',String(model.id===current()));
    button.append(make('span','model-picker-option-label',model.label));
    if(model.label!==model.id)button.append(make('small','model-picker-option-id',model.id));
    button.addEventListener('click',()=>choose(model.id));list.append(button);shown++;
   }
   empty.hidden=shown>0;
  }
  async function open(){
   if(input.disabled)return;
   title.textContent='Choose '+({claude:'Claude',codex:'Codex',opencode:'OpenCode'}[application]||'agent')+' model';
   search.value='';exact.value='';models=normalize([...fallback,current()]);source.textContent=fallbackSource();render();dialog.showModal();search.focus();
   if(!loader)return;
   const version=++requestVersion;source.textContent='Loading provider models…';
   try{
    const result=await loader();
    if(version!==requestVersion||!dialog.open)return;
    models=normalize([current(),...(result.models||[])]);source.textContent=result.source||'Provider models';render();
   }catch(error){if(version===requestVersion&&dialog.open){source.textContent='Could not load provider models: '+error.message+'. Showing saved choices.';models=normalize([...fallback,current()]);render()}}
  }
  openButton.addEventListener('click',open);
  closeButton.addEventListener('click',()=>dialog.close());
  search.addEventListener('input',render);
  exact.addEventListener('keydown',event=>{if(event.key==='Enter'){event.preventDefault();useExact.click()}});
  useExact.addEventListener('click',()=>{const id=exact.value.trim();if(id)choose(id);else exact.focus()});
  dialog.addEventListener('keydown',event=>{if(event.key==='Escape')event.stopPropagation()});
  dialog.addEventListener('close',()=>{requestVersion++;openButton.focus()});
  dialog.addEventListener('click',event=>{if(event.target===dialog)dialog.close()});
  display();
  return {
   setApplication(value){application=value||''},
   setOptions(values){fallback=values||[];if(!dialog.open)models=normalize([...fallback,current()])},
   setLoader(value){loader=value||null;requestVersion++},
   setValue(value){input.value=value||'';display()},
   destroy(){requestVersion++;if(dialog.open)dialog.close();dialog.remove()},
   open,
  };
 }
 window.VMBoxModelPicker={create,optionsFor};
})();
