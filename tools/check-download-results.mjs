import fs from 'node:fs/promises';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
const root=path.resolve(import.meta.dirname,'../frontend');
const {default:ts}=await import(pathToFileURL(path.join(root,'node_modules/typescript/lib/typescript.js')));
const source=await fs.readFile(path.join(root,'src/downloadResults.ts'),'utf8');
const code=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const {filterResults,resultCounts}=await import('data:text/javascript;base64,'+Buffer.from(code).toString('base64'));
const rows=Object.freeze([
 {name:'目录/Alpha.ZIP',status:'saved',result:{verification:'verified'}},
 {name:'目录/待核对.zip',status:'saved',result:{verification:'unverified'}},
 {name:'旧版.bin',status:'saved',result:{}},
 {name:'失败.zip',status:'failed',result:{verification:'verified'}},
 {name:'取消.zip',status:'cancelled'},
 {name:'未执行.zip',status:'skipped'},
].map(x=>Object.freeze({...x,result:x.result?Object.freeze(x.result):undefined})));
assert.deepEqual(resultCounts(rows),{all:6,verified:1,unverified:2,unfinished:3});
assert.deepEqual(filterResults(rows,'verified',' ZIP '),[rows[0]]);
assert.deepEqual(filterResults(rows,'unverified','目录/'),[rows[1]]);
assert.deepEqual(filterResults(rows,'all','alpha.zip'),[rows[0]]);
assert.deepEqual(filterResults(rows,'unfinished',''),rows.slice(3));
assert.deepEqual(filterResults(rows,'verified','不存在'),[]);
assert.deepEqual(resultCounts([]),{all:0,verified:0,unverified:0,unfinished:0});
assert.notEqual(filterResults(rows,'all',''),rows);
assert.equal(rows.length,6);
console.log('PASS: mutually exclusive download outcomes, saved-unverified is not unfinished, name/path search, immutable source and empty results.');
