import assert from 'node:assert/strict';
import {mkdtemp,readFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawn,execFileSync} from 'node:child_process';
import {randomBytes} from 'node:crypto';

const runtime=process.argv[2];if(!runtime)throw Error('usage: node tests/web-terminal-runtime.mjs /path/to/vmbox-runtime');
const dir=await mkdtemp(join(tmpdir(),'vmbox-web-terminal-')),env={...process.env,TMUX_TMPDIR:dir,TMUX:'',TERM:'xterm-256color'},fence=randomBytes(32).toString('hex');
const tmux=(...args)=>execFileSync('/usr/bin/tmux',args,{env,timeout:5000,encoding:'utf8'});
let child;
try{
 execFileSync(runtime,['native-bind',fence],{env,timeout:5000});
 tmux('new-session','-d','-s','web-proof','bash --noprofile --norc');
 const inv=JSON.parse(execFileSync(runtime,['native-sessions',fence],{env,timeout:5000,encoding:'utf8'})),session=inv.sessions.find(s=>s.name==='web-proof');
 child=spawn(runtime,['web-terminal',fence,session.id,session.incarnation],{env,stdio:['pipe','pipe','pipe']});
 let output='',errors='';child.stdout.on('data',b=>output+=b.toString());child.stderr.on('data',b=>errors+=b.toString());
 const wait=async predicate=>{const end=Date.now()+8000;while(!await predicate()){if(Date.now()>end)throw Error('terminal timeout: '+errors);await new Promise(r=>setTimeout(r,50))}};
 await wait(()=>output.length>0);
 child.stdin.write(JSON.stringify({cols:110,rows:35})+'\n');
 const marker='proof-'+randomBytes(8).toString('hex')+'-üßZy',file=join(dir,'received');
 const command=`printf '%s' '${marker}' > '${file}'; printf '\\nSCREEN-${marker}\\n'\r`;
 child.stdin.write(JSON.stringify({data:Buffer.from(command.replace(/\\r$/,'\r')).toString('base64')})+'\n');
 await wait(async()=>{try{return await readFile(file,'utf8')===marker}catch{return false}});
 await wait(()=>output.includes('SCREEN-'+marker));
 assert(tmux('capture-pane','-p','-t','web-proof').includes('SCREEN-'+marker));
 child.stdin.end();await new Promise(r=>child.once('exit',r));
 assert.equal(tmux('has-session','-t','web-proof'),'');
 console.log('PASS: real PTY input, independent file bytes, tmux screen, disconnect preserves session. Artifacts: '+dir);
}finally{child?.kill();try{tmux('kill-server')}catch{}}
