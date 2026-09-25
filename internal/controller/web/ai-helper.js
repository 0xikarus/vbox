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
 return {attach};
})();
