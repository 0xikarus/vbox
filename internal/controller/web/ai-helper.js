'use strict';
// Shared AI rewrite control for chat drafts and Markdown instruction editors.
window.VMBoxAIHelper = (() => {
 const defaults = {
  chat: 'Fix spelling, grammar and punctuation while keeping my meaning and tone.',
  markdown: 'Improve clarity, spelling and structure while preserving the Markdown and technical details.'
 };
 const wand = '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m4 20 12-12"/><path d="m14 5 5 5"/><path d="m17 2 .5 2.5L20 5l-2.5.5L17 8l-.5-2.5L14 5l2.5-.5z"/><path d="m7 2 .35 1.65L9 4l-1.65.35L7 6l-.35-1.65L5 4l1.65-.35z"/></svg>';
 const currentPrompt = kind => {try{return localStorage.getItem('vmbox.aiPrompt.'+kind)||defaults[kind]}catch{return defaults[kind]}};
 function editPrompt(kind, run) {
  const dialog=document.createElement('dialog');dialog.className='ai-prompt-dialog';
  const form=document.createElement('form');form.method='dialog';
  const title=document.createElement('h2');title.textContent='AI writing prompt';
  const hint=document.createElement('p');hint.textContent='Edit how the wand rewrites this '+(kind==='chat'?'message':'Markdown')+'. The draft is only replaced after the result returns.';
  const label=document.createElement('label');label.textContent='Prompt';
  const textarea=document.createElement('textarea');textarea.rows=5;textarea.maxLength=2000;textarea.required=true;textarea.value=currentPrompt(kind);label.append(textarea);
  const actions=document.createElement('div');actions.className='ai-prompt-actions';
  const reset=document.createElement('button');reset.type='button';reset.textContent='Reset';reset.onclick=()=>{textarea.value=defaults[kind];textarea.focus()};
  const cancel=document.createElement('button');cancel.type='button';cancel.textContent='Cancel';cancel.onclick=()=>dialog.close();
  const save=document.createElement('button');save.type='submit';save.className='primary';save.textContent='Save & improve';
  actions.append(reset,cancel,save);form.append(title,hint,label,actions);dialog.append(form);document.body.append(dialog);
  const cleanup=()=>dialog.remove();dialog.addEventListener('close',cleanup,{once:true});
  form.addEventListener('submit',event=>{
   event.preventDefault();if(!textarea.reportValidity())return;
   const instruction=textarea.value.trim();
   try{localStorage.setItem('vmbox.aiPrompt.'+kind,instruction)}catch{}
   dialog.close();run(instruction);
  });
  dialog.showModal();textarea.focus();textarea.select();
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
  async function run(instruction=currentPrompt(kind)) {
   if(busy)return;
   if(input.readOnly || input.disabled){report('Switch to an editable draft first.');return}
   const original=input.value,context=getContext();
   if(!original.trim()){report('Write something first, then use the wand.');input.focus();return}
   busy=true;button.disabled=true;button.classList.add('is-busy');report('Improving draft…');
   try{
    const response=await fetch('/v1/ai/rewrite',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify({text:original,instruction,kind}),signal:AbortSignal.timeout(50000)});
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
  dialog.innerHTML='<form><header><span class="ai-settings-mark" aria-hidden="true">✦</span><div><h2>AI writing helper</h2><p>OpenRouter key for the writing wand</p></div><button class="ai-settings-close" type="button" aria-label="Close settings">×</button></header><p class="ai-settings-copy">The key is stored encrypted on the controller and used only when you improve a draft. It is never sent back to this browser.</p><p class="ai-settings-state" role="status">Loading setting…</p><label>OpenRouter API key<input name="key" type="password" autocomplete="new-password" spellcheck="false" placeholder="Paste your OpenRouter key" required></label><label>Model ID<input name="model" type="text" maxlength="200" spellcheck="false" value="openrouter/auto" required></label><p class="ai-settings-hint">Use <code>openrouter/auto</code> or an exact OpenRouter model ID. A saved OpenCode OpenRouter profile is used if no key is set here.</p><div class="ai-settings-actions"><button class="ai-settings-remove" type="button" hidden>Remove key</button><span></span><button class="ai-settings-cancel" type="button">Cancel</button><button class="ai-settings-save" type="submit">Save key</button></div></form>';
  document.body.append(dialog);
  const form=dialog.querySelector('form'),key=form.elements.key,model=form.elements.model,state=dialog.querySelector('.ai-settings-state'),remove=dialog.querySelector('.ai-settings-remove'),save=dialog.querySelector('.ai-settings-save');
  const close=()=>dialog.close();dialog.querySelector('.ai-settings-close').onclick=close;dialog.querySelector('.ai-settings-cancel').onclick=close;
  dialog.addEventListener('close',()=>{key.value='';dialog.remove()},{once:true});
  dialog.showModal();
  const request=async(method,body)=>{const response=await fetch('/v1/ai/openrouter',{method,credentials:'same-origin',headers:{'Content-Type':'application/json'},body:body&&JSON.stringify(body)});if(response.status===204)return null;const data=await response.json().catch(()=>({}));if(!response.ok)throw Error(data.error||'Could not update AI writing helper');return data};
  const paint=data=>{model.value=data.model||'openrouter/auto';remove.hidden=!data.configured;key.required=!data.configured;key.placeholder=data.configured?'Leave blank to keep saved key':'Paste your OpenRouter key';save.textContent=data.configured?'Save changes':'Save key';state.textContent=data.configured?'Key saved · used for new writing wand requests.':'No dedicated key saved · an OpenCode OpenRouter profile can be used instead.';state.classList.remove('is-error')};
  try{paint(await request('GET'));key.focus()}catch(error){state.textContent=error.message;state.classList.add('is-error')}
  form.addEventListener('submit',async event=>{event.preventDefault();if(!form.reportValidity())return;save.disabled=true;state.textContent='Saving key…';state.classList.remove('is-error');try{paint(await request('PUT',{key:key.value,model:model.value}));key.value=''}catch(error){state.textContent=error.message;state.classList.add('is-error')}finally{save.disabled=false}});
  remove.addEventListener('click',async()=>{if(!confirm('Remove the dedicated OpenRouter key? The writing wand will use a saved OpenCode OpenRouter profile if one is available.'))return;remove.disabled=true;try{await request('DELETE');paint({configured:false,model:'openrouter/auto'});key.value=''}catch(error){state.textContent=error.message;state.classList.add('is-error')}finally{remove.disabled=false}});
 }
 return {attach,openSettings};
})();
