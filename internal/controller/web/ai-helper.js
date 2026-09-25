'use strict';
// Shared AI rewrite control for chat drafts and Markdown instruction editors.
window.VMBoxAIHelper = (() => {
 const defaults = {
  chat: 'Fix spelling, grammar and punctuation while keeping my meaning and tone.',
  markdown: 'Improve clarity, spelling and structure while preserving the Markdown and technical details.'
 };
 const wand = '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m4 20 12-12"/><path d="m14 5 5 5"/><path d="m17 2 .5 2.5L20 5l-2.5.5L17 8l-.5-2.5L14 5l2.5-.5z"/><path d="m7 2 .35 1.65L9 4l-1.65.35L7 6l-.35-1.65L5 4l1.65-.35z"/></svg>';
 const currentPrompt = kind => {try{return localStorage.getItem('vmbox.aiPrompt.'+kind)||defaults[kind]}catch{return defaults[kind]}};
 const currentModel = kind => {try{return localStorage.getItem('vmbox.aiModel.'+kind)||''}catch{return ''}};
 function modelControl({value='',optional=false}={}) {
  const label=document.createElement('label');label.className='ai-model-label';label.textContent='Model';
  const row=document.createElement('span');row.className='ai-model-row';
  const input=document.createElement('input');input.type='text';input.name='model';input.readOnly=true;input.placeholder=optional?'Use helper default':'openrouter/auto';input.value=value;input.required=!optional;
  const browse=document.createElement('button');browse.type='button';browse.textContent='Choose';browse.setAttribute('aria-haspopup','dialog');browse.title='Choose an OpenRouter model';
  const note=document.createElement('small');note.className='ai-model-note';note.textContent=optional?'Use the helper model, or choose a model for this prompt.':'Choose from OpenRouter models or enter an exact ID.';
  row.append(input,browse);label.append(row,note);
  const picker=document.createElement('dialog');picker.className='ai-model-dialog';picker.setAttribute('aria-label','Choose OpenRouter model');
  const header=document.createElement('header');
  const title=document.createElement('h2');title.textContent='Choose model';
  const close=document.createElement('button');close.type='button';close.className='ai-model-close';close.textContent='×';close.setAttribute('aria-label','Close model picker');close.onclick=()=>picker.close();header.append(title,close);
  const search=document.createElement('input');search.type='search';search.className='ai-model-search';search.placeholder='Search models';search.setAttribute('aria-label','Search models');
  const summary=document.createElement('p');summary.className='ai-model-summary';summary.setAttribute('role','status');
  const results=document.createElement('div');results.className='ai-model-results';results.setAttribute('role','listbox');results.setAttribute('aria-label','OpenRouter models');
  const custom=document.createElement('div');custom.className='ai-model-custom';
  const exact=document.createElement('input');exact.type='text';exact.maxLength=200;exact.required=true;exact.pattern='[A-Za-z0-9][A-Za-z0-9._:/~-]{0,199}';exact.spellcheck=false;exact.autocomplete='off';exact.placeholder='Exact model ID';exact.setAttribute('aria-label','Exact OpenRouter model ID');
  const useExact=document.createElement('button');useExact.type='button';useExact.textContent='Use ID';custom.append(exact,useExact);
  picker.append(header,search,summary,results,custom);document.body.append(picker);
  let version=0,models=[],loading=false,loadError='';
  const select=id=>{input.value=id;input.dispatchEvent(new Event('change',{bubbles:true}));picker.close()};
  function render() {
   results.replaceChildren();
   const query=search.value.trim().toLowerCase();
   const defaultLabel=optional?'Use helper default':'Automatic · openrouter/auto';
   const showDefault=!query||defaultLabel.toLowerCase().includes(query);
   if(showDefault){const automatic=document.createElement('button');automatic.type='button';automatic.className='ai-model-option';automatic.textContent=defaultLabel;automatic.dataset.model=optional?'':'openrouter/auto';automatic.setAttribute('role','option');automatic.setAttribute('aria-selected',String(input.value===automatic.dataset.model));automatic.onclick=()=>select(automatic.dataset.model);results.append(automatic)}
   const matches=models.filter(item=>item.id!=='openrouter/auto'&&`${item.label||''} ${item.id}`.toLowerCase().includes(query));
   for(const item of matches.slice(0,80)){
    const option=document.createElement('button');option.type='button';option.className='ai-model-option';option.dataset.model=item.id;option.setAttribute('role','option');option.setAttribute('aria-selected',String(input.value===item.id));
    const name=document.createElement('strong');name.textContent=item.label||item.id;
    const id=document.createElement('small');id.textContent=item.id;
    option.append(name,id);option.onclick=()=>select(item.id);results.append(option);
   }
   summary.textContent=loading?'Loading OpenRouter models…':loadError?loadError+' · exact IDs still work':models.length?`${matches.length+(showDefault?1:0)} matching choices${matches.length>80?' · showing first 80':''}`:'No catalog loaded · exact IDs still work';
  }
  browse.onclick=()=>{search.value='';exact.value='';render();picker.showModal();search.focus()};
  input.onclick=()=>browse.click();
  search.oninput=render;
  exact.onkeydown=event=>{if(event.key==='Enter'){event.preventDefault();useExact.click()}};
  useExact.onclick=()=>{exact.value=exact.value.trim();if(exact.reportValidity())select(exact.value);else exact.focus()};
  picker.onclick=event=>{if(event.target===picker)picker.close()};
  async function load(profile='') {
   const current=++version;
   loading=true;loadError='';models=[];render();
   note.textContent='Loading OpenRouter models…';
   try {
    const path='/v1/ai/models'+(profile?'?profile='+encodeURIComponent(profile):'');
    const response=await fetch(path,{credentials:'same-origin',cache:'no-store'});
    const data=await response.json();if(!response.ok)throw Error(data.error||'Model list unavailable');
    if(current!==version)return;
    models=Array.isArray(data.models)?data.models:[];loading=false;render();
    note.textContent=models.length+' models available · choose to browse'+(optional?' · default uses the helper model':'');
   }catch(error){if(current===version){loading=false;loadError=error.message;render();note.textContent=error.message+' · enter an exact model ID instead.'}}
  }
  return {label,input,load,destroy(){version++;if(picker.open)picker.close();picker.remove()}};
 }
 function editPrompt(kind, run) {
  const dialog=document.createElement('dialog');dialog.className='ai-prompt-dialog';
  const form=document.createElement('form');form.method='dialog';
  const title=document.createElement('h2');title.textContent='AI writing prompt';
  const hint=document.createElement('p');hint.textContent='Edit how the wand rewrites this '+(kind==='chat'?'message':'Markdown')+'. The draft is only replaced after the result returns.';
  const label=document.createElement('label');label.textContent='Prompt';
  const textarea=document.createElement('textarea');textarea.rows=5;textarea.maxLength=2000;textarea.required=true;textarea.value=currentPrompt(kind);label.append(textarea);
  const model=modelControl({value:currentModel(kind),optional:true});
  const actions=document.createElement('div');actions.className='ai-prompt-actions';
  const reset=document.createElement('button');reset.type='button';reset.textContent='Reset';reset.onclick=()=>{textarea.value=defaults[kind];model.input.value='';textarea.focus()};
  const cancel=document.createElement('button');cancel.type='button';cancel.textContent='Cancel';cancel.onclick=()=>dialog.close();
  const save=document.createElement('button');save.type='submit';save.className='primary';save.textContent='Save & improve';
  actions.append(reset,cancel,save);form.append(title,hint,label,model.label,actions);dialog.append(form);document.body.append(dialog);
  const cleanup=()=>{model.destroy();dialog.remove()};dialog.addEventListener('close',cleanup,{once:true});
  form.addEventListener('submit',event=>{
   event.preventDefault();if(!textarea.reportValidity())return;
   const instruction=textarea.value.trim();
   try{localStorage.setItem('vmbox.aiPrompt.'+kind,instruction)}catch{}
   const selectedModel=model.input.value.trim();
   try{localStorage.setItem('vmbox.aiModel.'+kind,selectedModel)}catch{}
   dialog.close();run(instruction,selectedModel);
  });
  dialog.showModal();void model.load();textarea.focus();textarea.select();
 }
 function attach({input,kind='chat',status,getContext=()=>''}) {
  if(!input || !defaults[kind])throw Error('AI helper requires a supported editor');
  const field=document.createElement('span');field.className='ai-field';
  input.parentNode.insertBefore(field,input);field.append(input);
  const button=document.createElement('button');button.type='button';button.className='ai-wand';button.innerHTML=wand;
  button.title='Improve text · hold or right-click to edit prompt';button.setAttribute('aria-label','Improve text with AI. Hold or press Shift+Enter to edit prompt');
  field.append(button);
  let timer=0,longPress=false,busy=false;
  const report=message=>{if(status)status.textContent=message};
  async function run(instruction=currentPrompt(kind),model=currentModel(kind)) {
   if(busy)return;
   if(input.readOnly || input.disabled){report('Switch to an editable draft first.');return}
   const original=input.value,context=getContext();
   if(!original.trim()){report('Write something first, then use the wand.');input.focus();return}
   busy=true;button.disabled=true;button.classList.add('is-busy');report('Improving draft…');
   try{
    const response=await fetch('/v1/ai/rewrite',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify({text:original,instruction,kind,model}),signal:AbortSignal.timeout(50000)});
    let result;try{result=await response.json()}catch{}
    if(!response.ok)throw Error(result?.error||'AI helper request failed.');
    if(input.value!==original || getContext()!==context){report('Draft changed while the wand was working; the new text was not applied.');return}
    if(!result?.text)throw Error('AI helper returned no text.');
    input.value=result.text;input.dispatchEvent(new Event('input',{bubbles:true}));input.focus();
    report(result.text===original?'No changes needed.':'Draft improved. Review it before sending or saving.');
   }catch(error){report(error.name==='TimeoutError'?'AI helper timed out. Try again.':error.message)}
   finally{busy=false;button.disabled=false;button.classList.remove('is-busy')}
  }
  button.addEventListener('pointerdown',event=>{
   if(event.button!==0)return;longPress=false;
   timer=setTimeout(()=>{longPress=true;editPrompt(kind,run)},550);
  });
  for(const name of ['pointerup','pointercancel','pointerleave'])button.addEventListener(name,()=>{clearTimeout(timer)});
  button.addEventListener('click',event=>{event.preventDefault();if(longPress){longPress=false;return}void run()});
  button.addEventListener('contextmenu',event=>{event.preventDefault();clearTimeout(timer);if(longPress)return;longPress=true;editPrompt(kind,run)});
  button.addEventListener('keydown',event=>{if(event.shiftKey && (event.key==='Enter'||event.key===' ')){event.preventDefault();editPrompt(kind,run)}});
  return {button,run};
 }
 async function openSettings() {
  const dialog=document.createElement('dialog');dialog.className='ai-settings-dialog';
  dialog.innerHTML='<form><header><span class="ai-settings-mark" aria-hidden="true">✦</span><div><h2>AI writing helper</h2><p>OpenRouter key and model for the writing wand</p></div><button class="ai-settings-close" type="button" aria-label="Close settings">×</button></header><p class="ai-settings-copy">Your saved key is shown here for the owner and stored encrypted on the controller.</p><p class="ai-settings-state" role="status">Loading setting…</p><label class="ai-import-label">Import from OpenCode profile<select name="profile"><option value="">Select a saved OpenCode profile…</option></select></label><label>OpenRouter API key<span class="ai-key-row"><input name="key" type="password" autocomplete="new-password" spellcheck="false" placeholder="Paste your OpenRouter key" required><button type="button" class="ai-key-visibility" aria-label="Show API key" aria-pressed="false">Show</button></span></label><p class="ai-settings-hint">You can select a model after importing a profile. The key is copied only when you save.</p><div class="ai-model-slot"></div><div class="ai-settings-actions"><button class="ai-settings-remove" type="button" hidden>Remove key</button><span></span><button class="ai-settings-cancel" type="button">Cancel</button><button class="ai-settings-save" type="submit">Save key and model</button></div></form>';
  document.body.append(dialog);
  const form=dialog.querySelector('form'),key=form.elements.key,profile=form.elements.profile,state=dialog.querySelector('.ai-settings-state'),remove=dialog.querySelector('.ai-settings-remove'),save=dialog.querySelector('.ai-settings-save');
  const model=modelControl({value:'openrouter/auto'});dialog.querySelector('.ai-model-slot').append(model.label);
  const visibility=dialog.querySelector('.ai-key-visibility');
  visibility.onclick=()=>{const shown=key.type==='password';key.type=shown?'text':'password';visibility.textContent=shown?'Hide':'Show';visibility.setAttribute('aria-label',shown?'Hide API key':'Show API key');visibility.setAttribute('aria-pressed',String(shown))};
  const close=()=>dialog.close();dialog.querySelector('.ai-settings-close').onclick=close;dialog.querySelector('.ai-settings-cancel').onclick=close;
  dialog.addEventListener('close',()=>{key.value='';model.destroy();dialog.remove()},{once:true});
  dialog.showModal();
  const request=async(method,body)=>{const response=await fetch('/v1/ai/openrouter',{method,credentials:'same-origin',cache:'no-store',headers:{'Content-Type':'application/json'},body:body&&JSON.stringify(body)});if(response.status===204)return null;const data=await response.json().catch(()=>({}));if(!response.ok)throw Error(data.error||'Could not update AI writing helper');return data};
  const paint=data=>{key.value=data.key||'';model.input.value=data.model||'openrouter/auto';remove.hidden=!data.configured;save.textContent=data.configured?'Save changes':'Save key and model';state.textContent=data.configured?'Key saved · select Show to reveal it.':'No dedicated key saved · import an OpenCode profile or paste a key.';state.classList.remove('is-error')};
  try{
   paint(await request('GET'));key.focus();void model.load();
   const response=await fetch('/v1/login-profiles',{credentials:'same-origin',cache:'no-store'});
   if(response.ok){const profiles=await response.json();for(const item of Array.isArray(profiles)?profiles:[]){if(item.application!=='opencode')continue;const option=document.createElement('option');option.value=item.name;option.textContent=item.name+(item.model?' · '+item.model:'');profile.append(option)}}
  }catch(error){state.textContent=error.message;state.classList.add('is-error')}
  profile.addEventListener('change',async()=>{
   if(!profile.value)return;
   const selected=profile.value;state.textContent='Loading OpenCode profile…';state.classList.remove('is-error');
   try{
    const response=await fetch('/v1/ai/openrouter/profiles/'+encodeURIComponent(selected),{credentials:'same-origin',cache:'no-store'});
    const data=await response.json();if(!response.ok)throw Error(data.error||'Could not import profile');
    if(profile.value!==selected)return;
    key.value=data.key;model.input.value=data.model;
    state.textContent='Profile loaded · choose a model, then save the key and model.';
    void model.load(selected);
   }catch(error){if(profile.value!==selected)return;state.textContent=error.message;state.classList.add('is-error')}
  });
  form.addEventListener('submit',async event=>{event.preventDefault();if(!form.reportValidity())return;save.disabled=true;state.textContent='Saving key and model…';state.classList.remove('is-error');try{await request('PUT',{key:key.value,model:model.input.value});paint(await request('GET'));profile.value='';void model.load()}catch(error){state.textContent=error.message;state.classList.add('is-error')}finally{save.disabled=false}});
  remove.addEventListener('click',async()=>{if(!confirm('Remove the dedicated OpenRouter key? The writing wand will use a saved OpenCode OpenRouter profile if one is available.'))return;remove.disabled=true;try{await request('DELETE');paint({configured:false,model:'openrouter/auto'});profile.value=''}catch(error){state.textContent=error.message;state.classList.add('is-error')}finally{remove.disabled=false}});
 }
 return {attach,openSettings};
})();
