import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import {pathToFileURL} from 'node:url';
import {createRequire} from 'node:module';
import assert from 'node:assert/strict';
const root=path.resolve(import.meta.dirname,'../frontend'),require=createRequire(path.join(root,'package.json'));
const {parse,compileScript}=require('@vue/compiler-sfc'),ts=require('typescript');
const vueURL=pathToFileURL(path.join(root,'node_modules/vue/dist/vue.runtime.esm-bundler.js')).href;
const {reactive,nextTick}=await import(vueURL);
const temp=await fs.mkdtemp(path.join(os.tmpdir(),'multipan-batch-revoke-'));
const url=n=>pathToFileURL(path.join(temp,n)).href;
try{
 await fs.writeFile(path.join(temp,'shareAccounts.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/shareAccounts.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
 await fs.writeFile(path.join(temp,'pageSearch.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/pageSearch.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
 await fs.writeFile(path.join(temp,'spreadsheetCell.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/spreadsheetCell.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
 await fs.writeFile(path.join(temp,'hooks.mjs'),`export {defineComponent,computed,ref,watch,nextTick} from ${JSON.stringify(vueURL)};export const cleanup=[];export function onMounted(){};export function onUnmounted(fn){cleanup.push(fn)}`);
 await fs.writeFile(path.join(temp,'api.mjs'),`export const calls={revoke:[],check:[],lists:[],preflight:[],resumePreflight:[]};export const control={state:'confirmed',failAt:0,badAt:0,stopAt:0,hold:null,entries:[]};export async function ValidateManagedCloudShareRevokeBatch(handles){calls.preflight.push(handles);if(control.holdPreflight)await control.holdPreflight;if(control.failPreflight)throw Error('stale page')}export async function ValidateManagedCloudShareRevokeResume(handles){calls.resumePreflight.push(handles);if(control.failPreflight)throw Error('stale identity')}export async function RevokeManagedCloudShare(handle){calls.revoke.push(handle);const n=calls.revoke.length;if(control.hold)await control.hold;if(n===control.failAt)throw Error('connection lost');if(n===control.badAt)return null;const state=n===control.stopAt?control.state:'confirmed';return {id:'receipt-'+handle,name:handle,state,message:state,canCheck:state==='unknown'}}export async function CheckManagedCloudShareRevoke(id){calls.check.push(id);return {id,name:id,state:'confirmed',message:'checked only',canCheck:false}}export async function ManagedCloudShares(id,page){calls.lists.push({id,page});return {items:control.entries,page,hasMore:false}}export async function ManagedCloudShareRevokes(){return []}`);
 async function load(name){let script=compileScript(parse(await fs.readFile(path.join(root,'src',name+'.vue'),'utf8')).descriptor,{id:'batch-revoke-check'}).content;
 script=script.replace(/from ['"]vue['"]/g,'from '+JSON.stringify(url('hooks.mjs'))).replace(/from ['"]\.\.\/bindings\/multipan\/internal\/desktop\/desktopservice['"]/g,'from '+JSON.stringify(url('api.mjs'))).replace(/import \{trapDialogFocus\} from ['"].*?['"]/,'const trapDialogFocus=()=>{}').replace(/import \{managedShareText\} from ['"].*?['"]/,'const managedShareText=()=>""').replace(/import ShareSettingsDialog from ['"].*?['"]/,'const ShareSettingsDialog={}').replace(/import BatchShareRevokeDialog from ['"].*?['"]/,'const BatchShareRevokeDialog={}').replace(/import BatchManagedShareReadDialog from ['"].*?['"]/,'const BatchManagedShareReadDialog={}');
  script=script.replace(/from ['"]\.\/shareAccounts['"]/g,'from '+JSON.stringify(url('shareAccounts.mjs')));
  script=script.replace(/import \{Browser\} from ['"]@wailsio\/runtime['"]/,'const Browser={OpenURL:async()=>{}}');
  script=script.replace(/from ['"]\.\/pageSearch['"]/g,'from '+JSON.stringify(url('pageSearch.mjs')));
  script=script.replace(/from ['"]\.\/spreadsheetCell['"]/g,'from '+JSON.stringify(url('spreadsheetCell.mjs')));
  script=script.replace(/import BatchShareExpiryDialog from ['"].*?['"]/,'const BatchShareExpiryDialog={}');
  script=script.replace(/import (AccountSearch|CloudShareInventory)(?:,\s*\{[^}]*\})? from ['"].*?['"]/g,'const $1={}');
  script=script.replace(/import BatchOperationReport from ['"].*?['"]/,'const BatchOperationReport={}');
  await fs.writeFile(path.join(temp,name+'.mjs'),ts.transpileModule(script,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);return (await import(url(name+'.mjs'))).default;
 }
 const dialog=await load('BatchShareRevokeDialog'),{calls,control}=await import(url('api.mjs')),{cleanup}=await import(url('hooks.mjs'));
 const entries=(prefix,count=3)=>Array.from({length:count},(_,i)=>({id:prefix+'-'+i,name:'测试分享'+i,canRevoke:true,status:'正常',expiry:'永久',created:'',canEdit:false}));
 function reset(){calls.revoke.length=0;calls.check.length=0;Object.assign(control,{state:'confirmed',failAt:0,badAt:0,stopAt:0,hold:null})}
 function setup(rows,preflight=false){const props=reactive({accountName:'测试账号',targets:rows,blocked:false,preflight}),events=[];const panel=dialog.setup(props,{expose(){},emit:(...args)=>events.push(args)});return {panel,props,events,dispose:cleanup.at(-1)}}
 function confirm(panel){panel.ack.value=true;panel.confirmText.value='撤销分享'}
 for(const kind of ['quark','pan123','baidu','guangya']){reset();const {panel,events}=setup(entries(kind));await panel.run();assert.equal(calls.revoke.length,0);panel.ack.value=true;panel.confirmText.value='撤销';await panel.run();assert.equal(calls.revoke.length,0);confirm(panel);await panel.run();assert.deepEqual(calls.revoke,entries(kind).map(r=>r.id));assert.equal(panel.confirmed.value,3);assert.equal(events.filter(e=>e[0]==='result').length,3);await panel.run();assert.equal(calls.revoke.length,3,'batch replayed');}
 for(const mode of ['unknown','not_sent','exception','null']){reset();if(mode==='exception')control.failAt=2;else if(mode==='null')control.badAt=1;else{control.stopAt=1;control.state=mode}const {panel}=setup(entries(mode));confirm(panel);await panel.run();assert.equal(calls.revoke.length,mode==='exception'?2:1);assert.equal(panel.rows.value.at(-1).state,'skipped');assert.equal(panel.started.value,true);const writes=calls.revoke.length;if(mode==='unknown'){await panel.check(panel.rows.value[0]);assert.deepEqual(calls.check,['receipt-unknown-0']);assert.equal(panel.rows.value[0].state,'confirmed')}await panel.run();assert.equal(calls.revoke.length,writes);}
 for(const mode of ['stop','blocked','unmount']){reset();let release;control.hold=new Promise(resolve=>release=resolve);const {panel,props,events,dispose}=setup(entries(mode));confirm(panel);const pending=panel.run();await panel.run();assert.equal(calls.revoke.length,1);if(mode==='stop')panel.stop();else if(mode==='blocked')props.blocked=true;else dispose();release();control.hold=null;await pending;assert.equal(calls.revoke.length,1,'continued after stop/blocked/unmount');if(mode!=='unmount')assert.equal(panel.rows.value[1].state,'skipped');else assert.equal(events.length,0,'late result emitted after unmount');}
 for(const invalid of [[],entries('limit',51),[{...entries('bad')[0],canRevoke:false}],[entries('duplicate')[0],entries('duplicate')[0]]]){reset();const {panel}=setup(invalid);confirm(panel);await panel.run();assert.equal(calls.revoke.length,0)}
 for(const mode of ['success','stale','stop','unmount']){
  reset()
  const {panel,dispose}=setup(entries(mode),true)
  let release
  control.holdPreflight=new Promise(resolve=>release=resolve)
  control.failPreflight=mode==='stale'
  confirm(panel)
  const task=panel.run()
  assert.deepEqual(calls.preflight.at(-1),entries(mode).map(row=>row.id))
  assert.equal(calls.revoke.length,0)
  if(mode==='stop')panel.stop()
  if(mode==='unmount')dispose()
  release();await task
  assert.equal(calls.revoke.length,mode==='success'?3:0)
  if(mode==='stale')assert.match(panel.error.value,/未提交撤销/)
 }
 {
  reset()
  const rows=entries('resume',4)
  const {panel,events}=setup(rows)
  control.stopAt=2
  control.state='unknown'
  confirm(panel)
  await panel.run()
  assert.deepEqual(panel.rows.value.map(row=>row.state),['confirmed','unknown','skipped','skipped'])
  assert.equal(panel.canResume.value,false)
  panel.prepareResume()
  assert.equal(panel.rows.value[2].state,'skipped')
  await panel.check(panel.rows.value[1])
  assert.equal(panel.canResume.value,true)
  assert.deepEqual(calls.revoke,rows.slice(0,2).map(row=>row.id))
  panel.prepareResume()
  assert.equal(panel.confirmText.value,'')
  assert.equal(panel.ack.value,false)
  await panel.run()
  assert.equal(calls.revoke.length,2)
  confirm(panel)
  control.failPreflight=true
  await panel.run()
  assert.deepEqual(calls.resumePreflight.at(-1),rows.slice(2).map(row=>row.id))
  assert.equal(calls.revoke.length,2)
  control.failPreflight=false
  panel.prepareResume()
  confirm(panel)
  await panel.run()
  assert.deepEqual(calls.revoke,rows.map(row=>row.id))
  assert.equal(panel.confirmed.value,4)
  assert.equal(panel.canResume.value,false)
  panel.close()
  assert.deepEqual(events.at(-1),['close',true])
 }
 reset();const list=await load('CloudShareList'),props=reactive({accounts:[{id:'q',kind:'quark',name:'工作'},{id:'q2',kind:'quark',name:'工作'},{id:'b',kind:'baidu',name:'工作'},{id:'p',kind:'pan123',name:''}],blocked:false,refreshKey:'0'}),events=[];
 const panel=list.setup(props,{expose(){},emit:(...args)=>events.push(args)});control.entries=entries('page',60);await panel.load();panel.selectBatch();assert.equal(panel.batchSelection.value.length,50);panel.prepareBatch();assert.equal(panel.batchTargets.value.length,50);assert.equal(panel.locked.value,true);assert.equal(calls.revoke.length,0,'selection or confirmation opening sent revoke');panel.batchResult('page-0',{id:'receipt',state:'confirmed',message:'confirmed'});assert.equal(panel.items.value.some(r=>r.id==='page-0'),false);await panel.closeBatch();assert.equal(panel.batchTargets.value,null);assert.equal(panel.batchSelection.value.length,0);
 const queued=entries('queued',2)
 panel.batchReadQueue.value=queued.map((share,i)=>({page:i+1,share}))
 panel.prepareBatch(true)
 assert.equal(panel.batchAcrossPages.value,true)
 assert.deepEqual(panel.batchTargets.value.map(share=>share.id),queued.map(share=>share.id))
 await panel.closeBatch(false)
 assert.equal(panel.batchReadQueue.value.length,2)
 const listReadsBeforeCancel=calls.lists.length;
 const editable={...panel.items.value[1],canEdit:true};
 panel.prepareEdit(editable);assert.equal(panel.editTarget.value.id,editable.id);
 await panel.closeEdit(false);
 assert.deepEqual(panel.batchReadQueue.value.map(row=>row.share.id),queued.map(share=>share.id));
 assert.equal(calls.lists.length,listReadsBeforeCancel);
 panel.toggleBatch(editable.id,true);panel.prepareBatch();assert.equal(panel.batchAcrossPages.value,false);
 await panel.closeBatch(false);
 assert.equal(panel.batchReadQueue.value.length,2);
 assert.equal(panel.batchSelection.value.includes(editable.id),true);
 panel.batchSelection.value=[];
 panel.batchReadQueue.value[1].page=11
 panel.prepareBatch(true)
 assert.equal(panel.batchTargets.value,null)
 panel.toggleBatch('page-1',true);
 assert.equal(panel.batchSelection.value.length,1);
 await panel.load();
 assert.equal(panel.batchSelection.value.length,0,'old selection retained after refresh');
 panel.toggleBatch('page-1',true);props.blocked=true;panel.prepareBatch();assert.equal(panel.batchTargets.value,null);props.blocked=false;panel.prepareBatch();assert.ok(panel.batchTargets.value);props.accountId='unused';panel.accountId.value='b';await nextTick();await Promise.resolve();assert.equal(panel.batchTargets.value,null,'old-account dialog survived scope switch');assert.equal(panel.batchSelection.value.length,0);
 for(const fn of cleanup)fn();assert.equal(events.at(-1)[1],false);
 assert.equal(panel.accountLabel(props.accounts[0]),'夸克网盘 · 工作（1）');
 assert.equal(panel.accountLabel(props.accounts[1]),'夸克网盘 · 工作（2）');
 assert.equal(panel.selectedAccountName.value,'百度网盘 · 工作');
 assert.equal(panel.accountLabel(props.accounts[3]),'123 云盘 · 未命名账号');
 console.log('Batch share revoke checks passed: four providers, exact confirmation, 50-item selection, whole-selection eligibility, first unknown/error stop, manual stop, duplicate guards, read-only receipt checks, remaining-only resumption, source invalidation and unmount protection.');
}finally{
 const absolute=path.resolve(temp),base=path.resolve(os.tmpdir());if(path.dirname(absolute)!==base||!path.basename(absolute).startsWith('multipan-batch-revoke-'))throw Error('Refusing cleanup outside owned test directory');await fs.rm(absolute,{recursive:true,force:true});
}
