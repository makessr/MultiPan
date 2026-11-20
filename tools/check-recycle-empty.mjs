import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import {pathToFileURL} from 'node:url';
import {createRequire} from 'node:module';
import assert from 'node:assert/strict';
const root=path.resolve(import.meta.dirname,'../frontend'),require=createRequire(path.join(root,'package.json'));
const {parse,compileScript}=require('@vue/compiler-sfc'),ts=require('typescript');
const vueURL=pathToFileURL(path.join(root,'node_modules/vue/dist/vue.runtime.esm-bundler.js')).href;
const temp=await fs.mkdtemp(path.join(os.tmpdir(),'multipan-recycle-empty-'));
const url=n=>pathToFileURL(path.join(temp,n)).href;
try{
 await fs.writeFile(path.join(temp,'hooks.mjs'),`export {defineComponent,computed,ref,watch} from ${JSON.stringify(vueURL)};export const cleanup=[];export function onMounted(){};export function onUnmounted(fn){cleanup.push(fn)}`);
 await fs.writeFile(path.join(temp,'api.mjs'),`export const calls={previews:[],prepared:[],writes:[],copies:[]};export const control={};
 export async function PreviewCloudRecycleEmpty(id){calls.previews.push(id);if(control.holdPreview)await control.holdPreview;if(control.failPreview)throw Error('incomplete');if(control.badPreview)return null;const items=calls.writes.length?(control.remaining??[]):control.previewItems??[{id:'one',name:'one',kind:'file'},{id:'two',name:'two',kind:'folder'}];return {token:'preview-token',total:items.length,items,expires:'2099-01-01'}}
 export async function PrepareCloudRecycleEmpty(token,confirmation){calls.prepared.push({token,confirmation});if(control.holdPrepare)await control.holdPrepare;if(control.failPrepare)throw Error('snapshot changed');if(control.badPrepare)return null;if(control.serverClear)return [{name:'清空整个回收站',handle:'server'}];return [{name:'one',handle:'h1'},{name:'two',handle:'h2'}]}
 export async function CopyDownloadReport(text){calls.copies.push(text);if(control.failCopy)throw Error('Clipboard unavailable')}
 export async function PurgeCloudRecycle(handle,confirmation){calls.writes.push({handle,confirmation});if(control.holdWrite)await control.holdWrite;if(control.throwWrite)throw Error('connection lost');if(control.nullWrite)return null;return {state:control.state??'confirmed',message:'checked'}}`);
 let script=compileScript(parse(await fs.readFile(path.join(root,'src/RecycleEmptyDialog.vue'),'utf8')).descriptor,{id:'empty-check'}).content;
 script=script.replace(/from ['"]vue['"]/g,'from '+JSON.stringify(url('hooks.mjs'))).replace(/from ['"]\.\.\/bindings\/multipan\/internal\/desktop\/desktopservice['"]/g,'from '+JSON.stringify(url('api.mjs'))).replace(/import \{trapDialogFocus\} from ['"].*?['"]/,'const trapDialogFocus=()=>{}');
 script=script.replace(/import BatchOperationReport from ['"].*?['"]/,'const BatchOperationReport={}');
 const cellSource=await fs.readFile(path.join(root,'src/spreadsheetCell.ts'),'utf8');
 await fs.writeFile(path.join(temp,'spreadsheetCell.mjs'),ts.transpileModule(cellSource,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
 script=script.replace(/from ['"]\.\/spreadsheetCell['"]/g,'from '+JSON.stringify(url('spreadsheetCell.mjs')));
 await fs.writeFile(path.join(temp,'dialog.mjs'),ts.transpileModule(script,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
 const dialog=(await import(url('dialog.mjs'))).default,{calls,control}=await import(url('api.mjs')),{cleanup}=await import(url('hooks.mjs'));
 function reset(){for(const a of Object.values(calls))a.length=0;for(const k of Object.keys(control))delete control[k]}
 function setup(serverClear=false){const events=[];const p=dialog.setup({accountId:'test-account',accountName:'测试盘 · 测试账号',serverClear},{expose(){},emit:(...e)=>events.push(e)});return {p,events,dispose:cleanup.at(-1)}}
 function confirm(p){p.agreed.value=true;p.confirmation.value='清空回收站'}
 reset();let {p}=setup();await p.load();assert.deepEqual(calls.previews,['test-account']);assert.equal(calls.writes.length,0);await p.start();assert.equal(calls.prepared.length,0);p.agreed.value=true;p.confirmation.value='永久删除';await p.start();assert.equal(calls.prepared.length,0);confirm(p);await p.start();assert.deepEqual(calls.prepared,[{token:'preview-token',confirmation:'清空回收站'}]);assert.deepEqual(calls.writes,[{handle:'h1',confirmation:'永久删除'},{handle:'h2',confirmation:'永久删除'}]);assert.match(p.status.value,/已清空/);await p.start();assert.equal(calls.writes.length,2);
 for(const mode of ['failPreview','badPreview','failPrepare','badPrepare','throwWrite','nullWrite','unknown','not_sent','remaining']){
  reset();({p}=setup());if(mode.includes('Preview'))control[mode]=true;await p.load();confirm(p);if(mode==='unknown'||mode==='not_sent')control.state=mode;else if(mode==='remaining')control.remaining=[{id:'new',name:'new',kind:'file'}];else control[mode]=true;await p.start();const expected=mode.includes('Preview')||mode.includes('Prepare')?0:mode==='remaining'?2:1;assert.equal(calls.writes.length,expected,mode);if(mode==='remaining')assert.match(p.status.value,/仍有 1 项/);assert.equal(calls.writes.some(r=>r.handle==='new'),false);await p.start();assert.equal(calls.writes.length,expected,'replayed '+mode);
 }
 reset();({p}=setup());await p.load();confirm(p);control.throwWrite=true;await p.start();
 assert.deepEqual(p.results.value.map(row=>row.state),['unknown','skipped']);
 assert.equal(calls.writes.length,1);
 reset();({p}=setup(true));control.serverClear=true;await p.load();confirm(p);await p.start();
 assert.deepEqual(calls.writes,[{handle:'server',confirmation:'清空回收站'}]);
 assert.deepEqual(p.results.value.map(row=>[row.name,row.state]),[['清空整个回收站','confirmed']]);
 for(const stage of ['Prepare','Write'])for(const action of ['stop','unmount']){
  reset();const s=setup();p=s.p;await p.load();confirm(p);let release;control['hold'+stage]=new Promise(r=>release=r);const pending=p.start();await Promise.resolve();await p.start();if(action==='stop')p.stop.value=true;else s.dispose();release();await pending;assert.equal(calls.writes.length,stage==='Prepare'?0:1,stage+'/'+action);if(action==='unmount')assert.equal(s.events.length,0);
 }
 reset();const s=setup();let release;control.holdPreview=new Promise(r=>release=r);const pending=s.p.load();s.dispose();release();await pending;assert.equal(s.p.preview.value,null,'late preview after unmount');assert.equal(calls.writes.length,0);
 reset();({p}=setup());await p.copyPreview();assert.equal(calls.copies.length,0);
 await p.load();
 p.preview.value.items[0].name='=1+1\tfile';
 p.preview.value.items[0].originalPath='\n@name';
 p.preview.value.items[0].size=0;
 await p.copyPreview();
 assert.equal(calls.prepared.length,0);
 assert.equal(calls.writes.length,0);
 assert.equal(calls.copies.length,1);
 assert.match(calls.copies[0],/'=1\+1 file\t文件\t0\t'@name/);
 assert.doesNotMatch(calls.copies[0],/preview-token|test-account|\bid\b/i);
 assert.match(p.copyStatus.value,/已复制 2/);
 control.failCopy=true;await p.copyPreview();assert.match(p.copyStatus.value,/复制预览失败/);
 await p.load();assert.equal(p.copyStatus.value,'');
 reset();({p}=setup());control.failPreview=true;await p.load();await p.copyPreview();assert.equal(calls.copies.length,0);
 reset();({p}=setup());
 control.previewItems=Array.from({length:53},(_,i)=>({id:String(i),name:'项目'+i,kind:'file',originalPath:i===51?'/隐藏路径':'/公共目录'}));
 await p.load();
 assert.equal(p.pageCount.value,3);
 assert.equal(p.visibleItems.value.length,25);
 p.stepPage(1);p.stepPage(1);p.stepPage(1);
 assert.deepEqual(p.visibleItems.value.map(row=>row.id),['50','51','52']);
 p.query.value=' 隐藏路径 ';
 assert.equal(p.page.value,1);
 assert.deepEqual(p.visibleItems.value.map(row=>row.id),['51']);
 await p.copyPreview();
 assert.equal(calls.copies[0].split('\r\n').length,55,'filter changed copied scope');
 assert.equal(calls.prepared.length,0);
 assert.equal(calls.writes.length,0);
 p.query.value='missing';
 assert.equal(p.visibleItems.value.length,0);
 confirm(p);
 assert.equal(p.canStart.value,true,'filter unexpectedly changed the clear scope');
 await p.load();
 assert.equal(p.query.value,'');
 assert.equal(p.page.value,1);
 assert.equal(p.canStart.value,false);
 console.log('Recycle empty actual Vue checks passed: preview search and pagination, safe preview copy, read-only full preview, exact confirmation, no replay, first failure/unknown stop, stop/unmount during preflight or write, final empty/read-time remainder, no late effects.');
}finally{
 const absolute=path.resolve(temp),base=path.resolve(os.tmpdir());if(path.dirname(absolute)!==base||!path.basename(absolute).startsWith('multipan-recycle-empty-'))throw Error('Refusing cleanup outside owned test directory');await fs.rm(absolute,{recursive:true,force:true});
}
