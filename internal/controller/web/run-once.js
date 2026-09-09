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
 const profiles=node('div');form.append(profiles);
 const prompt=field(form,'Prompt / shell command','textarea','prompt');prompt.required=true;prompt.rows=5;prompt.maxLength=100000;
 const submit=node('button','Run once');form.append(submit);
 const fresh=node('button','New run');fresh.type='button';fresh.onclick=()=>{if(busy)return;sessionStorage.removeItem('vmbox.run-once.intent');selected='';clearTimeout(timer);generation++;current.replaceChildren();error.textContent='';status.textContent='New run ready. Review the command and logins, then choose Run once.'};form.append(fresh);
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
  try{const [ps,logins,runs]=await Promise.all([api('/v1/provider-credentials'),api('/v1/login-profiles'),api('/v1/run-once')]);if(ticket!==generation)return;
   const old=provider.value;provider.replaceChildren();for(const p of ps)option(provider,p.provider+' / '+p.name,JSON.stringify([p.provider,p.name]));if([...provider.options].some(o=>o.value===old))provider.value=old;
   const previous=Object.fromEntries([...profiles.querySelectorAll('select')].map(s=>[s.name,s.value]));profiles.replaceChildren();
   for(const app of ['claude','codex','github']){const s=field(profiles,app+' login','select',app);option(s,'None','');for(const p of logins.filter(p=>p.application===app))option(s,p.name,p.name);if([...s.options].some(o=>o.value===previous[app]))s.value=previous[app]}
   status.textContent='Saved logins are uploaded using vmbox profiles upload. Agent runs require the corresponding login.';
   history.replaceChildren(node('h3','Recent runs'));const table=node('table');const head=node('tr');['Command / prompt','State','Exit code','Box'].forEach(h=>head.append(node('th',h)));table.append(head);
   for(const run of runs){const row=node('tr');row.append(node('td',run.request.prompt.slice(0,100)),node('td',run.task?.state||run.state),node('td',run.task?.exitCode??'—'));const cell=node('td');const b=node('button',run.boxId?'Open terminal / results':'Inspect');b.onclick=()=>{selected=run.id;inspect(run.id)};cell.append(b);row.append(cell);table.append(row)}history.append(table);
   if(selected)inspect(selected,ticket);
  }catch(e){error.textContent=e.message}
 }
 form.onsubmit=async e=>{e.preventDefault();if(busy)return;busy=true;submit.disabled=true;error.textContent='';
  try{const [p,name]=JSON.parse(provider.value),body={provider:p,providerCredential:name,agent:agent.value,prompt:prompt.value,loginProfiles:[...profiles.querySelectorAll('select')].filter(s=>s.value).map(s=>({application:s.name,name:s.value}))};
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
