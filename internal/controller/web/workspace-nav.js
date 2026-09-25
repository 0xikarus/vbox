'use strict';
window.VMBoxWorkspaceNav=(()=>{
 function init({menuId,panelId,usageId}){
  const menu=document.getElementById(menuId),panel=document.getElementById(panelId),usage=document.getElementById(usageId);
  if(!menu||!panel||!usage)return {setOwner(){}};
  const dialog=document.createElement('dialog');dialog.className='workspace-usage-dialog';dialog.setAttribute('aria-label','Profile usage limits');
  const header=document.createElement('header'),title=document.createElement('h2');title.textContent='Profile usage limits';
  const close=document.createElement('button');close.type='button';close.textContent='Close';close.onclick=()=>dialog.close();header.append(title,close);
  const status=document.createElement('p');status.setAttribute('role','status');
  const list=document.createElement('div');dialog.append(header,status,list);document.body.append(dialog);
  const closeMenu=()=>{panel.hidden=true;menu.setAttribute('aria-expanded','false')};
  menu.onclick=()=>{
   if(!panel.hidden){closeMenu();return}
   const rect=menu.getBoundingClientRect();panel.style.left=Math.max(8,Math.min(rect.left,innerWidth-panel.offsetWidth-8))+'px';panel.style.top=(rect.bottom+5)+'px';panel.hidden=false;menu.setAttribute('aria-expanded','true');
   // The panel's width is measurable only after it is shown.
   panel.style.left=Math.max(8,Math.min(rect.left,innerWidth-panel.offsetWidth-8))+'px';
  };
  document.addEventListener('pointerdown',event=>{if(!panel.hidden&&!panel.contains(event.target)&&event.target!==menu)closeMenu()});
  document.addEventListener('keydown',event=>{if(event.key==='Escape'&&!panel.hidden){event.preventDefault();closeMenu();menu.focus()}});
  const format=value=>new Intl.NumberFormat(undefined,{maximumFractionDigits:2}).format(value);
  async function showUsage(){
   closeMenu();list.replaceChildren();status.textContent='Loading profile usage…';dialog.showModal();
   try{
    const response=await fetch('/v1/profile-usage',{credentials:'same-origin',cache:'no-store'});
    const data=await response.json().catch(()=>({}));if(!response.ok)throw Error(data.error||'Could not load usage');
    if(!dialog.open)return;
    const profiles=Array.isArray(data.profiles)?data.profiles:[];
    status.textContent=profiles.length?'':'No saved agent profiles yet.';
    for(const profile of profiles){
     const card=document.createElement('article');card.className='workspace-usage-profile';
     const heading=document.createElement('h3');heading.textContent=profile.application+' · '+profile.name;card.append(heading);
     const windows=profile.snapshot?.windows||[];
     if(!windows.length){const note=document.createElement('p');note.textContent=profile.error||'Remaining usage unavailable.';card.append(note)}
     for(const window of windows){
      const row=document.createElement('div');row.className='workspace-usage-window';
      const label=document.createElement('span');label.textContent=[window.name,window.scope].filter(Boolean).join(' · ');
      const remaining=document.createElement('strong');remaining.textContent=Number.isFinite(window.usedPercent)?format(Math.max(0,Math.min(100,100-window.usedPercent)))+'% remaining':'Unavailable';
      row.append(label,remaining);card.append(row);
     }
     if(profile.snapshot?.note){const note=document.createElement('p');note.textContent=profile.snapshot.note;card.append(note)}
     list.append(card);
    }
   }catch(error){if(dialog.open)status.textContent=error.message}
  }
  usage.onclick=()=>void showUsage();
  return {setOwner(owner){usage.hidden=!owner;if(!owner&&dialog.open)dialog.close()},closeMenu};
 }
 return {init};
})();
