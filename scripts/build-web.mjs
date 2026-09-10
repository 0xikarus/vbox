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
await build({stdin:{contents:"export {default} from '@novnc/novnc/lib/rfb.js';",resolveDir:process.cwd(),loader:'js'},plugins:[fullscreenCursor],bundle:true,format:'iife',globalName:'NoVNC',minify:true,outfile:'internal/controller/web/novnc.js',legalComments:'eof'});
for(const [source,target] of [
 ['@novnc/novnc/LICENSE.txt','novnc-LICENSE.txt'],
 ['@novnc/novnc/AUTHORS','novnc-AUTHORS.txt'],
 ['@xterm/xterm/lib/xterm.js','xterm.js'],
 ['@xterm/xterm/css/xterm.css','xterm.css'],
 ['@xterm/xterm/LICENSE','xterm-LICENSE.txt'],
 ['@xterm/addon-fit/lib/addon-fit.js','xterm-fit.js'],
 ['@xterm/addon-fit/LICENSE','xterm-fit-LICENSE.txt'],
])await copyFile('node_modules/'+source,'internal/controller/web/'+target);
const authors='internal/controller/web/novnc-AUTHORS.txt';
await writeFile(authors,(await readFile(authors,'utf8')).replace(/[ \t]+$/gm,''));
