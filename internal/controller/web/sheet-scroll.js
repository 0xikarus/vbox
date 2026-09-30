// Shared mobile sheet scrolling, including a persistent scroll cue. SVG geometry
// is updated through attributes so this also works with the index page CSP.
(()=>{
 const selector='.sheet-card,.modal .card,.model-picker-dialog';
 function mount(card){
  if(card.dataset.sheetScrollReady)return;
  const header=card.querySelector(':scope > header,:scope > .model-picker-header');
  if(!header)return;
  const frame=document.createElement('div');frame.className='sheet-scroll-frame';
  const body=document.createElement('div');body.className='sheet-scroll-body';
  while(header.nextSibling)body.append(header.nextSibling);
  frame.append(body);card.append(frame);
  decorate(frame,body);
  card.dataset.sheetScrollReady='true';
  // A downward drag on the header may dismiss a sheet only at the top. The
  // content itself remains a native, vertically scrollable touch surface.
  let start=null;
  header.addEventListener('touchstart',event=>{
   if(event.touches.length!==1||event.target.closest('button,a,input'))return;
   start=body.scrollTop<1?event.touches[0].clientY:null;
  },{passive:true});
  header.addEventListener('touchend',event=>{
   if(start===null)return;
   const distance=event.changedTouches[0].clientY-start;start=null;
   if(distance<90||body.scrollTop>1)return;
   const close=header.querySelector('[data-close],button[aria-label^="Close"]');
   close?.click();
  },{passive:true});
  header.addEventListener('touchcancel',()=>{start=null},{passive:true});
 }
 function decorate(frame,body){
  if(frame.dataset.sheetScrollReady)return;
  const indicator=document.createElementNS('http://www.w3.org/2000/svg','svg');
  indicator.setAttribute('class','sheet-scroll-indicator');
  indicator.setAttribute('viewBox','0 0 3 100');
  indicator.setAttribute('preserveAspectRatio','none');
  indicator.setAttribute('aria-hidden','true');
  const thumb=document.createElementNS('http://www.w3.org/2000/svg','rect');
  thumb.setAttribute('x','0');thumb.setAttribute('width','3');thumb.setAttribute('rx','1.5');
  indicator.append(thumb);frame.append(indicator);
  let idleTimer;
  const update=()=>{
   const range=body.scrollHeight-body.clientHeight;
   const visible=range>2&&body.clientHeight>0;
   frame.classList.toggle('sheet-can-scroll',visible);
   frame.classList.toggle('sheet-has-above',visible&&body.scrollTop>2);
   frame.classList.toggle('sheet-has-below',visible&&body.scrollTop<range-2);
   if(!visible)return;
   const height=Math.max(12,100*body.clientHeight/body.scrollHeight);
   thumb.setAttribute('height',height.toFixed(2));
   thumb.setAttribute('y',((100-height)*body.scrollTop/range).toFixed(2));
  };
  body.addEventListener('scroll',()=>{
   update();frame.classList.remove('sheet-scroll-idle');
   clearTimeout(idleTimer);idleTimer=setTimeout(()=>frame.classList.add('sheet-scroll-idle'),1200);
  },{passive:true});
  new ResizeObserver(update).observe(body);
  new ResizeObserver(update).observe(frame);
  new MutationObserver(update).observe(body,{subtree:true,childList:true,attributes:true,characterData:true});
  frame.dataset.sheetScrollReady='true';
  requestAnimationFrame(update);
 }
 function init(){
  document.querySelectorAll(selector).forEach(mount);
  document.querySelectorAll('.sheet-scroll-frame:has(> .sheet-scroll-body):not([data-sheet-scroll-ready])').forEach(frame=>decorate(frame,frame.querySelector(':scope > .sheet-scroll-body')));
  // Details and New box already have dedicated scroll regions; keep their
  // existing layout and decorate those regions rather than moving content.
  const inspect=document.querySelector('#inspect');
  const inspectBody=document.querySelector('#inspect-body');
  if(inspect&&inspectBody&&!inspect.querySelector('.sheet-scroll-inspect')){
   const frame=document.createElement('div');frame.className='sheet-scroll-frame sheet-scroll-inspect';
   inspectBody.replaceWith(frame);frame.append(inspectBody);decorate(frame,inspectBody);
  }
  const newBox=document.querySelector('#new-box-card .new-box-body');
  if(newBox&&!newBox.querySelector('.sheet-scroll-new-box')){
   const form=newBox.querySelector('#create-box');
   if(form){const frame=document.createElement('div');frame.className='sheet-scroll-frame sheet-scroll-new-box';form.replaceWith(frame);frame.append(form);decorate(frame,form)}
  }
  const rowMenu=document.querySelector('#row-menu');
  if(rowMenu){
   new MutationObserver(()=>{
    if(rowMenu.querySelector('.sheet-scroll-menu-body')||!rowMenu.childElementCount)return;
    const heading=rowMenu.querySelector('.row-menu-heading');
    const frame=document.createElement('div');frame.className='sheet-scroll-frame sheet-scroll-menu-frame';
    const body=document.createElement('div');body.className='sheet-scroll-body sheet-scroll-menu-body';
    for(const child of [...rowMenu.children])if(child!==heading)body.append(child);
    frame.append(body);rowMenu.append(frame);decorate(frame,body);
   }).observe(rowMenu,{childList:true});
  }
  new MutationObserver(()=>{
   document.querySelectorAll(selector).forEach(mount);
   document.querySelectorAll('.sheet-scroll-frame:has(> .sheet-scroll-body):not([data-sheet-scroll-ready])').forEach(frame=>decorate(frame,frame.querySelector(':scope > .sheet-scroll-body')));
  }).observe(document.body,{subtree:true,childList:true});
 }
 if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',init,{once:true});else init();
})();
