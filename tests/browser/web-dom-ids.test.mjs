import assert from 'node:assert/strict';
import {test} from 'node:test';
import {readFile} from 'node:fs/promises';

const web='internal/controller/web/';
const pages=[['chat.html','chat.js'],['index.html','app.js'],['workspace.html','workspace.js']];

function referencedIds(source){
 const ids=new Set();
 for(const match of source.matchAll(/(?:\$\(|\bquerySelector(?:All)?\()\s*(['"`])#([A-Za-z][\w-]*)/g))ids.add(match[2]);
 for(const match of source.matchAll(/\bgetElementById\(\s*(['"`])([A-Za-z][\w-]*)/g))ids.add(match[2]);
 return ids;
}

function missingIds(html,script){
 const declared=new Set([...html.matchAll(/\bid\s*=\s*['"]([A-Za-z][\w-]*)['"]/g)].map(match=>match[1]));
 const fragments=[...script.matchAll(/\.innerHTML\s*=\s*(['"`])([\s\S]*?)\1/g)].map(match=>match[2]);
 const dynamic=new Set([
  ...[...script.matchAll(/\.id\s*=\s*['"]([A-Za-z][\w-]*)['"]/g)].map(match=>match[1]),
  ...[...script.matchAll(/\.setAttribute\(\s*['"]id['"]\s*,\s*['"]([A-Za-z][\w-]*)['"]/g)].map(match=>match[1]),
  ...fragments.flatMap(fragment=>[...fragment.matchAll(/\bid\s*=\s*['"]([A-Za-z][\w-]*)['"]/g)].map(match=>match[1])),
 ]);
 return [...referencedIds(script)].filter(id=>!declared.has(id)&&!dynamic.has(id)).sort();
}

test('static DOM ID check catches a missing element and accepts a created one',()=>{
 assert.deepEqual(missingIds('<div id="present"></div>',"$('#present'); document.getElementById('missing');"),['missing']);
 assert.deepEqual(missingIds('',"const panel=document.createElement('div'); panel.id='dynamic'; document.querySelector('#dynamic');"),[]);
 assert.deepEqual(missingIds('',"const id='missing'; document.querySelector('#missing');"),['missing']);
});

for(const [htmlName,scriptName] of pages)test(`${scriptName} static ID selectors exist in ${htmlName} or are created by script`,async()=>{
 const [html,script]=await Promise.all([readFile(web+htmlName,'utf8'),readFile(web+scriptName,'utf8')]);
 // No optional IDs are currently needed. Add an explicit, documented allowlist
 // here if a selector intentionally targets an element supplied elsewhere.
 const optionalIds=new Set();
 const missing=missingIds(html,script).filter(id=>!optionalIds.has(id));
 assert.deepEqual(missing,[],`${scriptName} references missing IDs: ${missing.join(', ')}`);
});
