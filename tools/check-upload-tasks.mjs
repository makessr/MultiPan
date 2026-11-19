import fs from 'node:fs/promises';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
const root=path.resolve(import.meta.dirname,'../frontend');
const {default:ts}=await import(pathToFileURL(path.join(root,'node_modules/typescript/lib/typescript.js')));
const code=ts.transpileModule(await fs.readFile(path.join(root,'src/uploadTasks.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const {filterUploadTasks,uploadCounts,uploadTaskReport,uploadSourceKey,uploadGroups,uploadGroupKey}=await import('data:text/javascript;base64,'+Buffer.from(code).toString('base64'));
const tasks=Object.freeze(['queued','running','failed','paused','success','unknown'].map((status,i)=>Object.freeze({id:String(i),status,kind:i%2?'quark':'lanzou',file:Object.freeze({name:i===2?'报告.ZIP':'文档'+i+'.txt'}),folder:'/raw',folderDisplay:i===3?'根目录 / 备份':'根目录'})));
assert.deepEqual(uploadCounts(tasks),{all:6,active:2,attention:2,done:1});
assert.deepEqual(filterUploadTasks(tasks,'all','',''),[...tasks].reverse());
assert.deepEqual(filterUploadTasks(tasks,'attention','lanzou',' ZIP '),[tasks[2]]);
assert.deepEqual(filterUploadTasks(tasks,'attention','quark','备份'),[tasks[3]]);
assert.deepEqual(filterUploadTasks(tasks,'active','',''),[tasks[1],tasks[0]]);
assert.deepEqual(filterUploadTasks(tasks,'done','',''),[tasks[4]]);
assert.deepEqual(filterUploadTasks(tasks,'all','baidu',''),[]);
assert.deepEqual(filterUploadTasks([{...tasks[0],folderDisplay:''}],'all','','/RAW').length,1);
assert.deepEqual(uploadCounts([]),{all:0,active:0,attention:0,done:0});
assert.equal(tasks[0].id,'0');
assert.deepEqual(filterUploadTasks(tasks,'all','','','oldest'),tasks);
assert.deepEqual(filterUploadTasks(tasks,'all','','','attention').map(t=>t.status),['failed','paused','running','queued','success','unknown']);
const natural=Object.freeze([10,2,1].map(i=>Object.freeze({...tasks[0],id:String(i),file:Object.freeze({name:'文件'+i+'.zip'})})));
assert.deepEqual(filterUploadTasks(natural,'all','','','name').map(t=>t.id),['1','2','10']);
assert.deepEqual(natural.map(t=>t.id),['10','2','1']);
assert.equal(filterUploadTasks([{...tasks[0],file:{name:'file.txt',relativePath:'tree/sub/file.txt'}}],'all','','tree/sub').length,1);
console.log('PASS: upload status counts, status/provider/name/destination intersection, newest-first immutable view, unknown status and empty tasks.');

const report=uploadTaskReport({...tasks[2],sent:0,file:{name:'报告.zip',size:20,path:'never-source-path'},accountName:'never-account',secret:'never-secret',message:'HTTP=403',lastAttempt:'fixture-time'},'fixture','蓝奏云','失败');
assert.match(report,/HTTP=403/);assert.match(report,/fixture-time/);assert.doesNotMatch(report,/never-/);assert.match(report,/0 \/ 20 字节/);

assert.notEqual(uploadSourceKey({path:'C:/same.txt'}),uploadSourceKey({path:'C:/same.txt',relativePath:'tree/same.txt'}));assert.equal(uploadSourceKey({path:'C:/same.txt'}),uploadSourceKey({path:'c:\\SAME.txt'}));

const groupTask=(id,extra={})=>({id,batchId:'batch1',accountId:'a',kind:'quark',folder:'0',folderDisplay:'根目录',sent:10,status:'failed',file:{name:'file.txt',relativePath:'tree/file.txt',size:10},...extra});
const treeRows=Object.freeze([
 Object.freeze(groupTask('file1')),
 Object.freeze(groupTask('dir',{status:'success',file:{name:'tree',relativePath:'tree',directory:true,size:0}})),
 Object.freeze(groupTask('empty',{status:'paused',file:{name:'empty.txt',relativePath:'tree/empty.txt',size:0}})),
]);
const g=uploadGroups(treeRows)[0];assert.equal(g.files,2);assert.equal(g.directories,1);assert.equal(g.failed,1);assert.equal(g.paused,1);assert.equal(g.success,1);assert.equal(g.percent,99);assert.equal(g.complete,false);assert.equal(g.totalBytes,10);assert.equal(g.sentBytes,10);
assert.equal(uploadGroups(treeRows.map(t=>({...t,status:'success'})))[0].percent,100);
assert.equal(uploadGroups([groupTask('empty-dir',{status:'queued',file:{name:'empty',relativePath:'empty',directory:true,size:0}})])[0].percent,0);
assert.equal(uploadGroups([groupTask('a'),groupTask('b',{accountId:'b'}),groupTask('c',{batchId:'batch2'}),groupTask('d',{folder:'other'}),groupTask('e',{file:{name:'x',relativePath:'different/x',size:1}})]).length,5);
assert.notEqual(uploadGroupKey(groupTask('legacy1',{batchId:''})),uploadGroupKey(groupTask('legacy2',{batchId:''})));
assert.equal(treeRows[0].status,'failed');
console.log('PASS: upload groups isolate batch/account/folder/root, retain legacy identity, count empty directories/zero-byte files and never show complete for failed full-byte tasks.');
