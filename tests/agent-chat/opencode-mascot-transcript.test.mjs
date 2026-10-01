import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import {mkdtemp,readFile,rm} from 'node:fs/promises';
import plugin from '../../internal/boxruntime/opencode_tui_plugin.mjs';

function get(socket,pathname){return new Promise((resolve,reject)=>{
 const request=http.get({socketPath:socket,path:pathname},response=>{
  let data='';response.on('data',chunk=>data+=chunk);
  response.on('end',()=>resolve({status:response.statusCode,body:JSON.parse(data)}));
 });request.once('error',reject);
})}

test('OpenCode MCP bridge samples only the visible native conversation',async()=>{
 const home=await mkdtemp(path.join(os.tmpdir(),'vmbox-mascot-plugin-'));
 const oldHome=process.env.HOME,oldPane=process.env.TMUX_PANE,oldSession=process.env.VMBOX_CHAT_SESSION;
 process.env.HOME=home;process.env.TMUX_PANE='%123';process.env.VMBOX_CHAT_SESSION='opencode-managed';
 let dispose;
 const route={current:{name:'session',params:{sessionID:'ses_active'}}};
 const api={route,lifecycle:{onDispose:callback=>{dispose=callback}},client:{session:{messages:async({sessionID,limit})=>{
  assert.equal(limit,24);
  return {data:[{info:{role:'user'},parts:[{type:'text',text:'Please fix the error'}]},
   {info:{role:'assistant'},parts:[{type:'text',text:'The build succeeded for '+sessionID+'.'},
    ...(sessionID==='ses_next'?[{type:'tool',state:{status:'running'}}]:[])]}]};
 }}}};
 try{
  await plugin.tui(api);
  const marker=JSON.parse(await readFile(path.join(home,'.local/share/vmbox/opencode-tui/mascot-opencode-managed.json'),'utf8'));
  const first=await get(marker.socket,'/mascot-transcript');
  assert.equal(first.status,200);
  assert.equal(first.body.instance,marker.instance);
  assert.match(first.body.text,/assistant: The build succeeded for ses_active/);
  route.current.params.sessionID='ses_next';
  const next=await get(marker.socket,'/mascot-transcript');
  assert.match(next.body.text,/assistant: The build succeeded for ses_next/);
  assert.match(next.body.text,/tool: Running tool/);
  assert.doesNotMatch(next.body.text,/ses_active/);
 }finally{
  if(dispose)await dispose();
  if(oldHome===undefined)delete process.env.HOME;else process.env.HOME=oldHome;
  if(oldPane===undefined)delete process.env.TMUX_PANE;else process.env.TMUX_PANE=oldPane;
  if(oldSession===undefined)delete process.env.VMBOX_CHAT_SESSION;else process.env.VMBOX_CHAT_SESSION=oldSession;
  await rm(home,{recursive:true,force:true});
 }
});
