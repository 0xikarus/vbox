/* Browser text size is device-local. CSS supplies the small phone/medium desktop defaults. */
(()=>{
 const key='vboxTextSize',sizes=['small','medium','large','xl'],root=document.documentElement;
 let saved='';try{saved=localStorage.getItem(key)||''}catch{}
 if(sizes.includes(saved))root.dataset.textSize=saved;
 const current=()=>root.dataset.textSize||(matchMedia('(max-width:600px)').matches?'small':'medium');
 function refresh(){document.querySelectorAll('#text-size-controls [data-size]').forEach(button=>button.setAttribute('aria-pressed',String(button.dataset.size===current())))}
 function set(size){if(!sizes.includes(size))return;root.dataset.textSize=size;try{localStorage.setItem(key,size)}catch{}refresh()}
 window.VBoxTextSize={get:current,set};
 document.addEventListener('DOMContentLoaded',()=>{
  document.querySelectorAll('#text-size-controls [data-size]').forEach(button=>button.addEventListener('click',()=>set(button.dataset.size)));
  refresh();
  matchMedia('(max-width:600px)').addEventListener?.('change',refresh);
 });
})();
