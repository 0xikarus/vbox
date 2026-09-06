import {build} from 'esbuild';
import {copyFile,readFile,writeFile} from 'node:fs/promises';
await build({stdin:{contents:"export {default} from '@novnc/novnc/lib/rfb.js';",resolveDir:process.cwd(),loader:'js'},bundle:true,format:'iife',globalName:'NoVNC',minify:true,outfile:'internal/controller/web/novnc.js',legalComments:'eof'});
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
