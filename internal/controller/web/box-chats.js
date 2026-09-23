'use strict';
(()=>{
 const $=selector=>document.querySelector(selector),list=$('#conversations'),messages=$('#messages'),status=$('#status');
 let pairs=[],selected='',loadEpoch=0;
 const key=pair=>pair.boxAId+'/'+pair.boxBId;
 async function api(path,options={}){
  const response=await fetch(path,{credentials:'same-origin',...options});
  if(response.status===401){$('#login').hidden=false;throw Error('Log in to read box conversations.');}
  if(!response.ok){let problem;try{problem=await response.json()}catch{}throw Error(problem?.error||'Request failed: '+response.status)}
  return response.status===204?null:response.json();
 }
 function node(tag,text){const element=document.createElement(tag);element.textContent=text;return element}
 async function loadPairs(){
  pairs=await api('/v1/box-conversations');list.replaceChildren();$('#empty-list').hidden=pairs.length>0;
  for(const pair of pairs){const item=node('li',''),button=node('button',pair.boxAName+' ↔ '+pair.boxBName);button.type='button';button.setAttribute('aria-current',String(key(pair)===selected));button.append(node('span',pair.lastText));button.onclick=()=>void select(pair);item.append(button);list.append(item)}
  if(!selected&&location.hash.startsWith('#pair=')){
   const requested=decodeURIComponent(location.hash.slice(6));const match=pairs.find(pair=>key(pair)===requested||pair.boxBId+'/'+pair.boxAId===requested);
   if(match)void select(match);
  }
  if(selected&&!pairs.some(pair=>key(pair)===selected)){selected='';messages.replaceChildren();$('#title').textContent='Select a conversation';document.body.classList.remove('in-conversation')}
 }
 async function select(pair){
  selected=key(pair);location.hash='pair='+selected;document.body.classList.add('in-conversation');$('#title').textContent=pair.boxAName+' ↔ '+pair.boxBName;$('#subtitle').textContent='Direct messages · read only';
  for(const button of list.querySelectorAll('button'))button.setAttribute('aria-current',String(button.textContent.startsWith(pair.boxAName+' ↔ '+pair.boxBName)));
  await loadMessages(pair);
 }
 async function loadMessages(pair){
  const epoch=++loadEpoch,result=await api('/v1/box-conversations/'+encodeURIComponent(pair.boxAId)+'/'+encodeURIComponent(pair.boxBId)+'/messages');
  if(epoch!==loadEpoch||key(pair)!==selected)return;
  const atBottom=messages.scrollHeight-messages.scrollTop-messages.clientHeight<100;
  const signature=result.map(message=>message.id+':'+message.state+':'+(message.images||[]).length).join('|');
  if(messages.dataset.signature===signature)return;
  messages.dataset.signature=signature;messages.replaceChildren();
  for(const message of result){
   const row=node('article','');row.className='message'+(message.senderBoxId===pair.boxBId?' reverse':'');
   row.append(node('strong',message.senderBoxId===pair.boxAId?pair.boxAName:pair.boxBName));row.append(node('p',message.text));
   for(const image of message.images||[]){const link=document.createElement('a');link.href='/v1/messages/'+encodeURIComponent(message.id)+'/images/'+encodeURIComponent(image.id);link.target='_blank';link.rel='noopener';const preview=document.createElement('img');preview.src=link.href;preview.alt='Image '+image.number+' from '+(message.senderBoxId===pair.boxAId?pair.boxAName:pair.boxBName);link.append(preview);row.append(link)}
   row.append(node('time',new Date(message.createdAt).toLocaleString()));messages.append(row);
  }
  if(atBottom)messages.scrollTop=messages.scrollHeight;
  status.textContent=result.length===500?'Showing the latest 500 messages.':result.length+' messages';
 }
 async function refresh(){try{await loadPairs();const pair=pairs.find(pair=>key(pair)===selected);if(pair)await loadMessages(pair)}catch(error){status.textContent=error.message}}
 $('#login').onsubmit=async event=>{event.preventDefault();try{await api('/v1/browser-session',{method:'POST',headers:{Authorization:'Bearer '+event.target.elements.token.value}});event.target.reset();$('#login').hidden=true;await refresh()}catch(error){status.textContent=error.message}};
 $('#refresh').onclick=()=>void refresh();$('#back').onclick=()=>{document.body.classList.remove('in-conversation')};
 addEventListener('hashchange',()=>{const requested=decodeURIComponent(location.hash.replace(/^#pair=/,''));if(requested!==selected){const match=pairs.find(pair=>key(pair)===requested||pair.boxBId+'/'+pair.boxAId===requested);if(match)void select(match)}});
 navigator.serviceWorker?.addEventListener('message',event=>{if(event.data?.type==='vmbox-push')void refresh()});
 void refresh();setInterval(()=>{if(!document.hidden)void refresh()},2500);
})();
