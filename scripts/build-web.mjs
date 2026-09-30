import {build} from 'esbuild';
import {copyFile,readFile,writeFile} from 'node:fs/promises';
// noVNC 1.5's touch/fallback cursor is appended to document.body, outside
// a fullscreen desktop's top layer. Keep it with its VNC canvas instead.
const fullscreenCursor={name:'fullscreen-cursor',setup(builder){builder.onLoad({filter:/[\\/]util[\\/]cursor\.js$/},async({path})=>{
 let source=await readFile(path,'utf8');
 for(const [from,to] of [['document.body.appendChild(this._canvas);','this._target.parentNode.appendChild(this._canvas);'],['document.body.removeChild(this._canvas);','this._canvas.remove();']]){
  if(!source.includes(from))throw Error('noVNC cursor changed; review fullscreen patch');
  source=source.replace(from,to);
 }
 return {contents:source,loader:'js'};
})}};
// Fence echoes measure the complete browser → controller → worker VNC path.
const fenceLatency={name:'fence-latency',setup(builder){builder.onLoad({filter:/[\\/]lib[\\/]rfb\.js$/},async({path})=>{
 let source=await readFile(path,'utf8');
 const from='return this._fail("Unexpected fence response");';
 if(!source.includes(from))throw Error('noVNC fence handler changed; review latency patch');
 source=source.replace(from,`this.dispatchEvent(new CustomEvent("fenceresponse", {detail: {payload: payload}})); return true;`);
 return {contents:source,loader:'js'};
})}};
await build({stdin:{contents:`import RFB from '@novnc/novnc/lib/rfb.js';
RFB.prototype.requestLatencyProbe=function(payload){
 if(this._rfbConnectionState!=='connected'||!this._supportsFence)return false;
 if(typeof payload!=='string'||payload.length>64)throw Error('Invalid fence payload');
 RFB.messages.clientFence(this._sock,0x80000000,payload);return true;
};export default RFB;`,resolveDir:process.cwd(),loader:'js'},plugins:[fullscreenCursor,fenceLatency],bundle:true,format:'iife',globalName:'NoVNC',minify:true,outfile:'internal/controller/web/novnc.js',legalComments:'eof'});
// Keep Motion's hybrid animate engine (JS object timelines and springs), while
// tree-shaking exports that the mascot never uses.
await build({stdin:{contents:"import {animate} from 'motion';export {animate};",resolveDir:process.cwd(),loader:'js'},bundle:true,format:'iife',globalName:'Motion',minify:true,outfile:'internal/controller/web/motion.js',legalComments:'none'});
for(const [source,target] of [
 ['@novnc/novnc/LICENSE.txt','novnc-LICENSE.txt'],
 ['@novnc/novnc/AUTHORS','novnc-AUTHORS.txt'],
 ['@xterm/xterm/lib/xterm.js','xterm.js'],
 ['motion/LICENSE.md','motion-LICENSE.md'],
 ['@xterm/xterm/css/xterm.css','xterm.css'],
 ['@xterm/xterm/LICENSE','xterm-LICENSE.txt'],
 ['@xterm/addon-fit/lib/addon-fit.js','xterm-fit.js'],
 ['@xterm/addon-fit/LICENSE','xterm-fit-LICENSE.txt'],
])await copyFile('node_modules/'+source,'internal/controller/web/'+target);
const authors='internal/controller/web/novnc-AUTHORS.txt';
await writeFile(authors,(await readFile(authors,'utf8')).replace(/[ \t]+$/gm,''));
