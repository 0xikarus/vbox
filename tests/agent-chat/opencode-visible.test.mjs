import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {mkdtemp, rm} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import plugin from '../../internal/boxruntime/opencode_tui_plugin.mjs';

test('visible TUI bridge creates on home, follows navigation, and preserves image parts',async()=>{
 const home=await mkdtemp(path.join(os.tmpdir(),'vmbox-tui-test-'));
 const previousHome=process.env.HOME,previousPane=process.env.TMUX_PANE;
 process.env.HOME=home;process.env.TMUX_PANE='%42';
 let dispose,created=0;
 const submissions=[],route={current:{name:'home'},navigate(name,params){this.current={name,params}}};
 const api={route,lifecycle:{onDispose(fn){dispose=fn}},client:{session:{async create(){created++;return {data:{id:'ses_visible'}}},async promptAsync(input){submissions.push(input);return {}}}}};
 const send=parts=>new Promise((resolve,reject)=>{
  const request=http.request({socketPath:path.join(home,'.local/share/vmbox/opencode-tui/42.sock'),path:'/prompt',method:'POST'},response=>{let body='';response.on('data',chunk=>body+=chunk);response.on('end',()=>resolve({status:response.statusCode,body:JSON.parse(body)}))});
  request.on('error',reject);request.end(JSON.stringify({parts}));
 });
 try{
  await plugin.tui(api);
  const parts=[{type:'text',text:'First web message'},{type:'file',mime:'image/png',url:'data:image/png;base64,fixture'}];
  assert.deepEqual(await send(parts),{status:200,body:{sessionID:'ses_visible'}});
  assert.equal(created,1);assert.equal(route.current.params.sessionID,'ses_visible');
  assert.deepEqual(submissions[0],{sessionID:'ses_visible',parts});
  route.navigate('session',{sessionID:'ses_user_selected'});
  await send([{type:'text',text:'Follow the actual desktop'}]);
  assert.equal(created,1);assert.equal(submissions[1].sessionID,'ses_user_selected');
  route.navigate('settings');
  assert.equal((await send([{type:'text',text:'Do not guess'}])).status,502);
  assert.equal(submissions.length,2);
 }finally{
  if(dispose)await dispose();
  if(previousHome===undefined)delete process.env.HOME;else process.env.HOME=previousHome;
  if(previousPane===undefined)delete process.env.TMUX_PANE;else process.env.TMUX_PANE=previousPane;
  await rm(home,{recursive:true,force:true});
 }
});
