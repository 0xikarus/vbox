'use strict';
(() => {
 const root=document.querySelector('#run-once-content'),section=root.parentElement;
 let timer,busy=false,selected='',generation=0;
 const node=(tag,text)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;return n};
 const field=(form,label,tag,name)=>{const l=node('label',label+' '),n=node(tag);n.name=name;l.append(n);form.append(l);return n};
 const option=(s,label,value)=>{const o=node('option',label);o.value=value;s.append(o)};
 const status=node('p');status.setAttribute('role','status');const error=node('p');error.setAttribute('role','alert');
 const form=node('form'),provider=field(form,'Provider','select','provider'),agent=field(form,'Agent','select','agent');
 ['shell','claude','codex'].forEach(a=>option(agent,a,a));
 const options=node('div');form.append(options);
 const modelMode=field(options,'Model','select','model-mode');option(modelMode,'Use saved profile default','');option(modelMode,'Specify model','custom');
 const model=field(options,'Model ID','input','model');model.maxLength=256;model.placeholder='Exact model ID supported by your agent';model.parentElement.hidden=true;
 modelMode.onchange=()=>{model.parentElement.hidden=modelMode.value!=='custom';model.required=modelMode.value==='custom'&&agent.value!=='shell'};
 const advanced=node('details');advanced.append(node('summary','Extra CLI arguments (optional)'));options.append(advanced);
 const args=field(advanced,'One argument per line; no shell quoting','textarea','args');args.rows=3;args.placeholder='--option\nvalue';
 advanced.append(node('p','Arguments are passed directly to the selected agent, not evaluated by a shell. They can change agent permissions or behavior. Do not enter tokens or passwords. Leave blank to use your saved configuration.'));
 const profiles=node('div');form.append(profiles);
 const tools=node('fieldset');form.append(tools);
 const prompt=field(form,'Prompt / shell command','textarea','prompt');prompt.required=true;prompt.rows=5;prompt.maxLength=100000;
 const imageArea=node('div');form.append(imageArea);const images=[];let nextImage=1,uploading=false;
 const imageInput=field(imageArea,'Attach images','input','images');imageInput.type='file';imageInput.multiple=true;imageInput.accept='image/png,image/jpeg,image/gif';
 imageArea.append(node('p','Up to 8 images, 8 MiB each (PNG, JPEG, GIF). Refer to [Image 1] in your instructions. Download URLs are appended at the bottom of the submitted prompt and expire seven days after scheduling.'));
 const imageList=node('div');imageArea.append(imageList);
 imageInput.onchange=async()=>{
  if(uploading)return;uploading=true;imageInput.disabled=true;submit.disabled=true;error.textContent='';
  try{for(const file of imageInput.files){
   if(images.length>=8)throw Error('Attach at most 8 images.');if(file.size>8*1024*1024)throw Error('Each image must be at most 8 MiB.');
   const r=await fetch('/v1/run-once-images',{method:'POST',credentials:'same-origin',body:file,signal:AbortSignal.timeout(60000)});
   const result=await r.json();if(!r.ok)throw Error(result.error||'Image upload failed.');
   const entry={id:result.id,number:nextImage++};images.push(entry);
   const row=node('p','[Image '+entry.number+'] '+file.name+' '),preview=node('img');const previewURL=URL.createObjectURL(file);preview.src=previewURL;preview.alt='Image '+entry.number;preview.width=96;row.append(preview);
   const remove=node('button','Remove');remove.type='button';remove.onclick=()=>{images.splice(images.indexOf(entry),1);URL.revokeObjectURL(previewURL);row.remove()};row.append(remove);imageList.append(row);
  }}catch(e){error.textContent=e.message}finally{uploading=false;imageInput.disabled=false;submit.disabled=false;imageInput.value=''}
 };
 const guidance=node('p');guidance.id='run-once-prompt-help';prompt.setAttribute('aria-describedby',guidance.id);form.append(guidance);
 function guide(){
  const shell=agent.value==='shell';prompt.parentElement.firstChild.textContent=shell?'Shell command ':'Task instructions ';
  options.hidden=shell;model.required=!shell&&modelMode.value==='custom';
  imageArea.hidden=shell;
  prompt.placeholder=shell?'cd /data/workspace/my-project && npm test':'Research or build …\nRepository / working directory: …\nExpected result: …\nVerify completion by …';
  guidance.textContent=shell?'Executed once in the box. Use a working directory explicitly when needed. The actual command exit code is retained.':'Describe the goal, repository or working directory, expected output, and how the agent should verify completion. Select the matching agent login below; GitHub login is optional for repository access.';
  for(const s of profiles.querySelectorAll('select')){s.required=s.name===agent.value;s.parentElement.firstChild.textContent=s.name+' login'+(s.required?' (required)':' (optional)')+' ';}
 }
 agent.addEventListener('change',guide);guide();
 form.append(node('p','Run once opens the live terminal automatically. After completion, the box hibernates and releases compute; files, output, and exit code remain available. Reopening results does not rerun the task.'));
 const submit=node('button','Run once');form.append(submit);
 const fresh=node('button','Start another run');fresh.type='button';fresh.onclick=()=>{if(busy)return;sessionStorage.removeItem('vmbox.run-once.intent');selected='';clearTimeout(timer);generation++;current.replaceChildren();error.textContent='';status.textContent='New run ready. Review the command and logins, then choose Run once.'};form.append(fresh);
 form.append(node('p','Retrying an unchanged submission reuses the same run. Choose Start another run only when you want to execute it again.'));
 const refresh=node('button','Refresh'),history=node('div'),current=node('div');
 root.append(status,error,form,current,refresh,history);
 async function api(path,method='GET',body,headers={}){const r=await fetch(path,{method,credentials:'same-origin',headers:{'Content-Type':'application/json',...headers},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(30000)});let v;try{v=await r.json()}catch{}if(!r.ok)throw Error(v?.error||'Controller unavailable ('+r.status+'). Your draft is retained.');return v}
 function active(){return location.hash==='#run-once'&&!document.querySelector('#app').hidden&&!document.hidden}
 async function inspect(id,ticket=generation){
  clearTimeout(timer);
  try{const run=await api('/v1/run-once/'+encodeURIComponent(id));if(ticket!==generation||selected!==id||!active())return;
   current.replaceChildren(node('p',run.failure||(run.state==='queued'?'Waiting for a healthy free slot.':run.task?.state||run.state)));
   if(run.boxId){location.assign('/boxes/'+encodeURIComponent(run.boxId)+'?run='+encodeURIComponent(run.id));return}
   if(run.state==='queued'){const cancel=node('button','Cancel queued run');cancel.onclick=async()=>{cancel.disabled=true;try{await api('/v1/run-once/'+id+'/cancel','POST',{});await inspect(id,ticket)}catch(e){error.textContent=e.message;cancel.disabled=false}};current.append(cancel);timer=setTimeout(()=>inspect(id,ticket),2000)}
  }catch(e){error.textContent=e.message}
 }
 async function load(){
  section.hidden=location.hash!=='#run-once';if(!active())return;
  const ticket=++generation;clearTimeout(timer);error.textContent='';
  try{const [ps,logins,runs,presets]=await Promise.all([api('/v1/provider-credentials'),api('/v1/login-profiles'),api('/v1/run-once'),api('/v1/tool-presets')]);if(ticket!==generation)return;
   const selectedTools=new Set([...tools.querySelectorAll('input:checked')].map(i=>i.value));tools.replaceChildren(node('legend','Optional tools · installed before your command starts'));
   for(const preset of presets){const label=node('label'),input=node('input');input.type='checkbox';input.value=preset.id;input.checked=selectedTools.has(preset.id);label.append(input,document.createTextNode(' '+preset.name+' '+preset.version+' — '+preset.description));tools.append(label)}
   const old=provider.value;provider.replaceChildren();for(const p of ps)option(provider,p.provider+' / '+p.name,JSON.stringify([p.provider,p.name]));if([...provider.options].some(o=>o.value===old))provider.value=old;
   const previous=Object.fromEntries([...profiles.querySelectorAll('select')].map(s=>[s.name,s.value]));profiles.replaceChildren();
   for(const app of ['claude','codex','github']){const s=field(profiles,app+' login','select',app);option(s,'None','');for(const p of logins.filter(p=>p.application===app))option(s,p.name,p.name);if([...s.options].some(o=>o.value===previous[app]))s.value=previous[app]}
   guide();
   status.textContent='Saved logins are uploaded using vmbox profiles upload. Agent runs require the corresponding login.';
   history.replaceChildren(node('h3','Recent runs'));const table=node('table');const head=node('tr');['Command / prompt','State','Exit code','Box'].forEach(h=>head.append(node('th',h)));table.append(head);
   for(const run of runs){const row=node('tr');row.append(node('td',run.request.prompt.slice(0,100)),node('td',run.task?.state||run.state),node('td',run.task?.exitCode??'—'));const cell=node('td');const b=node('button',run.boxId?'Open terminal / results':'Inspect');b.onclick=()=>{selected=run.id;inspect(run.id)};cell.append(b);row.append(cell);table.append(row)}history.append(table);
   if(selected)inspect(selected,ticket);
  }catch(e){error.textContent=e.message}
 }
 form.onsubmit=async e=>{e.preventDefault();if(busy||uploading)return;busy=true;submit.disabled=true;error.textContent='';
  try{const [p,name]=JSON.parse(provider.value),body={provider:p,providerCredential:name,agent:agent.value,prompt:prompt.value,loginProfiles:[...profiles.querySelectorAll('select')].filter(s=>s.value).map(s=>({application:s.name,name:s.value}))};
   if(agent.value!=='shell'){if(modelMode.value==='custom')body.model=model.value.trim();const argv=args.value.split('\n').filter(a=>a!=='');if(argv.length)body.args=argv;}
   if(agent.value!=='shell'&&images.length)body.images=images.map(({id,number})=>({id,number}));
   const selectedTools=[...tools.querySelectorAll('input:checked')].map(i=>i.value);if(selectedTools.length)body.tools=selectedTools;
   const encoded=JSON.stringify(body);let intent;try{intent=JSON.parse(sessionStorage.getItem('vmbox.run-once.intent'))}catch{}
   if(intent?.body!==encoded)intent={body:encoded,key:crypto.randomUUID()};sessionStorage.setItem('vmbox.run-once.intent',JSON.stringify(intent));
   const run=await api('/v1/run-once','POST',body,{'Idempotency-Key':intent.key});selected=run.id;await inspect(selected);
  }catch(e){error.textContent=e.message}finally{busy=false;submit.disabled=false}
 };
 refresh.onclick=load;
 window.addEventListener('hashchange',load);document.addEventListener('visibilitychange',()=>{if(active())load();else clearTimeout(timer)});
 new MutationObserver(()=>{if(!document.querySelector('#app').hidden)load();else{generation++;clearTimeout(timer)}}).observe(document.querySelector('#app'),{attributes:true,attributeFilter:['hidden']});
 load();
})();
