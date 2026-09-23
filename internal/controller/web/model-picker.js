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
  return [id,{id,label:String(typeof value==='string'?names[id]||id:value?.label||names[id]||id),reasoning:typeof value==='object'?value?.reasoning:undefined,
   reasoningEfforts:typeof value==='object'&&Array.isArray(value?.reasoningEfforts)?value.reasoningEfforts.map(String):[],
   inputCost:typeof value==='object'?Number(value?.inputCost)||0:0,outputCost:typeof value==='object'?Number(value?.outputCost)||0:0,
   context:typeof value==='object'?Number(value?.context)||0:0}];
 }).filter(([id])=>id)).values()];
 const ctxLabel=n=>n>=1e6?(n/1e6).toFixed(n%1e6?1:0)+'M':n>=1e3?Math.round(n/1e3)+'k':n?'':'';
 const money=n=>'$'+(n>=0.1?n.toFixed(2):n<0.01?n.toFixed(4):n.toFixed(3));
 const priceCell=(value,kind)=>{
  if(!value)return make('span','model-picker-price-empty','—');
  return make('b','model-picker-price-value',money(value));
 };
 let lastModels=[];
 function optionsFor(application,profileModels=[]){return [...(suggested[application]||[]),...profileModels]}
 function create(input){
  input.type='hidden';input.required=false;
  const wrapper=make('span','model-picker');input.parentNode.insertBefore(wrapper,input);wrapper.append(input);
  const effortInput=make('input','model-picker-effort-value');effortInput.type='hidden';effortInput.name='agentReasoningEffort';wrapper.append(effortInput);
  const openButton=make('button','model-picker-open','Choose model');openButton.type='button';openButton.setAttribute('aria-haspopup','dialog');wrapper.append(openButton);
  const dialog=make('dialog','model-picker-dialog');
  dialog.setAttribute('aria-label','Choose model');
  const header=make('div','model-picker-header');
  const title=make('h2','model-picker-title','Choose model');
  const closeButton=make('button','model-picker-close','×');closeButton.type='button';closeButton.setAttribute('aria-label','Close model picker');
  header.append(title,closeButton);
  const source=make('p','model-picker-source');
  const body=make('div','model-picker-body');
  const rail=make('div','model-picker-rail');
  const searchLabel=make('label','model-picker-rail-label','Search');
  const search=make('input','model-picker-search');search.type='search';search.placeholder='Search models';search.setAttribute('aria-label','Search models');
  searchLabel.append(search);
  const sortLabel=make('label','model-picker-rail-label','Sort by');
  const sort=make('select','model-picker-sort');sort.setAttribute('aria-label','Sort models');
  [['id','Name'],['inputCost','Cheapest input'],['outputCost','Cheapest output'],['context','Most context']].forEach(([value,text])=>{const option=make('option','',text);option.value=value;sort.append(option)});
  sortLabel.append(sort);
  const capLabel=make('label','model-picker-rail-label','Capabilities');
  const caps=make('div','model-picker-caps');
  const capReasoning=make('button','model-picker-cap','reasoning');capReasoning.type='button';capReasoning.dataset.cap='reasoning';capReasoning.setAttribute('aria-pressed','false');
  const capContext=make('button','model-picker-cap','200k+ ctx');capContext.type='button';capContext.dataset.cap='context';capContext.setAttribute('aria-pressed','false');
  caps.append(capReasoning,capContext);
  const railNote=make('p','model-picker-rail-note','Prices are per 1M tokens from the provider catalog. Exact model IDs are always accepted.');
  rail.append(searchLabel,sortLabel,capLabel,caps,railNote);
  const results=make('div','model-picker-results');
  const count=make('p','model-picker-count');
  const list=make('div','model-picker-list');list.setAttribute('role','listbox');
  const empty=make('p','model-picker-empty','No models match. Enter an exact model ID below.');
  const custom=make('div','model-picker-custom');
  const exact=make('input','model-picker-exact');exact.type='text';exact.maxLength=200;exact.placeholder='Exact model ID';exact.setAttribute('aria-label','Exact model ID');
  const useExact=make('button','model-picker-use-exact','Use exact ID');useExact.type='button';custom.append(exact,useExact);
  results.append(count,list,empty,custom);
  body.append(rail,results);
  const effortLabel=make('label','model-picker-effort-label','Reasoning');
  const effort=make('select','model-picker-effort');effort.setAttribute('aria-label','Reasoning level');effortLabel.append(effort);
  const effortNote=make('p','model-picker-effort-note');
  const apply=make('button','model-picker-apply','Use model and reasoning');apply.type='button';
  dialog.append(header,source,body,effortLabel,effortNote,apply);document.body.append(dialog);
  let application='',models=[],fallback=[],loader=null,requestVersion=0,selectedModel='',activeCaps=new Set(),loadingDone=false;
 lastModels=models;
  const fallbackSource=()=>application==='claude'?'Documented Claude Code choices · login checked at creation; model access checked when used':application==='codex'?'Documented Codex CLI choices · account access checked at launch':'Saved profile models';
  const current=()=>dialog.open?selectedModel:input.value.trim();
  const findModel=id=>models.find(model=>model.id===id);
  function display(){
   const model=findModel(input.value.trim());
   const effortSuffix=effortInput.value?' · '+effortInput.value:'';
   const priceSuffix=model&&model.inputCost?' · '+money(model.inputCost)+' in · '+money(model.outputCost)+' out / Mtok':'';
   openButton.textContent=(input.value.trim()||'Choose model')+effortSuffix+priceSuffix;
   openButton.disabled=input.disabled;
  }
  function levels(){
   if(application==='claude'){
    if(/haiku/i.test(current()))return [];
    return ['low','medium','high',...(/^(sonnet|opus|best|fable)(\[1m\])?$|(?:sonnet|opus|fable)-(?:5|4-[78])/i.test(current())?['xhigh']:[])];
   }
   if(application==='codex'){
    const selected=findModel(current());
    if(selected?.reasoningEfforts?.length)return selected.reasoningEfforts;
    return ['low','medium','high','xhigh',...(/^(gpt-6-astra|gpt-5\.6-sol)$/.test(current())?['max']:[])];
   }
   if(application==='opencode'){
    const selected=findModel(current());
    if(selected?.reasoning===false)return [];
    return ['low','medium','high','max'];
   }
   return [];
  }
  function renderEffort(){
   const prior=effort.value||effortInput.value;effort.replaceChildren();
   const defaultChoice=make('option','','Default');defaultChoice.value='';effort.append(defaultChoice);
   for(const level of levels()){const option=make('option','',level==='xhigh'?'Extra high':level[0].toUpperCase()+level.slice(1));option.value=level;effort.append(option)}
   effort.value=[...effort.options].some(option=>option.value===prior)?prior:'';
   effort.disabled=effort.options.length===1;
   effortNote.textContent=application==='opencode'?(effort.disabled?'Provider reports no reasoning-effort control for this model.':'OpenCode uses model variants; available levels depend on the provider and model.'):effort.disabled?'This model has no configurable reasoning level.':'Default keeps the uploaded profile or model setting.';
  }
  function choose(id){selectedModel=id;renderEffort();render();displayPreview()}
  function previewRows(){
   const model=findModel(current());
   if(!model)return [];
   const rows=[['Model',model.label]];
   if(model.inputCost)rows.push(['Price',money(model.inputCost)+' in · '+money(model.outputCost)+' out per Mtok']);
   if(model.context)rows.push(['Context',ctxLabel(model.context)+' tokens']);
   if(effort.value)rows.push(['Reasoning',effort.options[effort.selectedIndex]?.textContent||effort.value]);
   return rows;
  }
  function displayPreview(){
   if(!preview||!dialog.open)return;
   preview.replaceChildren();
   for(const [k,v] of previewRows()){
    const row=make('div','model-picker-preview-row');
    row.append(make('span','model-picker-preview-key',k),make('span','model-picker-preview-value',v));
    preview.append(row);
   }
  }
  function render(){
   const query=search.value.trim().toLowerCase();
   let shown=Object.keys(byProvider).length?0:models.length;
   list.replaceChildren();
   const filtered=models.filter(model=>{
    if(query&&!(`${model.label} ${model.id}`.toLowerCase().includes(query)))return false;
    if(activeCaps.has('reasoning')&&!model.reasoning)return false;
    if(activeCaps.has('context')&&model.context<200000)return false;
    return true;
   });
   const sortValue=sort.value;
   if(sortValue!=='id'){
    filtered.sort((a,b)=>sortValue==='context'?b.context-a.context:(a[sortValue]||1e9)-(b[sortValue]||1e9));
   }
   shown=filtered.length;
   const groups=new Map();
   for(const model of filtered){
    const provider=model.id.includes('/')?model.id.split('/')[0]:'';
    if(!groups.has(provider))groups.set(provider,[]);
    groups.get(provider).push(model);
   }
   for(const [provider,items] of groups){
    if(provider){
     const head=make('p','model-picker-group',provider);
     list.append(head);
    }
    for(const model of items){
     const button=make('button','model-picker-option');button.type='button';button.dataset.model=model.id;button.setAttribute('role','option');button.setAttribute('aria-selected',String(model.id===current()));
     const main=make('span','model-picker-option-main');
     main.append(make('span','model-picker-option-label',model.label));
     if(model.label!==model.id)main.append(make('small','model-picker-option-id',model.id));
     button.append(main);
     const meta=make('span','model-picker-option-meta');
     if(model.context)meta.append(make('span','model-picker-option-ctx',ctxLabel(model.context)+' ctx'));
     if(model.inputCost){
      const price=make('span','model-picker-option-price');
      price.append(priceCell(model.inputCost,'in'),document.createTextNode(' in · '),priceCell(model.outputCost,'out'),document.createTextNode(' out'));
      meta.append(price);
     }
     if(model.reasoning)meta.append(make('span','model-picker-option-badge','R'));
     button.append(meta);
     button.addEventListener('click',()=>choose(model.id));
     list.append(button);
    }
   }
   empty.hidden=shown>0;
   count.textContent=loadingDone&&models.length?shown+' of '+models.length+' models':'';
  }
  let preview=make('div','model-picker-preview');
  dialog.insertBefore(preview,effortLabel);
  const byProvider={};
  async function open(){
   if(input.disabled)return;
   title.textContent='Choose '+({claude:'Claude',codex:'Codex',opencode:'OpenCode'}[application]||'agent')+' model';
   selectedModel=input.value.trim();search.value='';exact.value='';effort.value=effortInput.value;models=normalize([...fallback,current()]);source.textContent=fallbackSource();loadingDone=false;renderEffort();render();displayPreview();dialog.showModal();search.focus();
   if(!loader)return;
   const version=++requestVersion;source.textContent='Loading provider models…';
   try{
    const result=await loader();
    if(version!==requestVersion||!dialog.open)return;
    models=normalize([current(),...(result.models||[])]);lastModels=models;source.textContent=result.source||'Provider models';loadingDone=true;renderEffort();render();displayPreview();input.dispatchEvent(new Event('change',{bubbles:true}));
   }catch(error){if(version===requestVersion&&dialog.open){source.textContent='Could not load provider models: '+error.message+'. Showing saved choices.';models=normalize([...fallback,current()]);loadingDone=true;render()}}
  }
  openButton.addEventListener('click',open);
  closeButton.addEventListener('click',()=>dialog.close());
  search.addEventListener('input',render);
  sort.addEventListener('change',render);
  caps.addEventListener('click',event=>{
   const button=event.target.closest('.model-picker-cap');if(!button)return;
   const cap=button.dataset.cap;
   if(activeCaps.has(cap))activeCaps.delete(cap);else activeCaps.add(cap);
   button.setAttribute('aria-pressed',String(activeCaps.has(cap)));
   button.classList.toggle('on',activeCaps.has(cap));
   render();
  });
  exact.addEventListener('keydown',event=>{if(event.key==='Enter'){event.preventDefault();useExact.click()}});
  useExact.addEventListener('click',()=>{const id=exact.value.trim();if(id)choose(id);else exact.focus()});
  effort.addEventListener('change',()=>{renderEffort();displayPreview()});
  apply.addEventListener('click',()=>{input.value=selectedModel;effortInput.value=effort.value;display();input.dispatchEvent(new Event('change',{bubbles:true}));dialog.close();openButton.focus()});
  dialog.addEventListener('keydown',event=>{if(event.key==='Escape')event.stopPropagation()});
  dialog.addEventListener('close',()=>{requestVersion++;openButton.focus()});
  dialog.addEventListener('click',event=>{if(event.target===dialog)dialog.close()});
  display();
  return {
   setApplication(value){application=value||''},
   setOptions(values){fallback=values||[];if(!dialog.open){models=normalize([...fallback,current()]);lastModels=models}},
   setLoader(value){loader=value||null;requestVersion++},
   setValue(value){input.value=value||'';display()},
   setReasoningEffort(value){effortInput.value=value||'';effort.value=effortInput.value;display()},
   destroy(){requestVersion++;if(dialog.open)dialog.close();dialog.remove()},
   open,
  };
 }
 window.VMBoxModelPicker={create,optionsFor,catalog:()=>lastModels};
})();
