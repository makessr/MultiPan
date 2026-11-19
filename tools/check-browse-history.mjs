import fs from 'node:fs/promises';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
const root=path.resolve(import.meta.dirname,'../frontend');
const {default:ts}=await import(pathToFileURL(path.join(root,'node_modules/typescript/lib/typescript.js')));
const code=ts.transpileModule(await fs.readFile(path.join(root,'src/browseHistory.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const {BrowseHistory}=await import('data:text/javascript;base64,'+Buffer.from(code).toString('base64'));
const h=new BrowseHistory(),loc=(id,name=id)=>({id,breadcrumbs:[{id:'0',name:'根目录'},{id,name}]});
assert.equal(h.current('a'),null);assert.equal(h.step('a',-1),null);
h.record('a',loc('0'));h.record('a',loc('9007199254740993'));h.record('a',loc('child'));
const previous=h.step('a',-1);assert.equal(previous.location.id,'9007199254740993');
assert.equal(h.current('a').id,'child'); // asking for a target must not commit a failed request
assert.equal(h.commit('a',previous.index,previous.location),true);assert.equal(h.step('a',1).location.id,'child');
h.record('a',loc('9007199254740993','新名称'));assert.equal(h.step('a',1).location.id,'child'); // refresh preserves forward history
assert.equal(h.current('a').breadcrumbs[1].name,'新名称');
h.record('a',loc('branch'));assert.equal(h.step('a',1),null);assert.equal(h.step('a',-1).location.id,'9007199254740993');
assert.equal(h.commit('a',0,loc('not-root')),false);assert.equal(h.commit('a',-1,loc('not-root')),false);assert.equal(h.current('a').id,'branch');
const input=loc('isolated');h.record('b',input);input.breadcrumbs[1].name='mutated';assert.equal(h.current('b').breadcrumbs[1].name,'isolated');const output=h.current('b');output.breadcrumbs[0].name='mutated';assert.equal(h.current('b').breadcrumbs[0].name,'根目录');
assert.equal(h.current('a').id,'branch');h.retain(['a']);assert.equal(h.current('b'),null);h.clear('a');assert.equal(h.current('a'),null);
for(let i=0;i<55;i++)h.record('bounded',loc(String(i)));
let visited=1;while(h.step('bounded',-1)){const target=h.step('bounded',-1);h.commit('bounded',target.index,target.location);visited++}
assert.equal(visited,50);assert.equal(h.current('bounded').id,'5');
h.record('bounded',loc(''));assert.equal(h.current('bounded').id,'5');
console.log('PASS: account-isolated navigation, successful commit only, refresh/branch rules, bounded history, cloned breadcrumbs and exact IDs.');
