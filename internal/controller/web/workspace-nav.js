'use strict';
window.VMBoxWorkspaceNav=(()=>{
 function updateUsagePill(button,profiles,owner=true){
  if(!button)return;
  const remaining=(Array.isArray(profiles)?profiles:[]).flatMap(profile=>(profile.snapshot?.windows||profile.windows||[]).map(window=>window.usedPercent)).filter(used=>typeof used==='number'&&Number.isFinite(used)).map(used=>Math.max(0,Math.min(100,100-used)));
  const lowest=remaining.length?Math.min(...remaining):null;
  button.hidden=!owner||lowest===null;
  if(button.hidden)return;
  if(!button.querySelector('.workspace-usage-ring')){
   const ns='http://www.w3.org/2000/svg',ring=document.createElementNS(ns,'svg');ring.classList.add('workspace-usage-ring');ring.setAttribute('viewBox','0 0 20 20');ring.setAttribute('aria-hidden','true');
   for(const className of ['workspace-usage-ring-track','workspace-usage-ring-value']){const circle=document.createElementNS(ns,'circle');circle.classList.add(className);for(const [key,value] of Object.entries({cx:'10',cy:'10',r:'7',fill:'none','stroke-width':'3',pathLength:'100'}))circle.setAttribute(key,value);ring.append(circle)}
   const value=document.createElement('span');value.className='workspace-usage-percent';const left=document.createElement('span');left.className='workspace-usage-left';left.textContent='left';button.replaceChildren(ring,value,left);
  }
  const percent=Math.round(lowest);
  button.querySelector('.workspace-usage-ring-value').setAttribute('stroke-dasharray',lowest+' 100');
  button.querySelector('.workspace-usage-percent').textContent=percent+'%';
  button.classList.toggle('usage-low',lowest<=20);
  button.setAttribute('aria-label','Usage limits for all saved profiles: '+percent+'% left');
  button.title='Usage limits for all saved profiles · '+percent+'% left';
 }
 function init({menuId,panelId,usageId}){
  const menu=menuId?document.getElementById(menuId):null,panel=panelId?document.getElementById(panelId):null,usage=document.getElementById(usageId);
  if(!usage)return {setOwner(){},closeMenu(){}};
  const dialog=document.createElement('dialog');dialog.className='workspace-usage-dialog vb-sheet-dialog';dialog.setAttribute('aria-label','Profile usage limits');
  const header=document.createElement('header');header.className='vb-sheet-header';const title=document.createElement('h2');title.textContent='Profile usage limits';
  const close=document.createElement('button');close.type='button';close.textContent='Close';close.onclick=()=>dialog.close();header.append(title,close);
  const status=document.createElement('p');status.setAttribute('role','status');
  const list=document.createElement('div'),frame=document.createElement('div'),body=document.createElement('div');frame.className='sheet-scroll-frame';body.className='sheet-scroll-body';body.append(status,list);frame.append(body);dialog.append(header,frame);document.body.append(dialog);
  const closeMenu=()=>{if(!panel||!menu)return;panel.hidden=true;menu.setAttribute('aria-expanded','false')};
  if(menu&&panel)menu.onclick=()=>{
   if(!panel.hidden){closeMenu();return}
   const rect=menu.getBoundingClientRect();panel.style.left=Math.max(8,Math.min(rect.left,innerWidth-panel.offsetWidth-8))+'px';panel.style.top=(rect.bottom+5)+'px';panel.hidden=false;menu.setAttribute('aria-expanded','true');
   // The panel's width is measurable only after it is shown.
   panel.style.left=Math.max(8,Math.min(rect.left,innerWidth-panel.offsetWidth-8))+'px';
  };
  if(menu&&panel){document.addEventListener('pointerdown',event=>{if(!panel.hidden&&!panel.contains(event.target)&&event.target!==menu)closeMenu()});
  document.addEventListener('keydown',event=>{if(event.key==='Escape'&&!panel.hidden){event.preventDefault();closeMenu();menu.focus()}})}
  const format=value=>new Intl.NumberFormat(undefined,{maximumFractionDigits:2}).format(value);
  let owner=false;
  async function refreshIndicator(){
   if(!owner||document.hidden)return;
   try{const response=await fetch('/v1/profile-usage',{credentials:'same-origin',cache:'no-store'});if(!response.ok)throw Error('Usage unavailable');const data=await response.json();if(owner)updateUsagePill(usage,data.profiles,true)}
   catch{if(owner)usage.hidden=true}
  }
  setInterval(()=>void refreshIndicator(),60000);
  async function showUsage(){
   closeMenu();list.replaceChildren();status.textContent='Loading profile usage…';dialog.showModal();
   try{
    const response=await fetch('/v1/profile-usage',{credentials:'same-origin',cache:'no-store'});
    const data=await response.json().catch(()=>({}));if(!response.ok)throw Error(data.error||'Could not load usage');
    if(!dialog.open)return;
    const profiles=Array.isArray(data.profiles)?data.profiles:[];
    updateUsagePill(usage,profiles,owner);
    status.textContent=profiles.length?'':'No saved agent profiles yet.';
    for(const profile of profiles){
     const card=document.createElement('article');card.className='workspace-usage-profile';
     const heading=document.createElement('h3');heading.textContent=profile.application+' · '+profile.name;card.append(heading);
     const windows=profile.snapshot?.windows||[];
     if(!windows.length){const note=document.createElement('p');note.textContent=profile.error||'Remaining usage unavailable.';card.append(note)}
     const shortNames={session:'Session',weekly_all:'Week',weekly_scoped:'Week',primary:'Primary',secondary:'Secondary'};
     const duration=minutes=>!minutes?'':minutes>=1440?Math.round(minutes/1440)+'d':minutes>=60?Math.round(minutes/60)+'h':minutes+' min';
     for(const window of windows){
      const percent=Number.isFinite(window.usedPercent)?Math.max(0,Math.min(100,100-window.usedPercent)):null;
      const row=document.createElement('div');row.className='workspace-usage-window';
      if(percent!==null)row.dataset.level=percent>30?'ok':percent>=10?'warn':'low';
      const label=document.createElement('span');const raw=window.name||'';let text=shortNames[raw]||(/^[a-z]+$/.test(raw)?raw.charAt(0).toUpperCase()+raw.slice(1):raw)||'Limit';const dur=duration(window.durationMinutes);if(dur)text+=' · '+dur;else if(window.scope)text+=' · '+window.scope;label.textContent=text;
      const bar=document.createElement('span');bar.className='usage-bar';
      if(percent!==null){const fill=document.createElement('i');fill.style.width=percent+'%';bar.append(fill)}else bar.classList.add('usage-bar-empty');
      const remaining=document.createElement('strong');remaining.textContent=percent!==null?format(percent)+'% left':'—';if(percent!==null&&percent<=10)remaining.classList.add('low');
      row.append(label,bar,remaining);card.append(row);
     }
     if(profile.snapshot?.note){const note=document.createElement('p');note.textContent=profile.snapshot.note;card.append(note)}
     list.append(card);
    }
   }catch(error){if(dialog.open)status.textContent=error.message}
  }
  usage.onclick=()=>void showUsage();
  return {setOwner(value){owner=!!value;usage.hidden=true;if(!owner&&dialog.open)dialog.close();if(owner)void refreshIndicator()},closeMenu,refreshUsage:refreshIndicator};
 }
 return {init,updateUsagePill};
})();
