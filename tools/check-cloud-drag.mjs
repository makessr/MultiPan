// Isolated component checks: execute the actual Vue script with synthetic accounts.
// No native window, credentials, network requests or real cloud mutations.
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
const root=path.resolve(import.meta.dirname,'../frontend');
const fromRoot=p=>pathToFileURL(path.join(root,p)).href;
const {parse,compileScript}=await import(fromRoot('node_modules/@vue/compiler-sfc/dist/compiler-sfc.esm-browser.js')).catch(async()=>{
 const {createRequire}=await import('node:module');return createRequire(fromRoot('package.json'))('@vue/compiler-sfc');
});
const {default:ts}=await import(fromRoot('node_modules/typescript/lib/typescript.js'));
const vueURL=fromRoot('node_modules/vue/dist/vue.runtime.esm-bundler.js');
const {reactive,nextTick}=await import(vueURL);
const dir=await fs.mkdtemp(path.join(os.tmpdir(),'multipan-cloud-drag-'));
const fileURL=name=>pathToFileURL(path.join(dir,name)).href;
await fs.writeFile(path.join(dir,'version.mjs'),await fs.readFile(path.join(root,'src/version.ts'),'utf8'));
const flowCode=ts.transpileModule(await fs.readFile(path.join(root,'src/useUploadReview.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText.replace(/from ['"]vue['"]/g,()=> 'from '+JSON.stringify(vueURL)).replace(/from ['"]\.\.\/bindings\/multipan\/internal\/desktop\/desktopservice['"]/g,()=> 'from '+JSON.stringify(fileURL('api.mjs')));
await fs.writeFile(path.join(dir,'useUploadReview.mjs'),flowCode);
await fs.writeFile(path.join(dir,'uploadTasks.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/uploadTasks.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
await fs.writeFile(path.join(dir,'uploadFolderMatch.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/uploadFolderMatch.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
await fs.writeFile(path.join(dir,'transferRecovery.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/transferRecovery.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
await fs.writeFile(path.join(dir,'shareText.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/shareText.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
await fs.writeFile(path.join(dir,'spreadsheetCell.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/spreadsheetCell.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
await fs.writeFile(path.join(dir,'shareAccounts.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/shareAccounts.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
await fs.writeFile(path.join(dir,'pan123ShareTraffic.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/pan123ShareTraffic.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
await fs.writeFile(path.join(dir,'pageSearch.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/pageSearch.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
await fs.writeFile(path.join(dir,'downloadView.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/downloadView.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
await fs.writeFile(path.join(dir,'downloadResults.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/downloadResults.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
await fs.writeFile(path.join(dir,'browseHistory.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/browseHistory.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
await fs.writeFile(path.join(dir,'fileList.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/fileList.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
await fs.writeFile(path.join(dir,'fileShortcuts.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/fileShortcuts.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
await fs.writeFile(path.join(dir,'downloadMeter.mjs'),ts.transpileModule(await fs.readFile(path.join(root,'src/downloadMeter.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
await fs.writeFile(path.join(dir,'hooks.mjs'),`export {defineComponent,computed,ref,shallowRef,watch,nextTick} from ${JSON.stringify(vueURL)}; export const cleanup=[]; export function onMounted(fn){fn()} export function onUnmounted(fn){cleanup.push(fn)}`);
await fs.writeFile(path.join(dir,'events.mjs'),`const listeners=new Map();export const Browser={async OpenURL(url){openedURLs.push(url)}};export const openedURLs=[];export const Events={On(name,fn){listeners.set(name,fn);return ()=>listeners.delete(name)}};export function emit(data){listeners.get('multipan:files-dropped')?.({data})} export function progress(data){listeners.get('multipan:download-progress')?.({data})} export function batch(data){listeners.get('multipan:batch-download')?.({data})} export function folder(data){listeners.get('multipan:folder-download')?.({data})} export function count(){return listeners.size}`);
await fs.writeFile(path.join(dir,'api.mjs'),`
export const calls={moves:[],folderMoves:[],uploads:[],downloads:[],cancelDownloads:0,defaults:0};
export const controls={failIds:[],pauseNext:false,release:null,active:0,maxActive:0,downloadMode:'success',holdDownload:false,releaseDownload:null};
export async function DownloadCloudFile(input){calls.downloads.push(input);if(controls.holdDownload)await new Promise(resolve=>{controls.releaseDownload=resolve});if(controls.downloadMode==='failed')throw Error('模拟下载中断');return controls.downloadMode==='cancelled'?{cancelled:true}:{path:'C:/fixture/saved.txt',bytes:12,cancelled:false,warning:controls.downloadMode==='warning'?'下载完成，未验证原件完整性':'',verification:controls.downloadMode==='warning'?'unverified':'verified',details:controls.downloadMode==='warning'?'接口参考 MD5 不一致：fixture-digest':'原件 SHA256 一致'}}
export async function DownloadCloudFiles(inputs){calls.batches??=[];calls.batches.push(inputs);if(controls.holdDownload)await new Promise(resolve=>{controls.releaseDownload=resolve});if(controls.downloadMode==='failed')throw Error('模拟目录不可用');return {cancelled:controls.downloadMode==='cancelled',items:inputs.map((f,i)=>({name:f.name,status:controls.downloadMode==='cancelled'?'skipped':i===0?'saved':'failed',message:i?'模拟失败':'',result:{path:i?'':'C:/fixture/'+f.name,verification:'verified',details:'原件SHA256'}}))}}
export async function DownloadCloudFolder(input){calls.folderDownloads??=[];calls.folderDownloads.push(input);if(controls.holdDownload)await new Promise(resolve=>{controls.releaseDownload=resolve});if(controls.downloadMode==='failed')throw Error('模拟根文件夹已存在');return controls.downloadMode==='cancelled'?{cancelled:true,items:[],directory:''}:{retryId:'fixture-retry',cancelled:false,directory:'C:/fixture/folder',items:controls.downloadMode==='empty'?[]:controls.downloadMode==='all-failed'?[{name:'failed.txt',status:'failed',message:'模拟DNS失败',result:{path:''}}]:[{name:'sub/file.txt',status:'saved',message:'',result:{path:'C:/fixture/folder/sub/file.txt',verification:'unverified'}},{name:'failed.txt',status:'failed',message:'模拟失败',result:{path:''}}]}}

export async function RetryCloudFolder(input,token){calls.folderRetries??=[];calls.folderRetries.push({input,token});if(controls.holdDownload)await new Promise(resolve=>{controls.releaseDownload=resolve});if(controls.retryFail)throw Error('模拟重试错误');return {retryId:controls.retryCancel?'fixture-retry':'',cancelled:!!controls.retryCancel,directory:'C:/fixture/folder',items:[{name:'sub/file.txt',status:'saved',message:'',result:{path:'C:/fixture/folder/sub/file.txt',verification:'unverified'}},{name:'failed.txt',status:controls.retryCancel?'skipped':'saved',message:'',result:controls.retryCancel?{path:''}:{path:'C:/fixture/folder/failed.txt',verification:'verified'}}]}}
export async function OpenDownloadFolder(path){calls.openFolders??=[];calls.openFolders.push(path);if(controls.openFolderFail)throw Error('模拟目录已删除')}
export async function CopyDownloadReport(report){calls.reports??=[];if(controls.copyFail)throw Error('模拟剪贴板失败');calls.reports.push(report)}
export async function CancelDownload(){calls.cancelDownloads++;controls.downloadMode='cancelled';controls.releaseDownload?.()}
export async function ListCloudFiles(accountId,parent){calls.lists??=[];calls.lists.push({accountId,parent});if(controls.holdListParent===parent)await new Promise(resolve=>{controls.releaseList=resolve});if(controls.failListParent===parent)throw Error('模拟目录已变化');if(controls.lanzouSharePages?.[parent]&&accountId.startsWith('blue-share-'))return controls.lanzouSharePages[parent];return {currentId:parent,breadcrumbs:parent==='0'?[{id:'0',name:'根目录'}]:[{id:'0',name:'根目录'},{id:parent,name:'资料'}],entries:[{id:'dest',name:'资料',folder:true,size:-1},{id:'file',name:'测试文件.txt',folder:false,size:12}].filter(f=>parent==='0'||!f.folder)}}
export async function MoveCloudFile(input){calls.moves.push(input);controls.active++;controls.maxActive=Math.max(controls.maxActive,controls.active);try{if(controls.pauseNext){controls.pauseNext=false;await new Promise(resolve=>{controls.release=resolve})}if(controls.failIds.includes(input.entryId))throw Error('模拟结果未确认，请刷新核对')}finally{controls.active--}}
export async function StartCloudSearch(input){calls.searches??=[];calls.searches.push(JSON.parse(JSON.stringify(input)));const id='search-'+calls.searches.length;if(controls.holdSearchStart)await new Promise(resolve=>controls.releaseSearchStart=resolve);return {id,state:'completed',message:'模拟扫描',scanned:60,directories:3,current:'root',items:[],issues:[],...structuredClone(controls.searchResult??{})}}
export async function CloudSearchStatus(id){calls.searchPolls??=[];calls.searchPolls.push(id);if(controls.holdSearchPoll)await new Promise(resolve=>controls.releaseSearchPoll=resolve);if(controls.failSearchPoll)throw Error('模拟搜索读取失败');return {id,state:'completed',message:'模拟扫描',scanned:60,directories:3,current:'root',items:[],issues:[],...structuredClone(controls.searchResult??{})}}
export async function CancelCloudSearch(id){calls.searchCancels??=[];calls.searchCancels.push(id)}
export async function CloudCopyHistory(id){if(controls.holdCopyHistory)await new Promise(resolve=>controls.releaseCopyHistory=resolve);if(controls.failCopyHistory)throw Error('模拟复制记录读取失败');return (controls.copyHistoryRows??[]).filter(r=>r.accountId===id)}
export async function CheckCloudCopy(id){calls.copyChecks??=[];calls.copyChecks.push(id);if(controls.holdCopyCheck)await new Promise(resolve=>controls.releaseCopyCheck=resolve);if(controls.failCopyCheck)throw Error('模拟核对失败');const r=controls.copyHistoryRows.find(r=>r.id===id);Object.assign(r,{state:'confirmed',canCheck:false,message:'仅读核对成功'});return r}
export async function ClearCloudCopyHistory(id){if(controls.failCopyClear)throw Error('模拟清理保存失败');const old=controls.copyHistoryRows.length;controls.copyHistoryRows=controls.copyHistoryRows.filter(r=>r.accountId!==id||!['confirmed','not_sent'].includes(r.state));return old-controls.copyHistoryRows.length}
export async function CopyCloudEntry(entry){calls.copies??=[];calls.copies.push(entry);if(controls.holdCopy)await new Promise(resolve=>controls.releaseCopy=resolve);if(controls.failCopyIds?.includes(entry.input.entryId))throw Error('模拟复制待确认');return {}}
export async function CancelCloudCopy(accountId,entryId,destinationId){(calls.copyStops??=[]).push({accountId,entryId,destinationId});return !controls.copyStopNotReady}
export async function CopyPan123FileToDirectory(entry){calls.directCopies??=[];calls.directCopies.push(entry);if(controls.failDirectCopy)throw Error('秒传待核对');return {}}
export async function CopyPan123FolderToDirectory(entry){(calls.directFolderCopies??=[]).push(entry);return {}}
export async function CopyGuangyaFolderToDirectory(entry){(calls.guangyaDirectFolders??=[]).push(entry);return {}}
export async function ValidateGuangyaDirectCopyBatch(entries){(calls.guangyaDirectPreflights??=[]).push(entries);if(controls.failGuangyaDirectPreflight)throw Error('模拟同名冲突')}
export async function ValidateLanzouCopyBatch(entries){(calls.lanzouCopyPreflights??=[]).push(entries);if(controls.failLanzouCopyPreflight)throw Error('模拟第二项大小未知')}
export async function LocateCloudCopyDestination(accountId,receiptId){(calls.copyLocations??=[]).push({accountId,receiptId});return {folderId:'/target'}}
export async function LocateCloudCopyResult(accountId,receiptId){(calls.copyResultLocations??=[]).push({accountId,receiptId});return {folderId:'copy-folder'}}
export async function ValidatePan123DirectCopyBatch(entries){calls.directCopyPreflights??=[];calls.directCopyPreflights.push(entries);if(controls.failDirectCopyPreflight)throw Error('已有同名项目')}
export async function ValidatePan123CopyBatch(entries){calls.pan123CopyPreflights??=[];calls.pan123CopyPreflights.push(entries);if(controls.failMovePreflight)throw Error('文件夹清单不完整')}
export async function ValidateGuangyaCopyBatch(entries){calls.guangyaCopyPreflights??=[];calls.guangyaCopyPreflights.push(entries);if(controls.failGuangyaCopyPreflight)throw Error('文件夹清单不完整')}
export async function PrepareCloudTrash(inputs){calls.trashPrepares??=[];calls.trashPrepares.push(structuredClone(inputs));if(controls.holdTrashPrepare)await new Promise(resolve=>controls.releaseTrashPrepare=resolve);if(controls.failTrashPrepare)throw Error('模拟预检查项目已变');if(controls.nullTrashPrepare)return null;return inputs.map(r=>({name:r.entry.name,folder:r.entry.folder,handle:r.entry.id}))}
export async function TrashCloudEntry(handle){calls.trashes??=[];calls.trashes.push(handle);if(controls.holdTrash)await new Promise(resolve=>controls.releaseTrash=resolve);if(controls.throwTrash)throw Error('模拟请求中断');return {state:controls.trashState??'confirmed',message:'模拟回收站结果'}}
export async function CloudTrashHistory(id){if(controls.holdTrashHistory)await new Promise(resolve=>controls.releaseTrashHistory=resolve);if(controls.failTrashHistory)throw Error('模拟记录读取失败');return (controls.trashRows??[]).filter(r=>r.accountId===id)}
export async function CheckCloudTrash(id){calls.trashChecks??=[];calls.trashChecks.push(id);const r=controls.trashRows.find(r=>r.id===id);Object.assign(r,{state:'confirmed',canCheck:false});return r}
export async function ClearCloudTrashHistory(id){if(controls.failTrashClear)throw Error('模拟记录保存失败');const old=controls.trashRows.length;controls.trashRows=controls.trashRows.filter(r=>r.accountId!==id||!['confirmed','not_sent'].includes(r.state));return old-controls.trashRows.length}
export async function MoveCloudFolder(input){calls.folderMoves.push(input);if(controls.failFolderIds?.includes(input.entryId))throw Error('模拟文件夹结果待确认')}
export async function ValidateCloudMoveBatch(entries){calls.movePreflights??=[];calls.movePreflights.push(entries);if(controls.holdMovePreflight)await new Promise(resolve=>controls.releaseMovePreflight=resolve);if(controls.failMovePreflight)throw Error('模拟整批冲突')}
export async function ListCloudFolders(accountId,parent){calls.folderLists??=[];calls.folderLists.push({accountId,parent});if(controls.holdFolderMatch){controls.holdFolderMatch=false;await new Promise(resolve=>controls.releaseFolderMatch=resolve)}return {currentId:parent,breadcrumbs:[{id:'/',name:'根目录'}],folders:controls.folderMatches?.[accountId]?.[parent]??[]}}
export async function CheckUploadPlan(files,targets){calls.uploadChecks??=[];calls.uploadChecks.push({files,targets});if(controls.holdReview)await new Promise(resolve=>controls.releaseReview=resolve);if(controls.reviewFail)throw Error('模拟检查失败');return {stamp:'fixture-stamp',items:files.flatMap((f,fileIndex)=>targets.map((t,targetIndex)=>({fileIndex,targetIndex,name:f.relativePath||f.name,kind:'quark',destination:t.folder,status:controls.reviewConflict?'conflict':'ready',message:'fixture check'})))}}
export async function EnqueueReviewedUploads(files,targets,skips,stamp){calls.reviewSubmissions??=[];calls.reviewSubmissions.push({files,targets,skips,stamp});if(controls.holdEnqueue)await new Promise(resolve=>controls.releaseEnqueue=resolve);await Enqueue(files,targets);return files.length*targets.length-skips.length}
export async function RetryFailedUploads(ids){calls.failedRetries??=[];calls.failedRetries.push([...ids]);if(controls.holdFailedRetry)await new Promise(resolve=>controls.releaseFailedRetry=resolve);return ids.map(id=>({id,name:id,applied:true,message:'重试请求已提交'}))}
export async function Enqueue(files,targets){calls.uploads.push({files:[...files],targets});return files.length}
export function SetDefaultFolder(){calls.defaults++;throw Error('Unexpected default-folder change')}
export async function DesktopPlatform(){return 'windows'}
export async function Snapshot(){return {accounts:controls.snapshotAccounts??[],tasks:(controls.snapshotTasks??[]).map(t=>({...t,file:{...t.file}})),storageError:''}}
export async function SaveAccount(input){calls.savedAccounts??=[];calls.savedAccounts.push(input);const output={...input,secret:undefined};controls.snapshotAccounts=(controls.snapshotAccounts??[]).map(a=>a.id===input.id?output:a);return output}
export async function LoginGuangya(id,name,folder){calls.accountLogins??=[];calls.accountLogins.push({id,name,folder});const a=controls.snapshotAccounts.find(a=>a.id===id);const updated={...a,id,name,folder};controls.snapshotAccounts=controls.snapshotAccounts.map(a=>a.id===id?updated:a);return updated}
export async function OpenUploadFolder(id){calls.uploadFolders??=[];calls.uploadFolders.push(id);if(controls.uploadFolderFail)throw Error('模拟源文件夹已删除')}
export async function Stop(id){calls.taskControls??=[];calls.taskControls.push({action:'stop',id})}
export async function Retry(id){calls.taskControls??=[];calls.taskControls.push({action:'retry',id});if(controls.holdTask)await new Promise(resolve=>{controls.releaseTask=resolve});if(controls.failTask===id)throw Error('模拟账号已移除')}
export async function PickUploadFolder(){return controls.folderFiles??[]}
export async function QueueCloudDownloads(inputs){calls.queuedDownloads??=[];calls.queuedDownloads.push(inputs);return inputs.length}
export async function QueueCloudFolder(input){calls.queuedFolder??=[];calls.queuedFolder.push(input);return 2}
export async function SaveTransferSettings(settings){calls.settings??=[];if(controls.settingsFail)throw Error('模拟保存失败');calls.settings.push({...settings})}
export async function BatchQueuedDownloads(ids,action){calls.queueBatches??=[];calls.queueBatches.push({ids:[...ids],action});if(controls.holdQueue)await new Promise(resolve=>controls.releaseQueue=resolve);return ids.map(id=>({id,name:id,applied:true,message:'fixture applied'}))}
export async function ControlAllQueuedDownloads(action){calls.allQueueActions??=[];calls.allQueueActions.push(action);return []}
export async function ClearQueuedDownloadPartials(ids){calls.partialClears??=[];calls.partialClears.push([...ids]);return ids.map(id=>({id,name:id,applied:true,message:'fixture cleaned'}))}
export async function ExportQueuedDownloadReport(ids,version){calls.reportExports??=[];calls.reportExports.push({ids:[...ids],version});return controls.cancelReportExport?'':'C:/fixture/report.txt'}
export async function ControlQueuedDownload(id,resume){calls.queueControls??=[];calls.queueControls.push({id,resume})}
export async function ClearSavedDownloadHistory(){calls.clearedHistory=true}
export async function OpenQueuedDownloadFolder(id){calls.openQueued=id}
export async function ValidateShareCreationBatch(inputs){calls.shareCreationPreflights??=[];calls.shareCreationPreflights.push(inputs);if(controls.holdCreationPreflight)await new Promise(resolve=>controls.releaseCreationPreflight=resolve);if(controls.failCreationPreflight)throw Error('所选项目已变化')}
export async function ShareBatchCloudEntry(input){calls.batchShares??=[];calls.batchShares.push({...input});if(controls.holdBatchShare)await new Promise(resolve=>controls.releaseBatchShare=resolve);if(controls.batchShareFail)throw Error('模拟连接丢失');return {id:'batch-'+calls.batchShares.length,state:controls.batchShareUnknown?'unknown':'ready',kind:'quark',name:input.name,url:controls.batchShareUnknown?'':'https://pan.quark.cn/s/fixture',password:input.password,days:input.days,message:'模拟结果',canCheck:true}}
export async function UpdateManagedCloudShare(handle,password,days){calls.shareSettings??=[];calls.shareSettings.push({handle,password,days});if(controls.holdSettings)await new Promise(r=>controls.releaseSettings=r);return {id:'settings-fixture',state:controls.settingsUnknown?'unknown':'ready',kind:'guangya',name:'fixture',url:controls.settingsUnknown?'':'https://www.guangyapan.com/s/fixture',password,days,message:'模拟设置结果',canCheck:true}}
export async function ActivateManagedBaiduShare(handle){calls.shareActivations??=[];calls.shareActivations.push(handle);return {id:'activation-fixture',state:handle==='frozen-a'?'ready':'unknown',kind:'baidu',name:'fixture',url:'',password:'',days:0,message:'模拟激活结果',canCheck:true}}
export async function RenameManagedPan123Share(handle,title){calls.shareRenames??=[];calls.shareRenames.push({handle,title});return {id:'rename-fixture',state:'unknown',kind:'pan123',name:'原分享',url:'',password:'',days:0,message:'模拟改名结果',canCheck:true}}
export async function SetManagedPan123ShareTrafficLimit(handle,limit){calls.shareTraffic??=[];calls.shareTraffic.push({handle,limit});return {id:'traffic-fixture',state:'unknown',kind:'pan123',name:'原分享',url:'',password:'',days:0,message:'模拟流量设置结果',canCheck:true}}
export async function SetManagedPan123ShareTrafficMode(handle,mode){calls.sharePermissions??=[];calls.sharePermissions.push({handle,mode});return {id:'permissions-fixture',state:handle==='traffic-a'?'ready':'unknown',kind:'pan123',name:'原分享',url:'',password:'',days:0,message:'模拟下载权限结果',canCheck:true}}
export async function ValidatePan123CloudShareTrafficBatch(handles,permission,limit){calls.trafficPreflights??=[];calls.trafficPreflights.push({handles,permission,limit});if(controls.failTrafficPreflight)throw Error('流量包余额不足')}
export async function ValidatePan123CloudShareDownloadSettingsBatch(handles,permission,limit){calls.combinedPreflights??=[];calls.combinedPreflights.push({handles,permission,limit});if(controls.failTrafficPreflight)throw Error('流量包余额不足')}
export async function SetManagedPan123ShareDownloadSettings(handle,mode,limit){calls.combinedSettings??=[];calls.combinedSettings.push({handle,mode,limit});return {state:handle==='traffic-a'?'ready':'unknown'}}
export async function ValidateBaiduCloudShareActivationBatch(handles){calls.activationPreflight??=[];calls.activationPreflight.push(handles);if(controls.failActivationPreflight)throw Error('原分享已变化')}
export async function ValidateManagedCloudShareExpiryBatch(handles,days){calls.expiryPreflight??=[];calls.expiryPreflight.push({handles,days});if(controls.holdExpiryPreflight)await new Promise(r=>controls.releaseExpiryPreflight=r);if(controls.failExpiryPreflight)throw Error('原期限已变化')}
export async function ValidateGuangyaCloudShareSettingsBatch(handles,password,days){calls.guangyaSettingsPreflight??=[];calls.guangyaSettingsPreflight.push({handles,password,days});if(controls.failGuangyaSettingsPreflight)throw Error('原分享已变化')}
export async function ShareCloudEntry(input){calls.shares??=[];calls.shares.push({...input});if(controls.holdShare)await new Promise(resolve=>controls.releaseShare=resolve);if(controls.shareFail)throw Error('模拟分享失败');return {id:'receipt-fixture',state:controls.shareUnknown?'unknown':'ready',kind:'quark',name:input.name,url:controls.shareUnknown?'':'https://pan.quark.cn/s/fixture',password:input.password,days:input.days,message:'模拟分享结果',canCheck:true,secret:'never-copy-secret',accountId:'never-copy-account'}}
export async function ClearCloudShareHistory(){calls.shareClears=(calls.shareClears??0)+1;if(controls.shareClearFail)throw Error('模拟清理失败');const before=(controls.shareHistory??[]).length;controls.shareHistory=(controls.shareHistory??[]).filter(r=>!['ready','failed'].includes(r.result.state));return before-controls.shareHistory.length}
export async function ManagedCloudShares(accountId,page){calls.managedLists??=[];calls.managedLists.push({accountId,page});if(controls.holdManagedListPage===page)await new Promise(resolve=>controls.releaseManagedList=resolve);if(controls.managedListFail||controls.failManagedListPage===page)throw Error('模拟列表失败');if(controls.cloudSearchPages?.[page])return controls.cloudSearchPages[page];return {items:page===1?[{id:'protected-local-handle',name:'Cloud.zip',status:'有效性待核对',created:'',expiry:'永久',canRevoke:true}]:[],page,hasMore:page===1,next:page===1?'fixture-cursor':''}}
export async function ReadManagedCloudShare(id){calls.managedReads??=[];calls.managedReads.push(id);if(controls.holdManagedRead)await new Promise(resolve=>controls.releaseManagedRead=resolve);if(controls.managedReadFail||controls.failManagedHandle===id)throw Error('模拟原链接失败');if(controls.managedReadEmpty)return {name:'Cloud.zip',url:''};if(controls.managedReadResults?.[id])return controls.managedReadResults[id];if(controls.managedReadResult)return controls.managedReadResult;return {name:'Cloud.zip',url:'https://pan.quark.cn/s/cloud',password:'A1b2',expiry:'永久',message:'模拟只读',secret:'never-copy-secret'}}
export async function ReadManagedQuarkShareLinks(ids){calls.quarkShareReadGroups??=[];calls.quarkShareReadGroups.push(ids);if(controls.holdQuarkShareReadGroup)await new Promise(resolve=>controls.releaseQuarkShareReadGroup=resolve);return ids.map(id=>controls.quarkShareReadResults?.[id]??null)}
export async function ReadManagedQuarkShareRestrictions(id){calls.quarkRestrictionReads??=[];calls.quarkRestrictionReads.push(id);if(controls.holdQuarkRestrictions)await new Promise(resolve=>controls.releaseQuarkRestrictions=resolve);return {reported:120,items:[{name:'受限文件',reason:'网盘给出的原因',parent:'目录'}]}}
export async function ReadLanzouDirectoryShare(accountId,parentId,id,name,folder){calls.lanzouShareReads??=[];calls.lanzouShareReads.push({accountId,parentId,id,name,folder});if(controls.failLanzouParent===parentId)throw Error('原目录项目已变化');return {name,url:'https://fixture.lanzoub.com/'+(folder?'b':'i')+id,password:'fixture',expiry:'有效期沿用网盘设置'}}
export async function ReadLanzouDirectoryShares(accountId,entries){
 calls.lanzouDirectoryGroups??=[];calls.lanzouDirectoryGroups.push({accountId,entries});
 if(!controls.groupLanzouDirectories)throw Error('模拟分组读取失败');
 return entries.map(e=>e.folder?null:{name:e.name,url:'https://fixture.lanzoub.com/i'+e.entryId,password:''});
}
export async function UpdateLanzouDirectorySharePassword(accountId,parentId,id,name,folder,url,originalPassword,password){calls.lanzouShareEdits??=[];calls.lanzouShareEdits.push({accountId,parentId,id,name,folder,url,originalPassword,password,reads:calls.lanzouShareReads.length});return {state:controls.lanzouEditUnknown?'unknown':'ready',message:'模拟改码结果'}}
export async function ClearLanzouDirectorySharePassword(accountId,parentId,id,name,folder,url,originalPassword){calls.lanzouShareClears??=[];calls.lanzouShareClears.push({accountId,parentId,id,name,folder,url,originalPassword,reads:calls.lanzouShareReads.length});return {state:'ready',message:'模拟取消提取码'}}
export async function ManagedCloudSharesNext(accountId,token){calls.managedNext??=[];calls.managedNext.push({accountId,token});return controls.cloudSearchTokens?.[token]??{items:[],page:2,hasMore:false,next:''}}
export async function ManagedPan123PaidShares(accountId){calls.managedPaid??=[];calls.managedPaid.push(accountId);return controls.paidSharePage??{items:[],page:1,hasMore:false,next:''}}
export async function ManagedQuarkShares(accountId,page,filter){calls.managedQuark??=[];calls.managedQuark.push({accountId,page,filter});return controls.quarkSharePages?.[filter]?.[page]??{items:[],page,hasMore:false,next:''}}
export async function ManagedCloudShareRevokes(accountId){calls.revokeLoads??=[];calls.revokeLoads.push(accountId);return (controls.revokes??[]).filter(r=>!r.accountId||r.accountId===accountId).map(r=>({...r}))}
export async function RevokeManagedCloudShare(handle){calls.revocations??=[];calls.revocations.push(handle);if(controls.holdRevoke)await new Promise(resolve=>controls.releaseRevoke=resolve);const result={id:'revoke-receipt',name:'Cloud.zip',state:'unknown',message:'模拟撤销结果待确认',created:'2026-11-17T00:00:00Z',canCheck:true};controls.revokes=[result];if(controls.revokeFail)throw Error('模拟撤销回执错误');return result}
export async function CheckManagedCloudShareRevoke(id){calls.revokeChecks??=[];calls.revokeChecks.push(id);controls.revokes=(controls.revokes??[]).map(r=>r.id===id?{...r,state:'confirmed',message:'模拟原撤销结果已确认',canCheck:false}:r);return controls.revokes.find(r=>r.id===id)}
export async function ClearManagedCloudShareRevokes(accountId){calls.revokeClears=(calls.revokeClears??0)+1;if(controls.revokeClearFail)throw Error('模拟回执清理失败');const before=(controls.revokes??[]).length;controls.revokes=(controls.revokes??[]).filter(r=>!['confirmed','not_sent'].includes(r.state));return before-controls.revokes.length}
export async function ReadLanzouShareReceiptLinks(accountId,ids){
 calls.localBlueGroups??=[];calls.localBlueGroups.push({accountId,ids});
 if(controls.holdLocalBlueGroup)await new Promise(resolve=>controls.releaseLocalBlueGroup=resolve);
 return ids.map(()=>null);
}
export async function ReadLanzouShareReceiptLink(accountId,id){calls.localBlueReads??=[];calls.localBlueReads.push({accountId,id});return {url:controls.localBlueCurrentLink??'https://fixture.lanzoub.com/i123',name:'项目',password:'fixture',expiry:'沿用网盘设置'}}
export async function EditLanzouShareReceiptPassword(accountId,id,url,originalPassword,password,clear){calls.localBlueEdits??=[];calls.localBlueEdits.push({accountId,id,url,originalPassword,password,clear});return {state:'ready',message:'已核对'}}
export async function LocateLanzouShareReceipt(accountId,id){calls.shareLocations??=[];calls.shareLocations.push({accountId,id});return controls.shareLocation}
export async function CloudShares(){calls.shareHistoryReads=(calls.shareHistoryReads??0)+1;return (controls.shareHistory??[]).map(r=>({...r,result:{...r.result}}))}
export async function CheckCloudShare(id){calls.shareChecks??=[];calls.shareChecks.push(id);if(controls.holdShareCheck)await new Promise(resolve=>controls.releaseShareCheck=resolve);if(controls.shareCheckFail)throw Error('模拟检查失败');const result={id,state:'ready',kind:'quark',name:'fixture.txt',url:'https://pan.quark.cn/s/fixture',password:'A1b2',days:7,message:'模拟原操作检查',canCheck:true,...controls.shareCheckResult};if(controls.shareHistory)controls.shareHistory=controls.shareHistory.map(r=>r.result.id===id?{...r,result}:r);return result}
export async function RestartUpload(id){calls.restartUpload=id}
export async function CloudRecycleRestores(id){if(controls.recycleHistoryFail)throw Error('模拟回执读取失败');return (controls.recycleReceipts??[]).filter(r=>r.accountId===id)}
export async function LocateCloudRecycleRestore(accountId,id){calls.recycleLocations??=[];calls.recycleLocations.push({accountId,id});if(controls.holdRecycleLocation)await new Promise(r=>controls.releaseRecycleLocation=r);if(controls.failRecycleLocation)throw Error('还原项目已移动');return {accountId,parentId:'fixture-target'}}
export async function PreviewLanzouRecycleRestore(handle){calls.lanzouRestorePreviews??=[];calls.lanzouRestorePreviews.push(handle);if(controls.holdLanzouRestorePreview)await new Promise(resolve=>controls.releaseLanzouRestorePreview=resolve);if(controls.failLanzouRestorePreview)throw Error('根目录已有同名项目');return {evidence:'root',files:[]}}
export async function RestoreCloudRecycle(handle){calls.recycleRestores??=[];calls.recycleRestores.push(handle);if(controls.recycleRestoreThrow)throw Error('模拟中断');if(controls.holdRecycleRestore)await new Promise(resolve=>controls.releaseRecycleRestore=resolve);const r={id:'restore-receipt',entryId:'9007199254740993123',name:'b archive.zip',state:controls.recycleRestoreState??'unknown',message:'原结果待确认',canCheck:true,accountId:'b'};controls.recycleReceipts=[r];return r}
export async function ValidateCloudRecycleRestoreBatch(handles){calls.recyclePreflights??=[];calls.recyclePreflights.push([...handles]);if(controls.holdRecyclePreflight)await new Promise(resolve=>controls.releaseRecyclePreflight=resolve);if(controls.failRecyclePreflight)throw Error('模拟整批原目录冲突')}
export async function PrepareCloudRecyclePurge(accountId,page,entries){calls.purgePreflights??=[];calls.purgePreflights.push({accountId,page,entries});if(controls.holdPurgePreflight)await new Promise(resolve=>controls.releasePurgePreflight=resolve);if(controls.failPurgePreflight)throw Error('模拟永久删除原记录已变');if(controls.nullPurgePreflight)return null;return entries.map(r=>({name:r.name,handle:r.id,...(r.kind==='folder'&&accountId==='blue-preview'?{folderFiles:['完整文件名.txt']}:{})}))}
export async function PrepareCloudRecyclePurgeAcrossPages(accountId,selections){calls.crossPagePurgePreflights??=[];calls.crossPagePurgePreflights.push({accountId,selections});return PrepareCloudRecyclePurge(accountId,0,selections.map(row=>row.entry))}
export async function PurgeCloudRecycle(handle,confirmation){calls.purges??=[];calls.purges.push({handle,confirmation});if(controls.holdPurge)await new Promise(resolve=>controls.releasePurge=resolve);if(controls.throwPurge)throw Error('模拟永久删除调用中断');return {state:controls.purgeState??'confirmed',message:'模拟受理及记录移除核对'}}
export async function CloudRecyclePurgeHistory(id){if(controls.failPurgeHistory)throw Error('模拟永久删除记录读取失败');return (controls.purgeRows??[]).filter(r=>r.accountId===id)}
export async function CheckCloudRecyclePurge(id){calls.purgeChecks??=[];calls.purgeChecks.push(id);const r=controls.purgeRows.find(r=>r.id===id);if(controls.noPurgeAck)return r;Object.assign(r,{state:'confirmed',canCheck:false});return r}
export async function ClearCloudRecyclePurgeHistory(id){if(controls.failPurgeClear)throw Error('模拟记录清理保存失败');const old=controls.purgeRows.length;controls.purgeRows=controls.purgeRows.filter(r=>r.accountId!==id||!['confirmed','not_sent'].includes(r.state));return old-controls.purgeRows.length}
export async function CheckCloudRecycleRestore(id){calls.recycleChecks??=[];calls.recycleChecks.push(id);const r={...controls.recycleReceipts[0],state:'confirmed',canCheck:false,message:'原结果已核对'};controls.recycleReceipts=[r];return r}
export async function ClearCloudRecycleRestores(id){controls.recycleReceipts=[];return 1}
export async function ListCloudRecycle(id,page){calls.recycleLists??=[];calls.recycleLists.push({id,page});if(controls.holdRecycle)await new Promise(resolve=>controls.releaseRecycle=resolve);if(controls.recycleFail)throw Error('模拟回收站读取失败');if(controls.recycleSearchPages?.[page])return controls.recycleSearchPages[page];return {items:page===1?[{id:'9007199254740993123',name:id+' archive.zip',kind:'file',size:0,originalPath:'/原目录/archive.zip',restoreHandle:id==='b'?'restore-handle':''}]:[],page,hasMore:page===1}}
export async function ListLanzouRecycleFolder(){return controls.lanzouFolderPreview}
export async function PreviewLanzouRecycleCoverage(id){calls.recycleCoverage??=[];calls.recycleCoverage.push(id);return {rows:controls.recycleCoverage,checkedFolders:0,visibleFolders:0}}
export async function PickFiles(){return []}
`);
async function loadComponent(name){
 const source=await fs.readFile(path.join(root,'src',name+'.vue'),'utf8');
 let script=compileScript(parse(source).descriptor,{id:'cloud-drag-check'}).content;
 script=script.replace(/from ['"]vue['"]/g,`from ${JSON.stringify(fileURL('hooks.mjs'))}`)
  .replace(/from ['"]@wailsio\/runtime['"]/g,`from ${JSON.stringify(fileURL('events.mjs'))}`)
  .replace(/from ['"]\.\.\/bindings\/multipan\/internal\/desktop\/desktopservice['"]/g,`from ${JSON.stringify(fileURL('api.mjs'))}`)
  .replace(/from ['"]\.\/version['"]/g,`from ${JSON.stringify(fileURL("version.mjs"))}`)
  .replace(/from ['"]\.\/relayLimits['"]/g,`from ${JSON.stringify(pathToFileURL(path.join(root,'src/relayLimits.ts')).href)}`)
  .replace(/from ['"]\.\/downloadMeter['"]/g,`from ${JSON.stringify(fileURL("downloadMeter.mjs"))}`)
  .replace(/from ['"]\.\/fileShortcuts['"]/g,`from ${JSON.stringify(fileURL("fileShortcuts.mjs"))}`)
  .replace(/from ['"]\.\/fileList['"]/g,`from ${JSON.stringify(fileURL("fileList.mjs"))}`)
  .replace(/from ['"]\.\/useUploadReview['"]/g,`from ${JSON.stringify(fileURL("useUploadReview.mjs"))}`)
  .replace(/from ['"]\.\/uploadTasks['"]/g,`from ${JSON.stringify(fileURL("uploadTasks.mjs"))}`)
  .replace(/from ['"]\.\/uploadFolderMatch['"]/g,`from ${JSON.stringify(fileURL("uploadFolderMatch.mjs"))}`)
  .replace(/from ['"]\.\/transferRecovery['"]/g,`from ${JSON.stringify(fileURL("transferRecovery.mjs"))}`)
  .replace(/from ['"]\.\/shareText['"]/g,`from ${JSON.stringify(fileURL("shareText.mjs"))}`)
  .replace(/from ['"]\.\/spreadsheetCell['"]/g,`from ${JSON.stringify(fileURL("spreadsheetCell.mjs"))}`)
  .replace(/from ['"]\.\/shareAccounts['"]/g,`from ${JSON.stringify(fileURL("shareAccounts.mjs"))}`)
  .replace(/from ['"]\.\/pan123ShareTraffic['"]/g,`from ${JSON.stringify(fileURL("pan123ShareTraffic.mjs"))}`)
  .replace(/from ['"]\.\/pageSearch['"]/g,`from ${JSON.stringify(fileURL("pageSearch.mjs"))}`)
  .replace(/from ['"]\.\/downloadView['"]/g,`from ${JSON.stringify(fileURL("downloadView.mjs"))}`)
  .replace(/from ['"]\.\/downloadResults['"]/g,`from ${JSON.stringify(fileURL("downloadResults.mjs"))}`)
  .replace(/from ['"]\.\/browseHistory['"]/g,`from ${JSON.stringify(fileURL("browseHistory.mjs"))}`)
  .replace(/import (\w+)(?:,\s*\{[^}]*\})? from ['"]\.\/[^'"]+\.vue['"]/g,'const $1={}')
  .replace(/import \{trapDialogFocus\} from ['"].*?['"]/,'const trapDialogFocus=()=>{}');
 await fs.writeFile(path.join(dir,name+'.mjs'),ts.transpileModule(script,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText);
 return (await import(fileURL(name+'.mjs'))).default;
}
try {
 const component=await loadComponent('CloudFiles');
 const {calls,controls}=await import(fileURL('api.mjs'));
 const events=await import(fileURL('events.mjs'));
 const props=reactive({accounts:[{id:'a',kind:'quark',folder:'0',folderDisplay:'根目录'},{id:'b',kind:'quark',folder:'0',folderDisplay:'根目录'}],accountId:'a',blocked:false});
 const emitted=[];
 const state=component.setup(props,{expose(){},emit(...args){emitted.push(args)}});
 const settle=async()=>{await nextTick();await new Promise(r=>setImmediate(r));await nextTick()};
 await settle();assert.equal(state.canDrop.value,true);state.openBatchShare();assert.ok(state.batchShareTarget.value);assert.equal(state.canDrop.value,false);assert.equal(state.canNavigate.value,false);state.closeBatchShare();assert.equal(state.canDrop.value,true);
 state.error.value='模拟登录失效';assert.equal(state.canDrop.value,false);const beforeEditEvents=emitted.length;state.editCurrentAccount();assert.deepEqual(emitted.at(-1),['edit','a',false]);state.editCurrentAccount(true);assert.deepEqual(emitted.at(-1),['edit','a',true]);props.blocked=true;await settle();state.editCurrentAccount(true);assert.equal(emitted.length,beforeEditEvents+2);props.blocked=false;await settle();const listBeforeEdit=calls.lists.length;props.refreshKey=1;await settle();assert.equal(state.error.value,'');assert.equal(calls.lists.length,listBeforeEdit+1);assert.equal(state.page.value.currentId,'0');

 const file={path:'C:\\fixture\\测试.txt',name:'测试.txt',size:12,modified:123};
 const drop={targetId:'cloud-upload-dropzone',files:[file,{...file,path:file.path.toUpperCase()}],directories:1,unreadable:1,duplicates:0};
 events.emit({...drop,targetId:'upload-dropzone'});assert.equal(state.localFiles.value.length,0);
 events.emit(drop);assert.equal(state.localFiles.value.length,1);assert.match(state.uploadError.value,/文件夹/);assert.equal(calls.uploads.length,0);
 state.localFiles.value=[];assert.equal(calls.uploads.length,0);
 events.emit(drop);await state.uploadLocal();assert.equal(calls.uploads.length,1);assert.deepEqual(calls.uploads[0].targets,[{accountId:'a',folder:'0'}]);assert.equal(state.localFiles.value.length,0);assert.equal(calls.defaults,0);
 const event=()=>({dataTransfer:{setData(){},effectAllowed:'',dropEffect:''},preventDefault(){},stopPropagation(){}});
 const entry={id:'file',name:'测试文件.txt',folder:false,size:12};
 state.beginDrag(event(),entry);await state.dropDestination(event(),'0','根目录');assert.equal(calls.moves.length,0);
 state.clearDrag();await state.dropDestination(event(),'dest','根目录 / 资料');assert.equal(calls.moves.length,0);
 state.beginDrag(event(),entry);const pendingMove=state.dropDestination(event(),'dest','根目录 / 资料');assert.equal(calls.moves.length,1);assert.equal(state.saving.value,true);await state.dropDestination(event(),'dest','根目录 / 资料');await pendingMove;assert.deepEqual(calls.moves,[{accountId:'a',parentId:'0',destinationId:'dest',entryId:'file',name:'测试文件.txt'}]);assert.equal(calls.defaults,0);
 await state.load('dest');state.beginDrag(event(),entry);await state.dropDestination(event(),'0','根目录');assert.equal(calls.moves[1].parentId,'dest');assert.equal(calls.moves[1].destinationId,'0');
 props.blocked=true;await settle();events.emit(drop);assert.equal(state.localFiles.value.length,0);state.beginDrag(event(),entry);assert.equal(state.dragSource.value,null);
 props.blocked=false;await settle();state.beginDrag(event(),entry);props.accountId='b';await settle();assert.equal(state.dragSource.value,null);assert.equal(state.localFiles.value.length,0);await state.dropDestination(event(),'dest','资料');assert.equal(calls.moves.length,2);
 props.accounts[1].kind='lanzou';await settle();state.beginDrag(event(),{...entry,folder:true});assert.equal(state.dragSource.value,null);props.accounts[1].kind='quark';await settle();
 // Multiple selection uses the visible list; filtering clears stale selection.
 const group=[1,2,3].map(i=>({...entry,id:'batch'+i,name:'文件'+i+'.txt'}));
 function prepareGroup(){state.page.value.entries=[{id:'dest',name:'资料',folder:true,size:-1},...group]}
 prepareGroup();state.selectAll(true);assert.equal(state.selectedEntries.value.length,3);assert.equal(state.allSelected.value,true);
 state.query.value='文件1';await settle();assert.equal(state.selectedEntries.value.length,0);state.selectAll(true);assert.deepEqual(state.selectedIds.value,['batch1']);
 state.query.value='';await settle();state.selectAll(true);assert.equal(state.selectedFolderIds.value.length,1);state.openBatchShare();assert.equal(state.batchShareTarget.value.initialSelection.length,4);state.closeBatchShare();state.selectAll(false);state.toggleSelection('batch1',true);state.toggleSelection('batch2',true);state.toggleFolderSelection('dest',true);assert.equal(state.selectedShareEntries.value.length,3);state.openBatchShare();assert.equal(state.batchShareTarget.value.initialSelection.length,3);state.closeBatchShare();state.toggleSelection('batch3',true);state.selectedFolderIds.value=[];state.openSelectedMove();assert.equal(state.moveTargets.value.length,3);const beforeCancel=calls.moves.length;state.moveTargets.value=[];assert.equal(calls.moves.length,beforeCancel);
 // A selected-file drag submits the group sequentially once, even with one error.
 controls.failIds=['batch2'];controls.pauseNext=true;state.beginDrag(event(),group[0]);assert.equal(state.dragSource.value.entries.length,3);
 const batchStart=calls.moves.length,batch=state.dropDestination(event(),'dest','根目录 / 资料');assert.equal(calls.moves.length,batchStart+1);assert.equal(emitted.at(-1)[0],'mutation');assert.equal(emitted.at(-1)[1],true);
 await state.dropDestination(event(),'dest','资料');assert.equal(calls.moves.length,batchStart+1);controls.release();await batch;
 assert.deepEqual(calls.moves.slice(batchStart).map(f=>f.entryId),['batch1','batch2','batch3']);assert.equal(controls.maxActive,1);assert.deepEqual(state.moveResults.value.map(r=>r.success),[true,false,true]);assert.match(state.moveResults.value[1].message,/未确认/);assert.equal(state.selectedEntries.value.length,0);assert.equal(emitted.at(-1)[1],false);assert.equal(calls.defaults,0);
 // Moving an unselected item never silently includes the existing selection.
 prepareGroup();state.toggleSelection('batch1',true);state.beginDrag(event(),group[2]);assert.deepEqual(state.dragSource.value.entries.map(f=>f.id),['batch3']);state.clearDrag();
 // The picker route moves the full group too. Stop waits for the current item,
 // then marks remaining files unexecuted instead of resending earlier items.
 state.selectAll(true);state.selectedFolderIds.value=[];state.openSelectedMove();controls.pauseNext=true;controls.failIds=[];const stopStart=calls.moves.length,stopped=state.move('dest','资料');state.stopMoving.value=true;assert.equal(calls.moves.length,stopStart+1);controls.release();await stopped;
 assert.equal(calls.moves.length,stopStart+1);assert.equal(state.moveResults.value.length,3);assert.equal(state.moveResults.value.filter(r=>r.skipped).length,2);assert.equal(state.moveResults.value[0].success,true);assert.match(state.notice.value,/2 个未执行/);assert.equal(emitted.at(-1)[1],false);
 // Folder moves are an explicit DAV-only operation and never enter file batches.
 props.accounts[1].kind='webdav';await settle();const folder={id:'/来源/目录',name:'目录',folder:true,size:-1};state.page.value={currentId:'/来源',breadcrumbs:[{id:'/',name:'根目录'},{id:'/来源',name:'来源'}],entries:[folder]};
 state.selectAll(true);assert.equal(state.selectedEntries.value.length,0);state.beginDrag(event(),folder);assert.equal(state.validDestination(folder.id),false);assert.equal(state.validDestination(folder.id+'/子目录'),false);assert.equal(state.validDestination('/目标'),true);const fileWrites=calls.moves.length;await state.dropDestination(event(),'/目标','根目录 / 目标');assert.equal(calls.moves.length,fileWrites);assert.deepEqual(calls.folderMoves,[{accountId:'b',parentId:'/来源',destinationId:'/目标',entryId:folder.id,name:folder.name}]);assert.ok(emitted.some(e=>e[0]==='renamed'&&e[1]==='b'));
 const pickerComponent=await loadComponent('FolderPicker'),pickerEvents=[];
 const picker=pickerComponent.setup(reactive({accountId:'b',accountName:'测试',initialFolder:'/来源',kind:'webdav',purpose:'move',forbiddenRoots:[folder.id]}),{expose(){},emit(...args){pickerEvents.push(args)}});await settle();
 assert.equal(picker.forbidden(folder.id),true);assert.equal(picker.forbidden(folder.id+'/子'),true);assert.equal(picker.forbidden(folder.id+'2'),false);
 picker.page.value.currentId=folder.id+'/子';await picker.select();assert.equal(pickerEvents.length,0);picker.page.value.currentId='/目标';await picker.select();assert.equal(pickerEvents[0][0],'selected');assert.equal(pickerEvents[0][1],'/目标');assert.equal(calls.defaults,0);
 // ID-based clouds must send complete source and destination trails, including
 // the initial path retained when the directory picker opens below root.
 for(const kind of ['quark','pan123','guangya']){
  props.accounts[1].kind=kind;await settle();const root=kind==='guangya'?'root':'0';const idFolder={id:'42',name:'目录',folder:true,size:-1};
  const sourceTrail=[{id:root,name:'根目录'},{id:'10',name:'来源'}];state.page.value={currentId:'10',breadcrumbs:sourceTrail,entries:[idFolder,{id:'40',name:'目标',folder:true,size:-1}]};
  state.beginDrag(event(),idFolder);await state.dropDestination(event(),'40','根目录 / 来源 / 目标');const input=calls.folderMoves.at(-1);assert.deepEqual(input.sourceTrail.map(f=>f.id),[root,'10']);assert.deepEqual(input.destinationTrail.map(f=>f.id),[root,'10','40']);
  const selected=[];const idPicker=pickerComponent.setup(reactive({accountId:'b',accountName:'测试',kind,purpose:'move',initialFolder:'10',initialBreadcrumbs:sourceTrail,forbiddenRoots:['42']}),{expose(){},emit(...args){selected.push(args)}});await settle();assert.deepEqual(idPicker.page.value.breadcrumbs.map(f=>f.id),[root,'10']);idPicker.go({id:'40',name:'目标'});await settle();await idPicker.select();assert.deepEqual(selected[0][3].map(f=>f.id),[root,'10','40']);
 }
 props.accounts[1].kind='baidu';await settle();state.page.value={currentId:'/来源',breadcrumbs:[{id:'/',name:'根目录'},{id:'/来源',name:'来源'}],entries:[{id:'/来源/目录',name:'目录',folder:true,size:-1},{id:'/来源/目标',name:'目标',folder:true,size:-1}]};state.beginDrag(event(),state.page.value.entries[0]);assert.equal(state.validDestination('/来源/目录/子'),false);await state.dropDestination(event(),'/来源/目标','根目录 / 来源 / 目标');assert.equal(calls.folderMoves.at(-1).entryId,'/来源/目录');assert.equal(calls.folderMoves.at(-1).destinationId,'/来源/目标');assert.equal(calls.folderMoves.at(-1).sourceTrail,undefined);
 // Downloads run one at a time, with native progress/cancel and a
 // saved-path notice only after the service returns a committed result.
 const downloadEntry={...entry,id:'/测试文件.txt'};props.accounts[1].kind='ctfile';await settle();await state.download(downloadEntry);assert.equal(calls.downloads.length,0);
 props.accounts[1].kind='webdav';await settle();state.page.value={currentId:'/',breadcrumbs:[{id:'/',name:'根目录'}],entries:[downloadEntry]};controls.holdDownload=true;controls.downloadMode='success';const pendingDownload=state.download(downloadEntry);assert.equal(calls.downloads.length,1);assert.equal(state.downloading.value,true);assert.equal(emitted.at(-1)[1],true);
 events.progress({received:6,total:12});assert.equal(state.downloadProgress.value.received,6);assert.match(state.downloadRateLabel.value,/近期速度/);assert.match(state.downloadRemainingLabel.value,/当前文件/);events.progress({received:NaN,total:12});assert.equal(state.downloadProgress.value.received,6);await state.download(downloadEntry);assert.equal(calls.downloads.length,1);controls.releaseDownload();await pendingDownload;assert.equal(state.downloading.value,false);assert.match(state.notice.value,/saved.txt/);assert.equal(emitted.at(-1)[1],false);
 const cancelledDownload=state.download(downloadEntry);await state.cancelDownload();await cancelledDownload;assert.equal(calls.cancelDownloads,1);assert.match(state.notice.value,/已取消/);assert.equal(state.downloading.value,false);
 controls.holdDownload=false;controls.downloadMode='failed';await state.download(downloadEntry);assert.match(state.downloadError.value,/中断/);assert.equal(state.notice.value,'');assert.equal(state.saving.value,false);const downloads=calls.downloads.length;await state.download({...downloadEntry,folder:true});assert.equal(calls.downloads.length,downloads);assert.equal(calls.defaults,0);
 controls.downloadMode='warning';await state.download(downloadEntry);assert.match(state.downloadWarning.value,/未验证/);assert.match(state.downloadDetails.value,/fixture-digest/);assert.doesNotMatch(state.downloadWarning.value,/fixture-digest/);assert.match(state.downloadWarning.value,/saved.txt/);assert.equal(state.notice.value,'');assert.equal(state.downloadError.value,'');controls.downloadMode='success';await state.download(downloadEntry);assert.equal(state.downloadWarning.value,'');assert.match(state.notice.value,/校验通过/);assert.match(state.downloadDetails.value,/SHA256/);
 props.accounts[1].kind='pan123';await settle();controls.downloadMode='success';state.page.value={currentId:'0',breadcrumbs:[{id:'0',name:'根目录'}],entries:[{...entry,id:'9007199254740993'}]};await state.download(state.page.value.entries[0]);assert.equal(calls.downloads.at(-1).parentId,'0');assert.equal(calls.downloads.at(-1).entryId,'9007199254740993');assert.match(state.notice.value,/saved.txt/);
 props.accounts[1].kind='guangya';await settle();state.page.value={currentId:'root',breadcrumbs:[{id:'root',name:'根目录'}],entries:[{...entry,id:'gy-file-id'}]};await state.download(state.page.value.entries[0]);assert.equal(calls.downloads.at(-1).parentId,'root');assert.equal(calls.downloads.at(-1).entryId,'gy-file-id');assert.match(state.notice.value,/saved.txt/);
 props.accounts[1].kind='quark';await settle();state.page.value={currentId:'0',breadcrumbs:[{id:'0',name:'根目录'}],entries:[{...entry,id:'quark-file-id'}]};await state.download(state.page.value.entries[0]);assert.equal(calls.downloads.at(-1).parentId,'0');assert.equal(calls.downloads.at(-1).entryId,'quark-file-id');assert.match(state.notice.value,/saved.txt/);
 props.accounts[1].kind='baidu';await settle();state.page.value={currentId:'/',breadcrumbs:[{id:'/',name:'根目录'}],entries:[{...entry,id:'/baidu.txt'}]};await state.download(state.page.value.entries[0]);assert.equal(calls.downloads.at(-1).entryId,'/baidu.txt');assert.match(state.notice.value,/saved.txt/);
 props.accounts[1].kind='lanzou';await settle();state.page.value={currentId:'-1',breadcrumbs:[{id:'-1',name:'根目录'}],entries:[{...entry,id:'123'}]};await state.download(state.page.value.entries[0]);assert.equal(calls.downloads.at(-1).parentId,'-1');assert.equal(calls.downloads.at(-1).entryId,'123');
 controls.downloadMode='success';props.accounts[1].kind='baidu';await settle();const batchEntries=[{...entry,id:'/a.txt',name:'a.txt'},{...entry,id:'/b.txt',name:'b.txt'}];state.page.value={currentId:'/',breadcrumbs:[{id:'/',name:'根目录'}],entries:batchEntries};state.selectedIds.value=batchEntries.map(f=>f.id);controls.holdDownload=true;const batchPending=state.downloadSelected();assert.equal(state.batchDownloading.value,true);assert.equal(state.canDrop.value,false);assert.equal(calls.batches.at(-1).length,2);events.batch({index:1,count:2,name:'a.txt',progress:{received:1,total:3}});assert.match(state.downloadName.value,/1 \/ 2/);assert.equal(state.downloadProgress.value.received,1);events.progress({received:999,total:999});assert.equal(state.downloadProgress.value.received,1);events.batch({index:2,count:2,name:'b.txt',progress:{received:0,total:3}});assert.equal(state.downloadMetrics.value.bytesPerSecond,null);controls.releaseDownload();await batchPending;assert.equal(state.batchResults.value.length,2);assert.match(state.batchStatus(state.batchResults.value[0]),/校验通过/);assert.equal(state.batchResults.value[1].status,'failed');assert.equal(state.saving.value,false);
 state.resultFilter.value='verified';state.resultQuery.value='a.txt';assert.equal(state.filteredBatchResults.value.length,1);assert.equal(state.outcomeCounts.value.unfinished,1);assert.equal(state.batchPendingIndices.value.length,1);assert.match(state.downloadReport(),/b.txt/);assert.equal(state.batchResults.value.length,2);
 await state.queueSelected();assert.equal(calls.queuedDownloads.at(-1).length,2);assert.deepEqual(calls.queuedDownloads.at(-1).map(f=>f.entryId),['/a.txt','/b.txt']);assert.match(state.notice.value,/已加入/);
 const savedFirstPath=state.batchResults.value[0].result.path;state.selectedIds.value=[];const retryCallCount=calls.batches.length;const retryPending=state.retryBatch();assert.equal(calls.batches.length,retryCallCount+1);assert.deepEqual(calls.batches.at(-1).map(f=>f.entryId),['/b.txt']);await state.retryBatch();assert.equal(calls.batches.length,retryCallCount+1);events.batch({index:1,count:1,name:'b.txt',progress:{received:3,total:3},item:{name:'b.txt',status:'saved',message:'',result:{path:'C:/fixture/b.txt',verification:'verified'}}});assert.equal(state.batchResults.value[0].result.path,savedFirstPath);assert.equal(state.batchResults.value[1].name,'b.txt');controls.releaseDownload();await retryPending;assert.equal(state.batchPendingIndices.value.length,0);const finishedCalls=calls.batches.length;await state.retryBatch();assert.equal(calls.batches.length,finishedCalls);
 controls.holdDownload=false;state.selectedIds.value=batchEntries.map(f=>f.id);await state.downloadSelected();controls.downloadMode='failed';await state.retryBatch();assert.equal(state.batchResults.value[0].result.path,savedFirstPath);assert.equal(state.batchResults.value[1].status,'failed');assert.match(state.downloadError.value,/目录不可用/);controls.downloadMode='cancelled';await state.retryBatch();assert.equal(state.batchResults.value[0].status,'saved');assert.equal(state.batchResults.value[1].status,'skipped');assert.equal(state.batchPendingIndices.value.length,1);
 controls.downloadMode='failed';await state.downloadSelected();assert.match(state.downloadError.value,/目录不可用/);assert.equal(state.batchDownloading.value,false);controls.downloadMode='cancelled';await state.downloadSelected();assert.match(state.notice.value,/已取消/);assert.equal(state.batchResults.value[0].status,'skipped');
 state.resetDrops();assert.equal(state.batchSources.value.length,0);assert.equal(state.batchPendingIndices.value.length,0);const resetCalls=calls.batches.length;await state.retryBatch();assert.equal(calls.batches.length,resetCalls);
 const folderEntry={id:'/folder',name:'folder',folder:true,size:-1};props.accounts[1].kind='ctfile';await settle();state.page.value={currentId:'/',breadcrumbs:[{id:'/',name:'根目录'}],entries:[folderEntry]};await state.downloadFolder(folderEntry);assert.equal(calls.folderDownloads,undefined);props.accounts[1].kind='webdav';await settle();controls.downloadMode='success';controls.holdDownload=true;const folderPending=state.downloadFolder(folderEntry);assert.equal(state.folderDownloading.value,true);assert.equal(state.canDrop.value,false);assert.equal(calls.folderDownloads.at(-1).entryId,'/folder');events.folder({index:0,count:0,name:'正在读取目录',progress:{received:0,total:-1}});assert.match(state.downloadName.value,/读取目录/);events.folder({index:1,count:2,name:'sub/file.txt',progress:{received:3,total:9}});assert.equal(state.downloadProgress.value.received,3);events.folder({index:1,count:2,name:'sub/file.txt',progress:{received:9,total:9}});assert.match(state.downloadRateLabel.value,/校验并保存/);controls.releaseDownload();await folderPending;assert.equal(state.folderResults.value.length,2);assert.equal(state.folderDirectory.value,'C:/fixture/folder');assert.equal(state.folderResults.value[1].status,'failed');assert.match(state.downloadWarning.value,/部分完成/);assert.equal(state.notice.value,'');assert.equal(state.folderDownloading.value,false);assert.equal(state.batchSources.value.length,0);controls.holdDownload=false;controls.downloadMode='failed';await state.downloadFolder(folderEntry);assert.match(state.downloadError.value,/已存在/);controls.downloadMode='all-failed';await state.downloadFolder(folderEntry);assert.match(state.downloadError.value,/下载失败/);assert.equal(state.notice.value,'');controls.downloadMode='empty';await state.downloadFolder(folderEntry);assert.equal(state.folderResults.value.length,0);assert.match(state.notice.value,/0 个文件/);controls.downloadMode='success';controls.holdDownload=true;const cancelFolder=state.downloadFolder(folderEntry);await state.cancelDownload();await cancelFolder;assert.match(state.downloadWarning.value,/已取消/);assert.equal(state.saving.value,false);controls.holdDownload=false;state.resetDrops();assert.equal(state.folderDirectory.value,'');assert.equal(state.folderResults.value.length,0);
 props.accounts[1].kind='quark';await settle();controls.downloadMode='success';controls.holdDownload=false;state.page.value={currentId:'0',breadcrumbs:[{id:'0',name:'根目录'}],entries:[{id:'opaque-root',name:'资料',folder:true,size:-1}]};assert.equal(state.canDownloadFolders.value,true);await state.downloadFolder(state.page.value.entries[0]);assert.equal(calls.folderDownloads.at(-1).parentId,'0');assert.equal(calls.folderDownloads.at(-1).entryId,'opaque-root');assert.equal(state.folderResults.value.length,2);props.accounts[1].kind='pan123';await settle();state.page.value={currentId:'0',breadcrumbs:[{id:'0',name:'根目录'}],entries:[{id:'9007199254740993',name:'123资料',folder:true,size:-1}]};assert.equal(state.canDownloadFolders.value,true);await state.downloadFolder(state.page.value.entries[0]);assert.equal(calls.folderDownloads.at(-1).parentId,'0');assert.equal(calls.folderDownloads.at(-1).entryId,'9007199254740993');assert.equal(state.folderResults.value.length,2);for(const kind of ['lanzou','baidu','guangya']){props.accounts[1].kind=kind;await settle();const parent=kind==='lanzou'?'-1':kind==='guangya'?'root':'/';const id=kind==='lanzou'?'123':kind==='guangya'?'opaque-folder':'/folder';state.page.value={currentId:parent,breadcrumbs:[{id:parent,name:'根目录'}],entries:[{...folderEntry,id}]};const before=calls.folderDownloads.length;await state.downloadFolder(state.page.value.entries[0]);assert.equal(calls.folderDownloads.length,before+1);assert.equal(calls.folderDownloads.at(-1).parentId,parent);assert.equal(calls.folderDownloads.at(-1).entryId,id)}
 assert.equal(state.resultFilter.value,'all');assert.equal(state.resultQuery.value,'');state.resultFilter.value='unverified';assert.equal(state.filteredFolderResults.value.length,1);assert.equal(state.folderPending.value,1);assert.equal(state.outcomeCounts.value.unverified,1);state.resultQuery.value='no-match';assert.equal(state.shownResultCount.value,0);assert.equal(state.folderResults.value.length,2);assert.match(state.downloadReport(),/failed.txt/);state.clearResultFilters();assert.equal(state.filteredFolderResults.value.length,2);
 await state.openSavedFolder('C:/fixture/folder/sub/file.txt');assert.equal(calls.openFolders.at(-1),'C:/fixture/folder/sub/file.txt');assert.match(state.locationStatus.value,/已请求打开/);
 await state.copySavedPath('C:/fixture/folder');assert.equal(calls.reports.at(-1),'C:/fixture/folder');assert.equal(state.locationStatus.value,'路径已复制');
 const savedResults=JSON.stringify(state.folderResults.value);controls.openFolderFail=true;await state.openSavedFolder('C:/fixture/folder');assert.match(state.locationError.value,/目录已删除/);assert.equal(JSON.stringify(state.folderResults.value),savedResults);controls.openFolderFail=false;
 const openCount=calls.openFolders.length;state.saving.value=true;await state.openSavedFolder('C:/fixture/folder');state.saving.value=false;await state.openSavedFolder('');assert.equal(calls.openFolders.length,openCount);
 controls.copyFail=true;await state.copySavedPath('C:/fixture/folder');assert.match(state.locationError.value,/剪贴板失败/);assert.equal(JSON.stringify(state.folderResults.value),savedResults);controls.copyFail=false;
 props.accounts[1].secret='never-copy-credential';props.accounts[1].name='never-copy-account';await state.copyReport();assert.ok(calls.reports.at(-1).includes('MultiPan '+(await import(fileURL('version.mjs'))).APP_VERSION));assert.match(calls.reports.at(-1),/sub\/file.txt/);assert.doesNotMatch(calls.reports.at(-1),/never-copy/);assert.match(state.copyStatus.value,/已复制/);controls.copyFail=true;await state.copyReport();assert.match(state.copyStatus.value,/剪贴板失败/);assert.equal(state.folderResults.value.length,2);

 controls.copyFail=false;controls.downloadMode='success';await state.downloadFolder(state.page.value.entries[0]);assert.equal(state.folderPending.value,1);assert.equal(state.folderRetryId.value,'fixture-retry');
 const firstSaved=structuredClone({...state.folderResults.value[0],result:{...state.folderResults.value[0].result}});
 const originalSource={...state.folderSource.value};
 controls.retryFail=true;await state.retryFolder();assert.match(state.downloadError.value,/重试错误/);assert.deepEqual({...state.folderResults.value[0],result:{...state.folderResults.value[0].result}},firstSaved);assert.equal(state.folderPending.value,1);controls.retryFail=false;
 const beforeWrongPage=calls.folderRetries.length;const originalFolderPage={...state.page.value,entries:[...state.page.value.entries]};const originalPage=state.page.value.currentId;const originalReportPath=state.downloadOrigin.value;await state.load('another-folder');assert.equal(state.folderPending.value,1);assert.equal(state.downloadOrigin.value,originalReportPath);controls.retryFail=true;await state.retryFolder();assert.equal(calls.folderRetries.length,beforeWrongPage+1);assert.equal(calls.folderRetries.at(-1).input.parentId,originalSource.parentId);controls.retryFail=false;await state.load(originalPage);state.page.value=originalFolderPage;
 controls.holdDownload=true;const folderRetryPendingUi=state.retryFolder();assert.equal(state.saving.value,true);assert.equal(state.folderDownloading.value,true);assert.equal(state.downloadMetrics.value.phase,'waiting');const retryCalls=calls.folderRetries.length;await state.retryFolder();assert.equal(calls.folderRetries.length,retryCalls);assert.deepEqual({...calls.folderRetries.at(-1).input},originalSource);assert.equal(calls.folderRetries.at(-1).token,'fixture-retry');
 events.folder({index:2,count:2,name:'failed.txt',progress:{received:2,total:3}});assert.equal(state.downloadProgress.value.received,2);assert.equal(state.folderResults.value[0].result.path,firstSaved.result.path);controls.releaseDownload();await folderRetryPendingUi;controls.holdDownload=false;
 assert.equal(state.folderPending.value,0);assert.equal(state.folderRetryId.value,'');assert.equal(state.folderResults.value[0].result.verification,'unverified');assert.match(state.notice.value,/下载完成/);await state.retryFolder();assert.equal(calls.folderRetries.length,retryCalls);
 await state.downloadFolder(state.page.value.entries[0]);controls.retryCancel=true;await state.retryFolder();assert.match(state.downloadWarning.value,/已取消/);assert.equal(state.folderPending.value,1);assert.equal(state.folderRetryId.value,'fixture-retry');controls.retryCancel=false;await state.retryFolder();assert.equal(state.folderPending.value,0);
 await state.downloadFolder(state.page.value.entries[0]);const beforeReset=calls.folderRetries.length;state.resetDrops();await state.retryFolder();assert.equal(calls.folderRetries.length,beforeReset);assert.equal(state.folderSource.value,null);assert.equal(state.folderRetryId.value,'');

 const uploadPickerEvents=[];const uploadPicker=pickerComponent.setup(reactive({accountId:'b',accountName:'测试',initialFolder:'/',kind:'webdav',purpose:'upload'}),{expose(){},emit(...args){uploadPickerEvents.push(args)}});await settle();uploadPicker.page.value.currentId='/本次目标';await uploadPicker.select();assert.equal(uploadPickerEvents[0][1],'/本次目标');assert.equal(calls.defaults,0);
 state.page.value={currentId:'root',breadcrumbs:[{id:'root',name:'根目录'}],entries:[{id:'a',name:'A',folder:false,size:1},{id:'b',name:'B',folder:false,size:1},{id:'c',name:'C',folder:false,size:1}]};state.selectedIds.value=[];state.selectFile({target:null,ctrlKey:false,metaKey:false,shiftKey:false},state.page.value.entries[0]);state.selectFile({target:null,ctrlKey:false,metaKey:false,shiftKey:true},state.page.value.entries[2]);assert.deepEqual([...state.selectedIds.value],['a','b','c']);state.selectFile({target:null,ctrlKey:true,metaKey:false,shiftKey:false},state.page.value.entries[1]);assert.deepEqual([...state.selectedIds.value],['a','c']);


 

 const listPage=state.page.value;state.page.value={...listPage,entries:[{id:'d',name:'文件夹',folder:true,size:-1},{id:'a',name:'A.pdf',folder:false,size:1},{id:'b',name:'B.txt',folder:false,size:4},{id:'c',name:'C.zip',folder:false,size:2}]};state.selectedIds.value=['a','c'];state.setSort('size');await settle();assert.equal(state.sort.value,'size-asc');assert.deepEqual(state.entries.value.map(f=>f.id),['d','a','c','b']);assert.deepEqual([...state.selectedIds.value],['a','c']);state.setSort('size');await settle();assert.equal(state.sortState('size'),'descending');assert.deepEqual(state.entries.value.map(f=>f.id),['d','b','c','a']);
 state.displayKind.value='folders';await settle();assert.equal(state.selectedIds.value.length,0);assert.deepEqual(state.entries.value.map(f=>f.id),['d']);state.selectAll(true);assert.equal(state.selectedIds.value.length,0);
 state.displayKind.value='files';state.query.value='B';await settle();state.selectAll(true);assert.deepEqual([...state.selectedIds.value],['b']);state.query.value='不存在';await settle();assert.equal(state.entries.value.length,0);assert.equal(state.hasListFilter.value,true);state.clearListFilters();await settle();assert.equal(state.entries.value.length,4);assert.equal(state.hasListFilter.value,false);state.sort.value='asc';state.page.value=listPage;await settle();
 const keyEvent=(key,flags={})=>({key,target:null,ctrlKey:false,shiftKey:false,altKey:false,metaKey:false,isComposing:false,defaultPrevented:false,preventDefault(){this.defaultPrevented=true},...flags});
 state.selectedIds.value=[];state.handleFileKey(keyEvent('a',{ctrlKey:true}));assert.deepEqual([...state.selectedIds.value],['a','b','c']);state.handleFileKey(keyEvent('Escape'));assert.equal(state.selectedIds.value.length,0);
 state.selectedIds.value=['a'];state.handleFileKey(keyEvent('F2'));await settle();assert.equal(state.renameTarget.value.id,'a');state.closeRename();
 const editKey=keyEvent('a',{ctrlKey:true,target:{closest(){return {}}}});state.selectedIds.value=['a'];state.handleFileKey(editKey);assert.equal(editKey.defaultPrevented,false);assert.deepEqual([...state.selectedIds.value],['a']);
 state.saving.value=true;const lockedKey=keyEvent('F5');state.handleFileKey(lockedKey);assert.equal(lockedKey.defaultPrevented,true);state.saving.value=false;
 state.handleFileKey(keyEvent('N',{ctrlKey:true,shiftKey:true}));await settle();assert.equal(state.createOpen.value,true);state.closeCreate();
 props.view='downloads';const hiddenKey=keyEvent('F2');state.handleFileKey(hiddenKey);assert.equal(hiddenKey.defaultPrevented,false);props.view='files';
 state.selectedIds.value=['a','c'];
const shortcutPage=state.page.value;state.handleFileKey(keyEvent('F5'));await settle();assert.equal(state.page.value.currentId,shortcutPage.currentId);state.page.value={...shortcutPage,currentId:'child',breadcrumbs:[{id:'root',name:'根目录'},{id:'child',name:'子目录'}]};state.handleFileKey(keyEvent('ArrowUp',{altKey:true}));await settle();assert.equal(state.page.value.currentId,'root');state.page.value=shortcutPage;state.selectedIds.value=['a','c'];
 const previousWindow=globalThis.window;globalThis.window={innerWidth:900,innerHeight:700};
 const menuSize={width:195,height:330};state.contextMenu.value={getBoundingClientRect:()=>menuSize,querySelector:()=>null};
 let restoredRowFocus=false;
 const trigger={getBoundingClientRect:()=>({left:840,right:874,top:640,bottom:674}),focus(){restoredRowFocus=true}};
 state.selectedIds.value=['a','c'];state.openEntryActions({currentTarget:trigger},state.page.value.entries[0]);await settle();
 assert.deepEqual([...state.selectedIds.value],['a','c']);assert.equal(state.contextGroup.value,false);assert.equal(state.contextShareGroup.value,false);
 assert.equal(state.contextState.value.x,874-menuSize.width);assert.equal(state.contextState.value.y,640-menuSize.height);
 state.contextKeys({key:'Escape',preventDefault(){}});assert.equal(restoredRowFocus,true);assert.equal(state.contextState.value,null);
 state.openEntryActions({currentTarget:trigger},state.page.value.entries[0]);await settle();state.contextAction('move');
 assert.deepEqual(state.moveTargets.value.map(f=>f.id),['a']);state.moveTargets.value=[];
 try{state.handleFileKey(keyEvent('F10',{shiftKey:true}));await settle();assert.equal(state.contextGroup.value,true);state.closeContext();await state.openContext({clientX:899,clientY:699},state.page.value.entries[0]);assert.equal(state.contextState.value.x,900-menuSize.width-8);assert.equal(state.contextState.value.y,700-menuSize.height-8);assert.equal(state.contextGroup.value,true);state.contextAction('move');assert.deepEqual(state.moveTargets.value.map(f=>f.id),['a','c']);assert.equal(state.contextState.value,null);state.moveTargets.value=[];
 await state.openContext({clientX:100,clientY:100},state.page.value.entries[1]);assert.deepEqual([...state.selectedIds.value],['b']);assert.equal(state.contextGroup.value,false);state.contextAction('rename');await settle();assert.equal(state.renameTarget.value.id,'b');state.closeRename();
 await state.openContext({clientX:100,clientY:100},{id:'folder',name:'文件夹',folder:true,size:-1});assert.equal(state.contextState.value.entry.folder,true);assert.equal(state.selectedIds.value.length,0);state.closeContext();await state.openContext({clientX:100,clientY:100});assert.equal(state.contextState.value.entry,null);state.contextAction('all');assert.deepEqual([...state.selectedIds.value],['a','b','c']);await state.openContext({clientX:100,clientY:100});state.contextKeys({key:'Escape',preventDefault(){}});assert.equal(state.contextState.value,null);
 }finally{state.contextMenu.value=null;if(previousWindow===undefined)delete globalThis.window;else globalThis.window=previousWindow}

 state.resetHistory();await state.load('root',[{id:'root',name:'根目录'}]);state.go({id:'A',name:'目录 A'});await settle();state.go({id:'B',name:'目录 B'});await settle();assert.equal(state.canGoBack.value,true);assert.deepEqual(state.page.value.breadcrumbs.map(c=>c.id),['root','A','B']);await state.goHistory(-1);assert.equal(state.page.value.currentId,'A');assert.equal(state.canGoForward.value,true);await state.load('A',state.page.value.breadcrumbs);assert.equal(state.canGoForward.value,true);await state.goHistory(1);assert.equal(state.page.value.currentId,'B');assert.deepEqual(state.page.value.breadcrumbs.map(c=>c.id),['root','A','B']);await state.goHistory(-1);state.go({id:'C',name:'新分支'});await settle();assert.equal(state.canGoForward.value,false);
 controls.failListParent='A';await state.goHistory(-1);assert.match(state.error.value,/模拟目录已变化/);assert.equal(state.browseHistory.current(state.accountScope.value).id,'C');controls.failListParent=undefined;await state.goHistory(-1);assert.equal(state.page.value.currentId,'A');
 const lastB=state.page.value.currentId;props.accountId='a';await settle();await state.load('other-account');props.accountId='b';await settle();assert.equal(state.page.value.currentId,lastB);assert.equal(calls.lists.at(-1).parent,lastB);
 controls.holdListParent='late';const slowList=state.load('late');props.accountId='a';await settle();assert.equal(state.page.value.currentId,'other-account');controls.releaseList();await slowList;assert.equal(state.page.value.currentId,'other-account');controls.holdListParent=undefined;props.accountId='b';await settle();assert.equal(state.page.value.currentId,lastB);assert.equal(state.browseHistory.current(state.accountScope.value).id,lastB);
 const countBeforeLock=calls.lists.length;state.saving.value=true;await state.goHistory(-1);assert.equal(calls.lists.length,countBeforeLock);state.saving.value=false;state.createOpen.value=true;await state.goHistory(-1);assert.equal(calls.lists.length,countBeforeLock);state.createOpen.value=false;state.localFiles.value=[file];await state.goHistory(-1);assert.equal(calls.lists.length,countBeforeLock);state.localFiles.value=[];state.resetHistory();await state.load('root');assert.equal(state.canGoBack.value,false);const oldScope=state.accountScope.value;await state.load('cached-before-source-change');props.accounts[1].endpoint='https://new-source.invalid';await settle();assert.equal(state.page.value.currentId,'root');assert.equal(state.canGoBack.value,false);assert.equal(state.browseHistory.current(oldScope),null);
 controls.snapshotAccounts=[{id:'draft-a',kind:'webdav',name:'123456',endpoint:'https://fixture.invalid',username:'fixture',folder:'/',folderDisplay:'根目录'},{id:'draft-b',kind:'webdav',name:'987654',endpoint:'https://fixture.invalid',username:'fixture',folder:'/',folderDisplay:'根目录'}];
 const workspaceComponent=await loadComponent('Workspace');const workspace=workspaceComponent.setup({}, {expose(){},emit(){}});await settle();assert.equal(workspace.tab.value,'files');assert.equal(workspace.accountLabel(workspace.accounts.value[0]),'WebDAV 1');assert.equal(workspace.accountLabel(workspace.accounts.value[1]),'WebDAV 2');
 workspace.openPendingModule('copy','draft-b','copy-record');assert.equal(workspace.tab.value,'files');assert.equal(workspace.browserAccountId.value,'draft-b');assert.equal(workspace.primaryNavigation.some(item=>item.id==='shares'),true);assert.equal(workspace.navigationGroups.some(group=>group.items.some(item=>item.id==='recycle')),false);workspace.tab.value='review';for(const kind of ['restore','purge'])workspace.openPendingModule(kind,'draft-a','recycle-record');assert.equal(workspace.tab.value,'review');assert.equal(workspace.pendingReceipt.value,null);workspace.cloudMoving.value=true;workspace.openPendingModule('share','draft-b','share-record');assert.equal(workspace.tab.value,'review');workspace.cloudMoving.value=false;workspace.openPendingModule('share_settings','draft-b','settings-record');assert.equal(workspace.tab.value,'shares');assert.equal(workspace.pendingReceipt.value.receiptId,'settings-record');workspace.openPendingModule('invalid','draft-a','invalid-record');assert.equal(workspace.tab.value,'shares');workspace.tab.value='files';await nextTick();assert.equal(workspace.pendingReceipt.value,null);workspace.browserAccountId.value='';
 const originalManagedAccounts=structuredClone(controls.snapshotAccounts);controls.snapshotAccounts[0]={...controls.snapshotAccounts[0],kind:'guangya',folder:'root',endpoint:'https://api.guangyapan.com',username:'fixture-device'};await workspace.refresh();workspace.editAccount('draft-a');assert.equal(workspace.form.value.id,'draft-a');assert.equal(workspace.loginMode.value,false);assert.equal(workspace.form.value.secret,'');workspace.form.value.name='修改后的名称';assert.notEqual(workspace.accounts.value[0].name,'修改后的名称');const loginBefore=calls.accountLogins?.length??0;await workspace.saveAccount();assert.equal(workspace.accountRefresh.value['draft-a'],1);assert.equal(workspace.accounts.value[0].id,'draft-a');assert.equal(workspace.accounts.value[0].name,'修改后的名称');assert.equal(calls.accountLogins?.length??0,loginBefore);workspace.editAccount('draft-a',true);assert.equal(workspace.loginMode.value,true);assert.equal(calls.accountLogins?.length??0,loginBefore);await workspace.saveAccount();assert.equal(calls.accountLogins.length,loginBefore+1);assert.equal(calls.accountLogins.at(-1).id,'draft-a');assert.equal(workspace.accountRefresh.value['draft-a'],2);assert.equal(workspace.accounts.value.length,2);workspace.cloudMoving.value=true;workspace.editAccount('draft-a',true);assert.equal(workspace.modal.value,false);workspace.cloudMoving.value=false;controls.snapshotAccounts=originalManagedAccounts;await workspace.refresh();
 controls.snapshotTasks=[{id:'task-live',status:'running',kind:'quark',file:{name:'live.zip',size:10},folder:'/',folderDisplay:'根目录',sent:1},{id:'task-fail',status:'failed',kind:'lanzou',file:{name:'报告.zip',size:10},folder:'123',folderDisplay:'根目录 / 备份',sent:0},{id:'task-done',status:'success',kind:'quark',file:{name:'done.zip',size:10},folder:'/',sent:10}];await workspace.refresh();workspace.filter.value='attention';workspace.taskKind.value='lanzou';workspace.taskQuery.value='备份';assert.deepEqual(workspace.visible.value.map(t=>t.id),['task-fail']);assert.equal(workspace.taskCounts.value.active,1);await workspace.refresh();assert.equal(workspace.taskQuery.value,'备份');assert.equal(workspace.tasks.value.length,3);await workspace.control(workspace.visible.value[0]);assert.deepEqual(calls.taskControls.at(-1),{action:'retry',id:'task-fail'});workspace.taskQuery.value='no-match';assert.equal(workspace.visible.value.length,0);workspace.clearTaskFilters();assert.deepEqual(workspace.visible.value.map(t=>t.id),['task-done','task-fail','task-live']);await workspace.control(workspace.visible.value[2]);assert.deepEqual(calls.taskControls.at(-1),{action:'stop',id:'task-live'});await workspace.copyTaskResult(workspace.tasks.value[1]);assert.match(calls.reports.at(-1),/报告.zip/);assert.match(calls.reports.at(-1),/状态：失败/);assert.doesNotMatch(calls.reports.at(-1),/task-fail/);controls.copyFail=true;await workspace.copyTaskResult(workspace.tasks.value[1]);assert.match(workspace.taskCopyStatus.value,/剪贴板失败/);assert.equal(workspace.tasks.value.length,3);assert.equal(workspace.copyingTask.value,'');controls.copyFail=false;workspace.clearTaskFilters();workspace.selectVisibleTasks(true);assert.deepEqual(workspace.chosenTasks.value.map(t=>t.id),['task-fail','task-live']);const batchControlStart=calls.taskControls.length;await workspace.controlChosen('retry');assert.deepEqual(calls.taskControls.slice(batchControlStart),[{action:'retry',id:'task-fail'}]);assert.match(workspace.taskBatchResults.value[0].message,/请求已提交/);assert.equal(workspace.taskSelection.value.length,0);workspace.selectVisibleTasks(true);await workspace.controlChosen('stop');assert.deepEqual(calls.taskControls.at(-1),{action:'stop',id:'task-live'});workspace.selectVisibleTasks(true);workspace.taskQuery.value='no-match';await settle();assert.equal(workspace.taskSelection.value.length,0);workspace.clearTaskFilters();await settle();controls.snapshotTasks.push({...controls.snapshotTasks[1],id:'task-fail2'});await workspace.refresh();workspace.selectVisibleTasks(true);controls.failTask='task-fail2';controls.holdTask=true;const pendingControls=workspace.controlChosen('retry');const beforeDouble=calls.taskControls.length;await workspace.controlChosen('retry');assert.equal(calls.taskControls.length,beforeDouble);assert.equal(workspace.taskControlling.value,true);controls.holdTask=false;controls.releaseTask();await pendingControls;assert.equal(workspace.taskBatchResults.value.length,2);assert.equal(workspace.taskBatchResults.value.filter(r=>r.failed).length,1);assert.equal(workspace.taskControlling.value,false);controls.failTask='';workspace.selectVisibleTasks(true);controls.holdTask=true;const beforeBatchCancel=calls.taskControls.length;const cancellingBatch=workspace.controlChosen('retry');assert.equal(workspace.taskBatchTotal.value,2);workspace.stopTaskBatch();workspace.stopTaskBatch();controls.holdTask=false;controls.releaseTask();await cancellingBatch;assert.equal(calls.taskControls.length,beforeBatchCancel+1);assert.equal(workspace.taskBatchSubmitted.value,1);assert.equal(workspace.taskBatchSkipped.value,1);assert.equal(workspace.taskBatchFailed.value,0);assert.equal(workspace.taskControlling.value,false);assert.equal(workspace.taskBatchName.value,'');workspace.selectVisibleTasks(true);await workspace.controlChosen('retry');assert.equal(workspace.taskBatchStop.value,false);assert.equal(workspace.taskBatchSubmitted.value,2);controls.snapshotTasks.pop();await workspace.refresh();workspace.tasks.value=Array.from({length:205},(_,i)=>({...controls.snapshotTasks[1],id:'limit-'+i}));await settle();workspace.selectVisibleTasks(true);assert.equal(workspace.taskSelection.value.length,50);workspace.goTaskPage(2);await settle();assert.equal(workspace.taskSelection.value.length,0);assert.equal(workspace.taskPageView.value.first,51);workspace.selectVisibleTasks(true);assert.equal(workspace.chosenTasks.value.length,50);assert.ok(workspace.chosenTasks.value.every(t=>Number(t.id.slice(6))>=105&&Number(t.id.slice(6))<=154));workspace.taskSort.value='oldest';await settle();assert.equal(workspace.taskPage.value,1);assert.equal(workspace.taskSelection.value.length,0);assert.equal(workspace.taskPageView.value.items[0].id,'limit-0');workspace.taskPageSize.value=200;await settle();workspace.selectVisibleTasks(true);assert.equal(workspace.taskSelection.value.length,200);workspace.toggleTask('limit-204',true);assert.equal(workspace.taskSelection.value.length,200);workspace.taskPageSize.value=50;workspace.clearTaskFilters();await settle();await workspace.refresh();workspace.clearTaskFilters();await workspace.openTaskFolder(workspace.tasks.value[1]);assert.equal(calls.uploadFolders.at(-1),'task-fail');assert.match(workspace.uploadFolderStatus.value,/已请求打开/);controls.uploadFolderFail=true;await workspace.openTaskFolder(workspace.tasks.value[1]);assert.match(workspace.uploadFolderStatus.value,/已删除/);assert.equal(workspace.openingUploadFolder.value,'');assert.equal(workspace.tasks.value.length,3);controls.uploadFolderFail=false;workspace.taskQuery.value='old';workspace.taskKind.value='lanzou';workspace.filter.value='attention';
 workspace.browse(workspace.accounts.value[0]);await workspace.folderChosen('/once','根目录 / 本次上传');assert.equal(workspace.folders.value['draft-a'],'/once');assert.equal(workspace.folderName(workspace.accounts.value[0]),'根目录 / 本次上传');assert.equal(workspace.accounts.value[0].folder,'/');assert.equal(calls.defaults,0);
 {
  const {matchUploadFolder}=await import(fileURL('uploadFolderMatch.mjs'));
  const level=folders=>async parent=>({currentId:parent,folders});
  assert.equal((await matchUploadFolder('/', ['工具'],level([{id:'exact',name:'工具'},{id:'near',name:'工 具'}]),()=>true)).choices[0].id,'exact');
  assert.deepEqual((await matchUploadFolder('/', ['工具'],level([{id:'one',name:'工 具'},{id:'two',name:'工-具'}]),()=>true)).choices.map(row=>row.id),['one','two']);
  assert.equal((await matchUploadFolder('/', ['工具','资料'],level([{id:'one',name:'工 具'},{id:'two',name:'工-具'}]),()=>true)).state,'ambiguous');
  assert.equal((await matchUploadFolder('/', ['Windows2026'],level([{id:'old',name:'Windows2025'}]),()=>true)).state,'missing');
  assert.equal((await matchUploadFolder('/', ['Windows2026'],level([{id:'typo',name:'Windos2026'}]),()=>true)).choices[0].id,'typo');
  const origin=workspace.accounts.value[0],other=workspace.accounts.value[1];
  const root=workspace.rootFolder(origin.kind),targetRoot=workspace.rootFolder(other.kind);
  const sourceTrail=[{id:root,name:'根目录'},{id:'/batch',name:'Windows2026'},{id:'/once',name:'002实用工具'}];
  controls.folderMatches={[other.id]:{[targetRoot]:[{id:'batch',name:'Ｗｉｎｄｏｗｓ ２０２６'}],batch:[{id:'candidate',name:'００２ - 实用工具'}]}};
  workspace.tab.value='upload';
  const originalTarget=workspace.folders.value[other.id],sent=calls.uploads.length;
  workspace.browse(origin);await workspace.folderChosen('/once','根目录 / Windows2026 / 002实用工具',sourceTrail);
  assert.equal(workspace.folders.value[other.id],originalTarget,'suggestion applied without confirmation');
  assert.equal(workspace.folderMatches.value[other.id].choices[0].path,'根目录 / Ｗｉｎｄｏｗｓ ２０２６ / ００２ - 实用工具');
  workspace.useFolderMatch(other.id,'candidate');assert.equal(workspace.folders.value[other.id],'candidate');
  assert.equal(calls.uploads.length,sent);
  assert.equal(calls.defaults,0);
  controls.holdFolderMatch=true;
  workspace.browse(origin);const stale=workspace.folderChosen('/once','根目录 / Windows2026 / 002实用工具',sourceTrail);
  workspace.browse(origin);await workspace.folderChosen(root,'根目录',[sourceTrail[0]]);
  controls.releaseFolderMatch();await stale;
  assert.deepEqual(workspace.folderMatches.value,{});
  delete controls.folderMatches;
  workspace.folders.value[other.id]=originalTarget;
  workspace.browse(origin);await workspace.folderChosen('/once','根目录 / 本次上传');
 }
 workspace.recoverAccount(workspace.accounts.value[0].id);assert.equal(workspace.modal.value,true);assert.equal(workspace.form.value.id,workspace.accounts.value[0].id);assert.equal(workspace.form.value.secret,'');workspace.closeModal();workspace.form.value={...workspace.accounts.value[0],secret:''};workspace.browse(workspace.form.value,'default');await workspace.folderChosen('/future','根目录 / 默认位置');assert.equal(workspace.form.value.folder,'/future');assert.equal(workspace.accounts.value[0].folder,'/');assert.equal(workspace.folders.value['draft-a'],'/once');
 workspace.files.value=[file];workspace.selected.value=['draft-a'];await workspace.start();assert.equal(calls.uploads.at(-1).targets[0].folder,'/once');assert.equal(workspace.tab.value,'tasks');assert.equal(workspace.taskQuery.value,'');assert.equal(workspace.taskKind.value,'');assert.equal(workspace.filter.value,'all');assert.equal(workspace.folderName(workspace.accounts.value[0]),'根目录 / 本次上传');
 workspace.cloudMoving.value=true;workspace.openFiles(workspace.accounts.value[0]);assert.equal(workspace.tab.value,'tasks');workspace.cloudMoving.value=false;workspace.openFiles(workspace.accounts.value[0]);assert.equal(workspace.tab.value,'files');
 controls.folderFiles=[{path:'C:/fixture/tree',name:'tree',directory:true,relativePath:'tree',size:0,modified:1},{path:'C:/fixture/tree/a.txt',name:'a.txt',directory:false,relativePath:'tree/a.txt',size:4,modified:1}];workspace.files.value=[];await workspace.pickFolder();assert.equal(workspace.files.value[0].directory,true);assert.equal(workspace.files.value[1].relativePath,'tree/a.txt');
 workspace.files.value=[];const sameSource=controls.folderFiles[1];workspace.appendFiles([{...sameSource,relativePath:''}]);workspace.appendFiles([sameSource]);assert.equal(workspace.files.value.length,2,'standalone and tree upload plans must both survive');workspace.appendFiles([sameSource]);assert.equal(workspace.files.value.length,2,'same mapping deduplicates');
 const panelComponent=await loadComponent('TransfersPanel');const panelProps=reactive({view:'settings',jobs:[],settings:{uploadConcurrency:3,downloadConcurrency:1,uploadKiB:0,downloadKiB:0,closeToTray:false}});const panel=panelComponent.setup(panelProps,{expose(){},emit(){}});panel.form.value.uploadConcurrency=2;panel.dirty.value=true;panelProps.settings={...panelProps.settings,uploadConcurrency:5};await settle();assert.equal(panel.form.value.uploadConcurrency,2);await panel.save();assert.equal(calls.settings.at(-1).uploadConcurrency,2);assert.equal(calls.settings.at(-1).downloadConcurrency,1);panel.form.value.downloadConcurrency=4;await panel.save();assert.equal(calls.settings.at(-1).downloadConcurrency,4);assert.equal(panel.dirty.value,false);controls.settingsFail=true;await panel.save();assert.match(panel.message.value,/保存失败/);assert.equal(panel.busy.value,false);controls.settingsFail=false;
 panelProps.view='queue';panelProps.jobs=[{id:'queued-1',status:'queued',kind:'quark',destination:'C:/fixture/visible.zip',input:{name:'visible.zip'}},{id:'failed-2',status:'failed',kind:'lanzou',destination:'C:/fixture/hidden.zip',input:{name:'hidden.zip'}}];await settle();panel.query.value='visible';await settle();panel.selectJobs(true);assert.equal(panel.chosen.value.length,1);controls.holdQueue=true;const heldQueueBatch=panel.batch('pause');assert.deepEqual(calls.queueBatches.at(-1),{ids:['queued-1'],action:'pause'});const queueBatchCount=calls.queueBatches.length;await panel.batch('resume');assert.equal(calls.queueBatches.length,queueBatchCount);controls.holdQueue=false;controls.releaseQueue();await heldQueueBatch;assert.equal(panel.selection.value.length,0);panel.selectJobs(true);panel.query.value='hidden';await settle();assert.equal(panel.selection.value.length,0);
 panel.selectJobs(true);await panel.copyChosen();assert.match(calls.reports.at(-1),/hidden.zip/);assert.doesNotMatch(calls.reports.at(-1),/visible.zip/);assert.equal(panel.busy.value,false);
 panel.selectJobs(true);await panel.exportChosen();assert.deepEqual(calls.reportExports.at(-1).ids,['failed-2']);assert.equal(calls.reportExports.at(-1).version,(await import(fileURL('version.mjs'))).APP_VERSION);assert.match(panel.message.value,/已保存/);controls.cancelReportExport=true;await panel.exportChosen();assert.match(panel.message.value,/已取消/);controls.cancelReportExport=false;await panel.clearPartials();assert.deepEqual(calls.partialClears.at(-1),['failed-2']);assert.equal(panel.selection.value.length,0);await panel.controlAll('pause');assert.equal(calls.allQueueActions.at(-1),'pause');
 panelProps.jobs=Array.from({length:121},(_,i)=>({id:'page-'+i,kind:i%2?'quark':'lanzou',status:'paused',destination:'C:/archive/'+i+'.zip',input:{name:'item'+i+'.zip'}}));panel.clearQueueFilters();await settle();assert.equal(panel.pageView.value.items.length,50);panel.selectJobs(true);assert.equal(panel.chosen.value.length,50);panel.goQueuePage(2);await settle();assert.equal(panel.selection.value.length,0);assert.equal(panel.pageView.value.first,51);panel.selectJobs(true);await panel.batch('pause');assert.equal(calls.queueBatches.at(-1).ids.length,50);assert.ok(calls.queueBatches.at(-1).ids.every(id=>Number(id.slice(5))>=21&&Number(id.slice(5))<=70));panel.queueKind.value='quark';await settle();assert.equal(panel.queuePage.value,1);assert.ok(panel.rows.value.every(j=>j.kind==='quark'));panel.goQueuePage(2);await settle();panelProps.jobs=panelProps.jobs.slice(0,3);await settle();assert.equal(panel.pageView.value.page,1);panel.selectJobs(true);panelProps.jobs=[];await settle();assert.equal(panel.chosen.value.length,0);assert.equal(panel.pageView.value.first,0);
 state.localFiles.value=[];state.acceptLocalDrop({targetId:'cloud-upload-dropzone',files:[{...sameSource,relativePath:''},sameSource],directories:0,unreadable:0,duplicates:0});assert.equal(state.localFiles.value.length,2);state.localFiles.value=[];


 // Share dialog is passive on open; its only mutation submits once. Rechecks
 // use the opaque receipt and copying uses a small explicit result whitelist.
 { // Batch shares: passively select files AND folders, then serialize mutations.
 const batchComponent=await loadComponent('BatchShareDialog');
 const batchProps={accountId:'a',parentId:'0',kind:'quark',entries:[{id:'same',name:'One.txt',folder:false,size:1},{id:'same',name:'Folder',folder:true,size:0},{id:'three',name:'Three.txt',folder:false,size:1}],initialSelection:[]};
 const makeBatch=(overrides={})=>batchComponent.setup(reactive({...batchProps,...overrides}),{expose(){},emit(){}});
 const batch=makeBatch();assert.equal(calls.batchShares?.length??0,0);assert.equal(batch.valid.value,false);assert.equal(batch.days.value,0);assert.equal(batch.protectedShare.value,false);
 batch.toggle(batchProps.entries[0],true);batch.toggle(batchProps.entries[1],true);assert.equal(batch.selected.value.length,2);batch.toggle(batchProps.entries[0],false);assert.deepEqual(batch.selected.value,['d:same']);batch.selectAll();
 controls.holdBatchShare=true;const pendingBatch=batch.submit();await batch.submit();assert.equal(calls.batchShares.length,1);assert.equal(batch.rows.value[0].state,'running');assert.equal(batch.rows.value[1].state,'queued');assert.equal(calls.batchShares[0].password,'');assert.equal(calls.batchShares[0].days,0);batch.stop.value=true;controls.holdBatchShare=false;controls.releaseBatchShare();await pendingBatch;assert.equal(calls.batchShares.length,1);assert.equal(batch.remaining.value,2);batch.toggle(batchProps.entries[2],false);assert.equal(batch.selected.value.length,3);
 await batch.run();assert.equal(calls.batchShares.length,3);assert.equal(batch.ready.value.length,3);assert.equal(batch.resultLink(batch.rows.value[0]),'https://pan.quark.cn/s/fixture');assert.equal(calls.batchShares[1].folder,true);await batch.run();await batch.submit();assert.equal(calls.batchShares.length,3);await batch.copy();assert.match(calls.reports.at(-1),/One.txt/);assert.match(calls.reports.at(-1),/Folder/);assert.doesNotMatch(calls.reports.at(-1),/batch-|accountId|parentId/);
 const unknownBatch=makeBatch();unknownBatch.selectAll();controls.batchShareUnknown=true;await unknownBatch.submit();assert.equal(calls.batchShares.length,4);assert.equal(unknownBatch.unresolved.value,true);assert.equal(unknownBatch.resultLink(unknownBatch.rows.value[0]),'');assert.equal(unknownBatch.remaining.value,2);await unknownBatch.run();assert.equal(calls.batchShares.length,4);await unknownBatch.check(unknownBatch.rows.value[0]);assert.equal(calls.shareChecks.at(-1),'batch-4');controls.batchShareUnknown=false;await unknownBatch.run();assert.equal(calls.batchShares.length,6);assert.equal(unknownBatch.remaining.value,0);
 const lostBatch=makeBatch();lostBatch.selectAll();controls.batchShareFail=true;await lostBatch.submit();assert.equal(lostBatch.unresolved.value,true);const lostCount=calls.batchShares.length;controls.batchShareFail=false;await lostBatch.run();await lostBatch.submit();assert.equal(calls.batchShares.length,lostCount);assert.match(lostBatch.rows.value[0].error,/连接丢失/);
 const large=makeBatch({entries:Array.from({length:60},(_,i)=>({id:String(i),name:String(i),folder:false,size:1}))});large.selectAll();assert.equal(large.selected.value.length,50);large.toggle({id:'59',folder:false},true);assert.equal(large.selected.value.length,50);
 for(const kind of ['lanzou','pan123','baidu','guangya']){const item=makeBatch({kind,entries:[batchProps.entries[0]],initialSelection:[batchProps.entries[0]]});assert.equal(item.days.value,0);assert.equal(item.protectedShare.value,kind==='baidu');assert.equal(item.valid.value,true);await item.submit();assert.equal(calls.batchShares.at(-1).action,kind==='lanzou'?'get':'create');assert.equal(calls.batchShares.at(-1).password.length,kind==='baidu'?4:0)}
 const cleanupHooks=await import(fileURL('hooks.mjs'));const disposedBatch=makeBatch();disposedBatch.selectAll();controls.holdBatchShare=true;const disposedPending=disposedBatch.submit();await settle();const disposedCount=calls.batchShares.length;cleanupHooks.cleanup.at(-1)();controls.holdBatchShare=false;controls.releaseBatchShare();await disposedPending;assert.equal(calls.batchShares.length,disposedCount);assert.equal(disposedBatch.remaining.value,2);await disposedBatch.run();assert.equal(calls.batchShares.length,disposedCount);
 for(const kind of ['baidu','pan123','guangya']){const mixed=makeBatch({kind,entries:batchProps.entries,initialSelection:batchProps.entries});const before=calls.batchShares.length;assert.equal(mixed.selected.value.length,3);assert.equal(mixed.combine.value,true);await mixed.submit();assert.equal(calls.batchShares.length,before+1);assert.equal(calls.batchShares.at(-1).targets.length,3);assert.equal(calls.batchShares.at(-1).targets.filter(t=>t.folder).length,1);assert.equal(mixed.rows.value.length,1);assert.match(mixed.rows.value[0].entry.name,/3项/);const separate=makeBatch({kind,entries:batchProps.entries,initialSelection:batchProps.entries});separate.combine.value=false;await separate.submit();assert.equal(calls.batchShares.length,before+4)}
 const unsupported=makeBatch({kind:'webdav',initialSelection:[batchProps.entries[0]]});await unsupported.submit();assert.equal(unsupported.attempted.value,false);
 const preflight=makeBatch();preflight.selectAll();
 const writesBefore=calls.batchShares.length;
 controls.failCreationPreflight=true;
 await preflight.submit();
 controls.failCreationPreflight=false;
 assert.equal(calls.shareCreationPreflights.at(-1).length,3);
 assert.equal(calls.batchShares.length,writesBefore);
 assert.match(preflight.error.value,/未创建分享/);
 controls.holdCreationPreflight=true;
 const stoppedPreflight=preflight.run();
 await settle();
 preflight.stop.value=true;
 controls.holdCreationPreflight=false;
 controls.releaseCreationPreflight();
 await stoppedPreflight;
 assert.equal(calls.batchShares.length,writesBefore);
 assert.equal(preflight.remaining.value,3);
 const quarkEntries=[batchProps.entries[0],{...batchProps.entries[1],id:'quark-folder'}];
 const quarkCombined=makeBatch({entries:quarkEntries,initialSelection:quarkEntries});
 assert.equal(quarkCombined.combine.value,false);
 quarkCombined.combine.value=true;
 const beforeGroup=calls.batchShares.length;
 await quarkCombined.submit();
 assert.equal(calls.batchShares.length,beforeGroup+1);
 assert.deepEqual(calls.batchShares.at(-1).targets,quarkEntries.map(({id,name,folder})=>({id,name,folder})));
 assert.deepEqual(calls.shareCreationPreflights.at(-1)[0].targets,calls.batchShares.at(-1).targets);
 }
 const settingsComponent=await loadComponent('ShareSettingsDialog');const settingsDialog=settingsComponent.setup(reactive({handle:'sealed-fixture',name:'fixture'}),{expose(){},emit(){}});await settle();assert.ok(settingsDialog.original.value);assert.equal(settingsDialog.days.value,-1);await settingsDialog.save();assert.equal(calls.shareSettings?.length??0,0);settingsDialog.days.value=7;settingsDialog.password.value='New2';settingsDialog.protectedShare.value=true;controls.holdSettings=true;const settingsPending=settingsDialog.save();await settingsDialog.save();assert.equal(calls.shareSettings.length,1);controls.holdSettings=false;controls.releaseSettings();await settingsPending;assert.equal(settingsDialog.result.value.state,'ready');assert.deepEqual(calls.shareSettings.at(-1),{handle:'sealed-fixture',password:'New2',days:7});await settingsDialog.save();assert.equal(calls.shareSettings.length,1);await settingsDialog.check();assert.equal(calls.shareChecks.at(-1),'settings-fixture');assert.equal(calls.shareSettings.length,1);
 const unknownSettings=settingsComponent.setup(reactive({handle:'sealed-unknown',name:'fixture'}),{expose(){},emit(){}});await settle();unknownSettings.days.value=0;controls.settingsUnknown=true;await unknownSettings.save();const settingsCalls=calls.shareSettings.length;assert.equal(unknownSettings.result.value.state,'unknown');await unknownSettings.save();assert.equal(calls.shareSettings.length,settingsCalls);await unknownSettings.check();assert.equal(calls.shareSettings.length,settingsCalls);controls.settingsUnknown=false;
 const bdSettings=settingsComponent.setup(reactive({handle:'baidu-expiry',name:'fixture',kind:'baidu'}),{expose(){},emit(){}});await settle();assert.equal(bdSettings.isBaidu.value,true);bdSettings.days.value=3;await bdSettings.save();assert.deepEqual(calls.shareSettings.at(-1),{handle:'baidu-expiry',password:'',days:3});
 {
  const beforeExpiryWrites=calls.shareSettings.length
  const rename=settingsComponent.setup(reactive({handle:'pan123-original',name:'Cloud.zip',kind:'pan123'}),{expose(){},emit(){}})
  await settle()
  rename.edit123.value='title'
  await rename.save()
  assert.equal(calls.shareRenames,undefined)
  rename.title.value='不合/法'
  assert.equal(rename.valid.value,false)
  rename.title.value='😀'.repeat(256)
  assert.equal(rename.valid.value,false)
  rename.title.value=' 新主题 '
  await Promise.all([rename.save(),rename.save()])
  assert.deepEqual(calls.shareRenames,[{handle:'pan123-original',title:'新主题'}])
  assert.equal(calls.shareSettings.length,beforeExpiryWrites)
  await rename.check()
  assert.equal(calls.shareChecks.at(-1),'rename-fixture')
  assert.equal(calls.shareRenames.length,1)
 }
 {
  const setup=()=>settingsComponent.setup(reactive({handle:'pan123-limit',name:'Cloud.zip',kind:'pan123'}),{expose(){},emit(){}})
  controls.managedReadResult={name:'Cloud.zip',password:'A1b2',expiry:'到期',trafficCap123:2**30}
  const traffic=setup()
  await settle()
  traffic.edit123.value='traffic'
  traffic.trafficMode.value='limit'
  traffic.trafficAmount.value='1'
  assert.equal(traffic.valid.value,false)
  traffic.trafficAmount.value='2'
  await Promise.all([traffic.save(),traffic.save()])
  assert.deepEqual(calls.shareTraffic,[{handle:'pan123-limit',limit:2**31}])
  await traffic.check()
  assert.equal(calls.shareChecks.at(-1),'traffic-fixture')
  assert.equal(calls.shareTraffic.length,1)
  const unlimited=setup()
  await settle()
  unlimited.edit123.value='traffic'
  unlimited.trafficMode.value='unlimited'
  await unlimited.save()
  assert.deepEqual(calls.shareTraffic.at(-1),{handle:'pan123-limit',limit:0})
  delete controls.managedReadResult
 }
 {
  controls.managedReadResult={name:'Cloud.zip',password:'A1b2',expiry:'到期',trafficMode123:1}
  const permissions=settingsComponent.setup(reactive({handle:'pan123-permissions',name:'Cloud.zip',kind:'pan123'}),{expose(){},emit(){}})
  await settle()
  permissions.edit123.value='permissions'
  permissions.permissionChoice.value=4
  await permissions.save()
  assert.equal(calls.sharePermissions,undefined)
  permissions.permissionConsent.value=true
  await Promise.all([permissions.save(),permissions.save()])
  assert.deepEqual(calls.sharePermissions,[{handle:'pan123-permissions',mode:4}])
  await permissions.check()
  assert.equal(calls.shareChecks.at(-1),'permissions-fixture')
  assert.equal(calls.sharePermissions.length,1)
  const singleCombined=settingsComponent.setup({handle:'pan123-single-combined',name:'Cloud.zip',kind:'pan123',paid:true},{expose(){},emit(){}})
  await settle()
  singleCombined.edit123.value='permissions'
  singleCombined.permissionChoice.value=4
  singleCombined.changeLimitWithPermission.value=true
  singleCombined.trafficMode.value='limit'
  singleCombined.trafficAmount.value='2'
  assert.equal(singleCombined.editCombined123.value,true)
  await singleCombined.save()
  assert.equal(calls.combinedSettings,undefined)
  singleCombined.permissionConsent.value=true
  await Promise.all([singleCombined.save(),singleCombined.save()])
  assert.deepEqual(calls.combinedSettings,[{handle:'pan123-single-combined',mode:4,limit:2**31}])
  assert.equal(calls.sharePermissions.length,1)
  controls.managedReadResult={...controls.managedReadResult,trafficMode123:4,trafficCap123:2**31}
  const alreadySet=settingsComponent.setup({handle:'pan123-same-settings',name:'Cloud.zip',kind:'pan123'},{expose(){},emit(){}})
  await settle()
  alreadySet.edit123.value='permissions'
  alreadySet.permissionChoice.value=4
  alreadySet.changeLimitWithPermission.value=true
  alreadySet.trafficMode.value='limit'
  alreadySet.trafficAmount.value='2'
  alreadySet.permissionConsent.value=true
  assert.equal(alreadySet.valid.value,false)
  alreadySet.trafficAmount.value='3'
  assert.equal(alreadySet.valid.value,true)
  delete controls.managedReadResult
 }
 {
  const beforeWrites=calls.shareSettings.length
  const activation=settingsComponent.setup(reactive({handle:'baidu-frozen',name:'fixture',kind:'baidu',activate:true,originalExpiry:'永久'}),{expose(){},emit(){}})
  await settle()
  await activation.save()
  assert.equal(calls.shareActivations,undefined)
  activation.agreed.value=true
  await Promise.all([activation.save(),activation.save()])
  assert.deepEqual(calls.shareActivations,['baidu-frozen'])
  assert.equal(activation.result.value.state,'unknown')
  await activation.check()
  assert.equal(calls.shareChecks.at(-1),'activation-fixture')
  assert.deepEqual(calls.shareActivations,['baidu-frozen'])
  assert.equal(calls.shareSettings.length,beforeWrites)
 }
 const shareComponent=await loadComponent('ShareDialog');const shareProps=reactive({accountId:'a',parentId:'0',kind:'quark',entry:{id:'fid',name:'fixture.txt',folder:false,size:1}});const sharesBefore=calls.shares?.length??0;const share=shareComponent.setup(shareProps,{expose(){},emit(){}});assert.equal(calls.shares?.length??0,sharesBefore);assert.equal(share.protectedShare.value,false);assert.equal(share.days.value,0);share.protectedShare.value=true;share.password.value='A1b2';share.days.value=7;controls.holdShare=true;const pendingShare=share.submit();await share.submit();assert.equal(calls.shares.length,sharesBefore+1);assert.deepEqual(calls.shares.at(-1),{accountId:'a',parentId:'0',entryId:'fid',name:'fixture.txt',folder:false,action:'create',password:'A1b2',days:7});controls.holdShare=false;controls.releaseShare();await pendingShare;await share.submit();assert.equal(calls.shares.length,sharesBefore+1);await share.copy(false);assert.match(calls.reports.at(-1),/提取码：A1b2/);assert.doesNotMatch(calls.reports.at(-1),/never-copy|accountId|secret/);await share.copy(true);assert.equal(calls.reports.at(-1),'https://pan.quark.cn/s/fixture');await share.check();assert.equal(calls.shareChecks.at(-1),'receipt-fixture');assert.equal(calls.shares.length,sharesBefore+1);
 const defaultShare=shareComponent.setup(shareProps,{expose(){},emit(){}});await defaultShare.submit();assert.equal(calls.shares.at(-1).password,'');assert.equal(calls.shares.at(-1).days,0);
 const panShare=shareComponent.setup(reactive({...shareProps,kind:'pan123',entry:{id:'123',name:'folder',folder:true,size:-1}}),{expose(){},emit(){}});assert.equal(panShare.title.value,'123 云盘分享');assert.equal(panShare.actionLabel.value,'创建分享');await panShare.submit();assert.deepEqual(calls.shares.at(-1),{accountId:'a',parentId:'0',entryId:'123',name:'folder',folder:true,action:'create',password:'',days:0});const protected123=shareComponent.setup(reactive({...shareProps,kind:'pan123'}),{expose(){},emit(){}});protected123.protectedShare.value=true;protected123.password.value='abc';assert.equal(protected123.valid.value,false);protected123.password.value='A1b2';protected123.days.value=30;await protected123.submit();assert.equal(calls.shares.at(-1).action,'create');assert.equal(calls.shares.at(-1).password,'A1b2');assert.equal(calls.shares.at(-1).days,30);
 const baiduShare=shareComponent.setup(reactive({...shareProps,kind:'baidu',parentId:'/',entry:{id:'/folder',name:'folder',folder:true,size:-1}}),{expose(){},emit(){}});assert.equal(baiduShare.title.value,'百度网盘分享');assert.equal(baiduShare.protectedShare.value,true);assert.equal(baiduShare.days.value,0);assert.equal(baiduShare.autoFill.value,true);baiduShare.protectedShare.value=false;baiduShare.password.value='';assert.equal(baiduShare.valid.value,false);const beforeBaiduSubmit=calls.shares.length;await baiduShare.submit();assert.equal(calls.shares.length,beforeBaiduSubmit);baiduShare.password.value='B1c2';await baiduShare.submit();assert.deepEqual(calls.shares.at(-1),{accountId:'a',parentId:'/',entryId:'/folder',name:'folder',folder:true,action:'create',password:'B1c2',days:0});await baiduShare.submit();assert.equal(calls.shares.length,beforeBaiduSubmit+1);baiduShare.result.value={id:'bd-receipt',kind:'baidu',state:'ready',name:'folder',url:'https://pan.baidu.com/s/1fixture',password:'B1c2',days:0,canCheck:true,message:''};assert.equal(baiduShare.resultLink.value,'https://pan.baidu.com/s/1fixture?pwd=B1c2');await baiduShare.copy(true);assert.equal(calls.reports.at(-1),'https://pan.baidu.com/s/1fixture?pwd=B1c2');await baiduShare.copy(false);assert.match(calls.reports.at(-1),/永久/);assert.match(calls.reports.at(-1),/pwd=B1c2/);baiduShare.autoFill.value=false;assert.equal(baiduShare.resultLink.value,'https://pan.baidu.com/s/1fixture');await baiduShare.copy(true);assert.equal(calls.reports.at(-1),'https://pan.baidu.com/s/1fixture');assert.equal(calls.shares.length,beforeBaiduSubmit+1);
 const gyShare=shareComponent.setup(reactive({...shareProps,kind:'guangya',parentId:'root',entry:{id:'folder',name:'folder',folder:true,size:-1}}),{expose(){},emit(){}});assert.equal(gyShare.title.value,'光鸭云盘分享');assert.equal(gyShare.protectedShare.value,false);assert.equal(gyShare.days.value,0);await gyShare.submit();assert.equal(calls.shares.at(-1).password,'');assert.equal(calls.shares.at(-1).days,0);assert.equal(calls.shares.at(-1).action,'create');const gyProtected=shareComponent.setup(reactive({...shareProps,kind:'guangya'}),{expose(){},emit(){}});gyProtected.protectedShare.value=true;gyProtected.password.value='G1y2';gyProtected.days.value=30;await gyProtected.submit();assert.equal(calls.shares.at(-1).password,'G1y2');assert.equal(calls.shares.at(-1).days,30);
 const lzShare=shareComponent.setup(reactive({...shareProps,kind:'lanzou',parentId:'-1',entry:{id:'123',name:'folder',folder:true,size:-1}}),{expose(){},emit(){}});await lzShare.submit();assert.equal(calls.shares.at(-1).action,'get');assert.equal(calls.shares.at(-1).password,'');assert.equal(calls.shares.at(-1).folder,true);assert.equal(calls.shares.at(-1).days,0);
 const uncertainShare=shareComponent.setup(shareProps,{expose(){},emit(){}});controls.shareUnknown=true;await uncertainShare.submit();const beforeUncertainCopy=calls.reports.length;await uncertainShare.copy();assert.equal(calls.reports.length,beforeUncertainCopy);await uncertainShare.check();assert.equal(uncertainShare.result.value.state,'ready');controls.shareUnknown=false;
 props.accounts[1].kind='quark';await settle();await state.load('0');state.openShare({...entry,id:'file',name:'测试文件.txt'});assert.ok(state.shareTarget.value);assert.equal(state.canDrop.value,false);assert.equal(state.canNavigate.value,false);state.closeShare();assert.equal(state.shareTarget.value,null);const menuWindow=globalThis.window;globalThis.window={innerWidth:900,innerHeight:700};try{const height=342;state.contextMenu.value={getBoundingClientRect:()=>({width:195,height}),querySelector:()=>null};await state.openContext({clientX:899,clientY:699},{id:'folder',name:'folder',folder:true,size:-1});assert.equal(state.contextState.value.y,700-height-8);state.closeContext()}finally{state.contextMenu.value=null;globalThis.window=menuWindow}
 // Receipt history only reads/checks opaque original IDs, never calls create.
 const managedComponent=await loadComponent('CloudShareList');const managedProps=reactive({accounts:[{id:'q',kind:'quark'},{id:'b',kind:'baidu'},{id:'p',kind:'pan123'},{id:'g',kind:'guangya'}],blocked:false,refreshKey:'0'});const managedEvents=[];const managed=managedComponent.setup(managedProps,{expose(){},emit(...a){managedEvents.push(a)}});await settle();assert.equal(managed.items.value.length,1);assert.deepEqual(calls.managedLists.at(-1),{accountId:'q',page:1});assert.equal(managed.accounts.value.length,4);const createsBeforeManaged=calls.shares.length;controls.holdManagedRead=true;const readsBeforeManaged=calls.managedReads?.length??0;const managedPending=managed.read(managed.items.value[0]);assert.equal(managed.readId.value,managed.items.value[0].id);assert.equal(managed.busy.value,true);await managed.read(managed.items.value[0]);assert.equal(calls.managedReads.length,readsBeforeManaged+1);controls.holdManagedRead=false;controls.releaseManagedRead();await managedPending;assert.equal(managed.detail.value.name,'Cloud.zip');await managed.copy();assert.match(calls.reports.at(-1),/提取码：A1b2/);assert.doesNotMatch(calls.reports.at(-1),/protected-local-handle|never-copy|accountId/);await managed.copy(true);assert.equal(calls.reports.at(-1),'https://pan.quark.cn/s/cloud');controls.managedReadFail=true;await managed.read(managed.items.value[0]);assert.equal(managed.detail.value,null);assert.match(managed.error.value,/原链接失败/);assert.equal(managed.busy.value,false);controls.managedReadFail=false;controls.managedReadEmpty=true;await managed.read(managed.items.value[0]);assert.equal(managed.detail.value,null);assert.match(managed.error.value,/未返回可用链接/);assert.equal(managed.busy.value,false);assert.equal(managed.readId.value,managed.items.value[0].id);controls.managedReadEmpty=false;await managed.load(2);assert.equal(managed.readId.value,'');assert.equal(managed.page.value,2);assert.equal(managed.items.value.length,0);assert.equal(managed.hasMore.value,false);controls.managedListFail=true;await managed.load(1);assert.equal(managed.page.value,2);assert.match(managed.error.value,/列表失败/);controls.managedListFail=false;await managed.load(1);controls.holdManagedRead=true;const staleRead=managed.read(managed.items.value[0]);managedProps.refreshKey='1';await nextTick();assert.equal(managed.readId.value,'');assert.equal(managed.detail.value,null);controls.holdManagedRead=false;controls.releaseManagedRead();await staleRead;assert.equal(managed.detail.value,null);assert.equal(managed.items.value.length,1);const countLists=calls.managedLists.length,countReads=calls.managedReads.length;managedProps.blocked=true;await managed.load();await managed.read(managed.items.value[0]);assert.equal(calls.managedLists.length,countLists);assert.equal(calls.managedReads.length,countReads);managedProps.blocked=false;assert.equal(calls.shares.length,createsBeforeManaged);assert.deepEqual(managedEvents.at(-1),['mutation',false]);
 const revokeRow=managed.items.value[0];const revokeCount=calls.revocations?.length??0;managed.prepareRevoke(revokeRow);assert.equal(calls.revocations?.length??0,revokeCount);assert.equal(managed.confirmTarget.value.id,revokeRow.id);assert.equal(managed.locked.value,true);managed.cancelRevoke();assert.equal(managed.confirmTarget.value,null);assert.equal(calls.revocations?.length??0,revokeCount);managed.prepareRevoke(revokeRow);controls.holdRevoke=true;const revokePending=managed.revoke();await managed.revoke();assert.equal(calls.revocations.length,revokeCount+1);controls.holdRevoke=false;controls.releaseRevoke();await revokePending;assert.equal(managed.busy.value,false);assert.equal(managed.revokes.value[0].state,'unknown');assert.equal(managed.detail.value,null);const beforeRevokeCheck=calls.revocations.length;await managed.checkRevoke(managed.revokes.value[0]);assert.equal(calls.revocations.length,beforeRevokeCheck);assert.equal(calls.revokeChecks.at(-1),'revoke-receipt');assert.equal(managed.revokes.value[0].state,'confirmed');controls.revokeClearFail=true;await managed.clearRevokes();assert.match(managed.error.value,/清理失败/);assert.equal(managed.revokes.value.length,1);assert.equal(managed.busy.value,false);controls.revokeClearFail=false;await managed.clearRevokes();assert.equal(managed.revokes.value.length,0);assert.equal(calls.revocations.length,beforeRevokeCheck);managed.prepareRevoke(revokeRow);controls.revokeFail=true;await managed.revoke();assert.match(managed.error.value,/不要反复提交/);assert.equal(managed.busy.value,false);controls.revokeFail=false;await managed.load();assert.equal(managed.revokes.value[0].state,'unknown');controls.managedListFail=true;managed.revokes.value=[];await managed.load();assert.equal(managed.revokes.value[0].state,'unknown');controls.managedListFail=false;managedProps.blocked=true;const blockedRevokeCount=calls.revocations.length;managed.prepareRevoke(revokeRow);await managed.revoke();await managed.checkRevoke(managed.revokes.value[0]);assert.equal(calls.revocations.length,blockedRevokeCount);managedProps.blocked=false;assert.deepEqual(managedEvents.at(-1),['mutation',false]);
 managed.accountId.value='p';await settle();assert.equal(managed.selectedKind.value,'pan123');assert.equal(managed.canRevoke.value,true);assert.equal(managed.cursors.value[2],'fixture-cursor');const pBefore=calls.revocations.length;managed.prepareRevoke(managed.items.value[0]);assert.equal(managed.confirmTarget.value.id,managed.items.value[0].id);assert.equal(calls.revocations.length,pBefore);managed.cancelRevoke();await managed.load(2);assert.deepEqual(calls.managedNext.at(-1),{accountId:'p',token:'fixture-cursor'});assert.equal(managed.page.value,2);const nextCount=calls.managedNext.length;await managed.load(3);assert.equal(calls.managedNext.length,nextCount);assert.match(managed.error.value,/刷新第一页/);managed.accountId.value='b';await settle();assert.equal(managed.selectedKind.value,'baidu');assert.equal(managed.canRevoke.value,true);assert.equal(managed.detail.value,null);assert.deepEqual(calls.managedLists.at(-1),{accountId:'b',page:1});assert.equal(managed.accountLabel(managedProps.accounts[1]),'百度网盘 · 未命名账号');assert.equal(managed.accountLabel(managedProps.accounts[2]),'123 云盘 · 未命名账号');for(const [account,kind,url,param] of [['b','baidu','https://pan.baidu.com/s/1fixture','pwd'],['g','guangya','https://www.guangyapan.com/s/fixture','code']]){managed.accountId.value=account;await settle();controls.managedReadResult={name:'Official.zip',url,password:'A1b2',expiry:'有效期以网盘显示为准',message:'原记录'};await managed.read(managed.items.value[0]);assert.equal(managed.detailURL.value,url+'?'+param+'=A1b2');await managed.copy(true);assert.equal(calls.reports.at(-1),url+'?'+param+'=A1b2');await managed.copy(false);assert.ok(calls.reports.at(-1).includes('链接：'+url+'?'+param+'=A1b2'));assert.ok(calls.reports.at(-1).includes('提取码：A1b2'));assert.ok(calls.reports.at(-1).includes('有效期以网盘显示为准'));managed.autoFill.value=false;assert.equal(managed.detailURL.value,url);await managed.copy(true);assert.equal(calls.reports.at(-1),url);managed.autoFill.value=true;controls.managedReadResult.password='';await managed.read(managed.items.value[0]);assert.equal(managed.detailURL.value,url);assert.equal(controls.managedReadResult.url,url)}delete controls.managedReadResult;managed.accountId.value='b';await settle();const bBefore=calls.revocations.length;managed.prepareRevoke(managed.items.value[0]);assert.equal(managed.confirmTarget.value.id,managed.items.value[0].id);assert.equal(calls.revocations.length,bBefore);managed.cancelRevoke();
 managed.accountId.value='g';await settle();assert.equal(managed.selectedKind.value,'guangya');assert.equal(managed.accountLabel(managedProps.accounts[3]),'光鸭云盘 · 未命名账号');const gBefore=calls.revocations.length;managed.prepareRevoke({...managed.items.value[0],canRevoke:false});assert.equal(managed.confirmTarget.value,null);assert.equal(calls.revocations.length,gBefore);managed.prepareRevoke(managed.items.value[0]);assert.equal(managed.confirmTarget.value.id,managed.items.value[0].id);managed.cancelRevoke();assert.equal(calls.revocations.length,gBefore);
 const historyComponent=await loadComponent('ShareHistory');const historyProps=reactive({accounts:[{id:'a',kind:'quark'}],blocked:false,refreshKey:'0'});const historyEvents=[];const readyReceipt={accountId:'a',created:'2026-11-17T00:00:00Z',result:{id:'history-ready',kind:'quark',name:'One.zip',state:'ready',url:'https://pan.quark.cn/s/fixture',password:'A1b2',days:7,message:'fixture',canCheck:true,secret:'never-copy-history'}};const unknownReceipt={...readyReceipt,result:{...readyReceipt.result,id:'history-unknown',name:'Two.zip',state:'unknown',url:'',password:'',canCheck:false}};controls.shareHistory=[readyReceipt,unknownReceipt];const history=historyComponent.setup(historyProps,{expose(){},emit(...args){historyEvents.push(args)}});await history.refresh();await settle();assert.equal(history.busy.value,false);assert.equal(history.rows.value.length,2);history.query.value='ONE';assert.equal(history.rows.value.length,1);const readsForLinkSearch=calls.shareHistoryReads;const writesForLinkSearch=calls.shares.length;history.query.value='链接：https://pan.quark.cn/s/fixture?pwd=Other\n提取码：Other';assert.deepEqual(history.rows.value.map(row=>row.result.id),['history-ready']);history.query.value='https://pan.quark.cn/s/Fixture';assert.equal(history.rows.value.length,0);assert.equal(calls.shareHistoryReads,readsForLinkSearch);assert.equal(calls.shares.length,writesForLinkSearch);history.query.value='';history.status.value='unknown';assert.equal(history.rows.value.length,1);history.kind.value='guangya';assert.equal(history.rows.value.length,0);history.kind.value='';history.status.value='';await history.copy(readyReceipt);assert.equal(history.error.value,'');assert.match(calls.reports.at(-1),/One.zip/);assert.doesNotMatch(calls.reports.at(-1),/never-copy|history-ready|accountId/);const historyCopies=calls.reports.length;await history.copy(unknownReceipt);assert.equal(calls.reports.length,historyCopies);const beforeHistoryCreate=calls.shares.length,beforeHistoryChecks=calls.shareChecks.length;controls.holdShareCheck=true;const historyPending=history.check(readyReceipt);await history.check(readyReceipt);assert.equal(calls.shareChecks.length,beforeHistoryChecks+1);assert.deepEqual(historyEvents.at(-1),['mutation',true]);controls.holdShareCheck=false;controls.releaseShareCheck();await historyPending;assert.equal(history.busy.value,false);assert.deepEqual(historyEvents.at(-1),['mutation',false]);assert.equal(calls.shares.length,beforeHistoryCreate);assert.equal(calls.shareChecks.at(-1),'history-ready');historyProps.blocked=true;await history.copy(readyReceipt);assert.equal(calls.reports.length,historyCopies);historyProps.blocked=false;controls.shareCheckFail=true;await history.check(readyReceipt);assert.equal(history.busy.value,false);assert.match(history.error.value,/检查失败/);assert.deepEqual(historyEvents.at(-1),['mutation',false]);controls.shareCheckFail=false;controls.shareHistory=[unknownReceipt];historyProps.refreshKey='1';await settle();assert.equal(history.records.value[0].result.state,'unknown');controls.shareHistory=[readyReceipt,unknownReceipt];await history.refresh();historyProps.blocked=true;const clearsBefore=calls.shareClears??0;await history.clearConfirmed();assert.equal(calls.shareClears??0,clearsBefore);historyProps.blocked=false;controls.shareClearFail=true;await history.clearConfirmed();assert.match(history.error.value,/清理失败/);assert.equal(history.busy.value,false);controls.shareClearFail=false;await history.clearConfirmed();assert.equal(history.records.value.length,1);assert.equal(history.records.value[0].result.state,'unknown');assert.equal(calls.shares.length,beforeHistoryCreate);assert.deepEqual(historyEvents.at(-1),['mutation',false]);controls.shareHistory=[];
 // Review flow: captured inputs, late read cancellation, manual conflict review,
 // and a single enqueue even if a submit button is pressed twice.
 const {useUploadReview}=await import(fileURL('useUploadReview.mjs'));const locks=[],queuedPlans=[];
 const flow=useUploadReview(async(n,skipped)=>{queuedPlans.push({n,skipped})},active=>locks.push(active));
 const draft=[{...file}],draftTargets=[{accountId:'a',folder:'0'}];controls.holdReview=true;
 const canceledCheck=flow.begin(draft,draftTargets);const checksBeforeCancel=calls.uploadChecks.length;
 draft[0].name='changed-after-capture';draftTargets[0].folder='changed-after-capture';assert.equal(flow.pending.value.files[0].name,file.name);assert.equal(flow.pending.value.targets[0].folder,'0');flow.cancel();controls.holdReview=false;controls.releaseReview();await canceledCheck;assert.equal(flow.pending.value,null);assert.equal(queuedPlans.length,0);assert.equal(locks.at(-1),false);assert.equal(calls.uploadChecks.length,checksBeforeCancel);
 controls.reviewConflict=true;await flow.begin([file],[{accountId:'a',folder:'0'}]);assert.ok(flow.pending.value.report);assert.equal(queuedPlans.length,0);
 const reviewComponent=await loadComponent('UploadReview'),reviewEvents=[];
 const reviewProps=reactive({report:flow.pending.value.report,checking:false,submitting:false,error:''});const review=reviewComponent.setup(reviewProps,{expose(){},emit(...args){reviewEvents.push(args)}});assert.equal(review.count.value,0);review.submit();assert.equal(reviewEvents.length,0);
 reviewProps.report={stamp:'fixture',items:[{fileIndex:0,targetIndex:0,name:'conflict',status:'conflict'},{fileIndex:1,targetIndex:0,name:'unknown',status:'unknown'},{fileIndex:2,targetIndex:0,name:'reuse',status:'reuse'},{fileIndex:3,targetIndex:0,name:'ready',status:'ready'}]};await settle();assert.equal(review.count.value,2);review.toggle(reviewProps.report.items[1],true);assert.equal(review.count.value,3);review.submit();assert.deepEqual(reviewEvents.at(-1),['submit',[{fileIndex:0,targetIndex:0}]]);
 controls.holdEnqueue=true;const pendingSubmit=flow.submit([]);const submissions=calls.reviewSubmissions.length;await flow.submit([]);flow.cancel();assert.equal(calls.reviewSubmissions.length,submissions);assert.ok(flow.pending.value);controls.holdEnqueue=false;controls.releaseEnqueue();await pendingSubmit;assert.deepEqual(queuedPlans,[{n:1,skipped:0}]);assert.equal(flow.pending.value,null);controls.reviewConflict=false;
 controls.reviewFail=true;await flow.begin([file],[{accountId:'a',folder:'0'}]);assert.match(flow.error.value,/检查失败/);assert.equal(flow.pending.value.report,null);flow.cancel();controls.reviewFail=false;
 // Failure retry is deliberately across pages but scoped to the selected
 // batch/root; paused and saved entries, and other groups, are excluded.
 const grouped=Array.from({length:121},(_,i)=>({id:'group-'+i,batchId:'batch-scope',accountId:'a',kind:'quark',folder:'0',folderDisplay:'root',status:i===0?'paused':i===1?'success':'failed',sent:0,file:{name:'f'+i+'.txt',relativePath:'tree/f'+i+'.txt',size:12}}));workspace.tasks.value=[...grouped,{...grouped[2],id:'other-group',batchId:'other'}];workspace.clearTaskFilters();await settle();workspace.viewGroup(workspace.groups.value.find(g=>g.tasks.some(t=>t.id==='group-2')).key,true);await settle();assert.equal(workspace.visibleFailures.value.length,119);assert.equal(workspace.taskPageView.value.items.length,50);controls.holdFailedRetry=true;const pendingFailures=workspace.retryFailedTasks();const retries=calls.failedRetries.length;await workspace.retryFailedTasks();assert.equal(calls.failedRetries.length,retries);assert.equal(calls.failedRetries.at(-1).length,119);assert.ok(calls.failedRetries.at(-1).every(id=>id!=='other-group'&&id!=='group-0'&&id!=='group-1'));controls.holdFailedRetry=false;controls.releaseFailedRetry();await pendingFailures;assert.equal(workspace.taskBatchSubmitted.value,119);assert.equal(workspace.taskControlling.value,false);
 const {cleanup}=await import(fileURL('hooks.mjs'));cleanup.forEach(fn=>fn());assert.equal(events.count(),0);
 const recycleComponent=await loadComponent('RecycleBin');const recycleProps=reactive({accounts:[{id:'q',kind:'quark'},{id:'p',kind:'pan123'},{id:'b',kind:'baidu'},{id:'g',kind:'guangya'}],blocked:false,refreshKey:'0'});const recycle=recycleComponent.setup(recycleProps,{expose(){},emit(){}});await settle();assert.equal(recycle.accounts.value.length,4);assert.equal(recycle.items.value[0].id,'9007199254740993123');assert.equal(recycle.sizeText(recycle.items.value[0]),'0 B');assert.equal(recycle.sizeText({...recycle.items.value[0],size:null}),'—');assert.equal(recycle.sizeText({...recycle.items.value[0],kind:'folder'}),'—');recycle.query.value='原目录';assert.equal(recycle.rows.value.length,1);recycle.query.value='missing';assert.equal(recycle.rows.value.length,0);recycle.query.value='';await recycle.load(2);assert.equal(recycle.page.value,2);assert.equal(recycle.items.value.length,0);assert.equal(recycle.hasMore.value,false);controls.recycleFail=true;await recycle.load(1);assert.match(recycle.error.value,/回收站读取失败/);assert.equal(recycle.busy.value,false);assert.equal(recycle.items.value.length,0);controls.recycleFail=false;await recycle.load(1);controls.holdRecycle=true;const staleRecycle=recycle.load(2);const readsRecycle=calls.recycleLists.length;await recycle.load(2);assert.equal(calls.recycleLists.length,readsRecycle);recycle.accountId.value='b';await nextTick();assert.equal(recycle.items.value.length,0);controls.holdRecycle=false;controls.releaseRecycle();await staleRecycle;await settle();assert.equal(calls.recycleLists.at(-1).id,'b');assert.equal(recycle.page.value,1);assert.equal(recycle.items.value[0].name,'b archive.zip');recycleProps.blocked=true;const blockedReads=calls.recycleLists.length;await recycle.load();assert.equal(calls.recycleLists.length,blockedReads);recycleProps.blocked=false;const restoreBefore=calls.recycleRestores?.length??0;await recycle.prepareRestore(recycle.items.value[0]);assert.equal(recycle.confirmTarget.value.id,recycle.items.value[0].id);assert.equal(calls.recycleRestores?.length??0,restoreBefore);await recycle.cancelRestore();assert.equal(recycle.confirmTarget.value,null);assert.equal(calls.recycleRestores?.length??0,restoreBefore);await recycle.prepareRestore(recycle.items.value[0]);controls.holdRecycleRestore=true;const restoring=recycle.restore();await recycle.restore();assert.equal(calls.recycleRestores.length,restoreBefore+1);controls.holdRecycleRestore=false;controls.releaseRecycleRestore();await restoring;assert.equal(recycle.receipts.value[0].state,'unknown');assert.equal(recycle.receiptFor(recycle.items.value[0]).id,'restore-receipt');await recycle.prepareRestore(recycle.items.value[0]);assert.equal(recycle.confirmTarget.value,null);const postBeforeCheck=calls.recycleRestores.length;await recycle.checkRestore(recycle.receipts.value[0]);assert.equal(recycle.receipts.value[0].state,'confirmed');assert.equal(calls.recycleRestores.length,postBeforeCheck);await recycle.clearReceipts();assert.equal(recycle.receipts.value.length,0);assert.equal(calls.recycleRestores.length,postBeforeCheck);controls.recycleHistoryFail=true;await recycle.load();assert.match(recycle.error.value,/回执读取失败/);await recycle.prepareRestore(recycle.items.value[0]);assert.equal(recycle.confirmTarget.value,null);controls.recycleHistoryFail=false;recycleProps.accounts=[];await settle();assert.equal(recycle.accountId.value,'');assert.equal(recycle.items.value.length,0);controls.holdRecycle=true;recycleProps.accounts=[{id:'q',kind:'quark'}];await nextTick();const beforeRecycleUnmount=calls.recycleLists.length;const recycleHooks=await import(fileURL('hooks.mjs'));recycleHooks.cleanup.at(-1)();controls.holdRecycle=false;controls.releaseRecycle();await settle();assert.equal(recycle.items.value.length,0);assert.equal(calls.recycleLists.length,beforeRecycleUnmount);

 {
  const locatingProps=reactive({accounts:[{id:'a',kind:'lanzou'},{id:'b',kind:'baidu'}],blocked:false})
  controls.recycleReceipts=[
   {id:'historical-restore',accountId:'a',state:'confirmed'},
   {id:'pending-restore',accountId:'a',state:'unknown',canCheck:true},
  ]
  const locationEvents=[]
  const locator=recycleComponent.setup(locatingProps,{expose(){},emit(...args){locationEvents.push(args)}})
  await settle()
  const [confirmed,pending]=locator.receipts.value
  const restoreWrites=calls.recycleRestores.length
  await locator.locateRestore(pending)
  assert.equal(calls.recycleLocations,undefined)
  controls.failRecycleLocation=true
  await locator.locateRestore(confirmed)
  delete controls.failRecycleLocation
  assert.match(locator.message.value,/已移动/)
  assert.equal(locationEvents.some(event=>event[0]==='open-location'),false)
  await locator.locateRestore(confirmed)
  assert.deepEqual(calls.recycleLocations.at(-1),{accountId:'a',id:'historical-restore'})
  assert.deepEqual(locationEvents.slice(-2),[['mutation',false],['open-location',{accountId:'a',parentId:'fixture-target'},false]])
  const opened=locationEvents.length
  controls.holdRecycleLocation=true
  const lateLocation=locator.locateRestore(confirmed)
  locator.accountId.value='b'
  await nextTick()
  delete controls.holdRecycleLocation
  controls.releaseRecycleLocation()
  await lateLocation
  assert.equal(locationEvents.slice(opened).some(event=>event[0]==='open-location'),false)
  assert.equal(calls.recycleRestores.length,restoreWrites)
  assert.equal(locator.receipts.value.length,0)
  const routeAccount=workspace.accounts.value[0].id
  workspace.openFileLocation(routeAccount,'fixture-target')
  assert.equal(workspace.tab.value,'files')
  assert.equal(workspace.fileLocation.value.id,'fixture-target')
  assert.equal(workspace.fileLocation.value.accountId,routeAccount)
  assert.equal(workspace.fileLocation.value.breadcrumbs,undefined)
  ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
  console.log('PASS: confirmed recycle receipts navigate to the verified account and directory; pending, missing and stale results never navigate or replay restore.')
 }
 {
  const reviewAccounts=[{id:'a',kind:'quark',name:'同名'},{id:'b',kind:'quark',name:'同名'}]
  const context={expose(){},emit(){}}
  const localReview={kind:'share_settings',accountId:'b',receiptId:'same'}
  controls.shareHistory=[
   {accountId:'a',result:{id:'same',name:'项目',kind:'quark',state:'unknown'}},
   {accountId:'b',result:{id:'same',name:'项目',kind:'quark',state:'unknown'}},
  ]
  const focusedShares=historyComponent.setup({accounts:reviewAccounts,reviewTarget:localReview},context)
  await settle()
  assert.deepEqual(focusedShares.rows.value.map(row=>row.accountId),['b'])
  controls.shareHistory=[]
  controls.revokes=[{accountId:'a',id:'same'},{accountId:'b',id:'same'},{accountId:'b',id:'other'}]
  const beforeShareLists=calls.managedLists.length
  const focusedRevokes=managedComponent.setup({accounts:reviewAccounts,reviewTarget:{accountId:'b',receiptId:'same'}},context)
  await settle()
  assert.equal(calls.managedLists.length,beforeShareLists)
  assert.equal(calls.revokeLoads.at(-1),'b')
  assert.deepEqual(focusedRevokes.visibleRevokes.value.map(row=>[row.accountId,row.id]),[['b','same']])
  focusedRevokes.browseCloudShares()
  await settle()
  assert.equal(calls.managedLists.at(-1).accountId,'b')
  controls.revokes=[]
  controls.recycleReceipts=[{accountId:'a',id:'same'},{accountId:'b',id:'same'},{accountId:'b',id:'other'}]
  const beforeRecycleLists=calls.recycleLists.length
  const focusedRestores=recycleComponent.setup({accounts:reviewAccounts,reviewTarget:{kind:'restore',accountId:'b',receiptId:'same'}},context)
  await settle()
  assert.equal(calls.recycleLists.length,beforeRecycleLists)
  assert.deepEqual(focusedRestores.visibleReceipts.value.map(row=>[row.accountId,row.id]),[['b','same']])
  focusedRestores.browseRecycle()
  await settle()
  assert.equal(calls.recycleLists.at(-1).id,'b')
  const focusedPurgeComponent=await loadComponent('RecyclePurgeHistory')
  controls.purgeRows=[{accountId:'a',id:'same'},{accountId:'b',id:'same'},{accountId:'b',id:'other'}]
  const focusedPurge=focusedPurgeComponent.setup({accountId:'b',receiptId:'same'},context)
  await settle()
  assert.deepEqual(focusedPurge.visible.value.map(row=>[row.accountId,row.id]),[['b','same']])
  controls.purgeRows=[]
  console.log('PASS: pending review opens exact account receipts and enters ordinary cloud listings only on explicit browse.')
 }
 const batchProps=reactive({accounts:[{id:'b',kind:'baidu'}],blocked:false});controls.recycleReceipts=[];const rb=recycleComponent.setup(batchProps,{expose(){},emit(){}});await settle();
 const batchItems=()=>Array.from({length:55},(_,i)=>({id:'batch-'+i,name:'文件'+i,kind:i%2?'folder':'file',originalPath:'/测试/'+i,restoreHandle:'handle-'+i}));
 rb.items.value=batchItems();rb.selectVisible();assert.equal(rb.selectedRows.value.length,50);await rb.prepareBatch();assert.equal(rb.batchConfirm.value.length,50);await rb.cancelBatch();assert.equal(rb.batchConfirm.value.length,0);
 rb.selected.value=['batch-0','batch-1','batch-2'];await rb.prepareBatch();controls.recycleRestoreState='confirmed';let before=calls.recycleRestores.length;await rb.runBatch();assert.equal(calls.recycleRestores.length,before+3);assert.deepEqual(rb.batchResults.value.map(r=>r.state),['confirmed','confirmed','confirmed']);
 rb.items.value=batchItems();rb.selected.value=['batch-0','batch-1'];await rb.prepareBatch();controls.recycleRestoreState='unknown';before=calls.recycleRestores.length;await rb.runBatch();assert.equal(calls.recycleRestores.length,before+1);assert.deepEqual(rb.batchResults.value.map(r=>r.state),['unknown','skipped']);
 rb.items.value=batchItems();rb.selected.value=['batch-0','batch-1'];await rb.prepareBatch();controls.recycleRestoreState='confirmed';controls.holdRecycleRestore=true;before=calls.recycleRestores.length;const pendingBatch=rb.runBatch();await rb.runBatch();assert.equal(calls.recycleRestores.length,before+1);rb.stopRequested.value=true;controls.holdRecycleRestore=false;controls.releaseRecycleRestore();await pendingBatch;assert.deepEqual(rb.batchResults.value.map(r=>r.state),['confirmed','skipped']);
 rb.items.value=batchItems();rb.selected.value=['batch-0','batch-1'];await rb.prepareBatch();controls.recycleRestoreThrow=true;before=calls.recycleRestores.length;await rb.runBatch();assert.equal(calls.recycleRestores.length,before+1);assert.deepEqual(rb.batchResults.value.map(r=>r.state),['unknown','skipped']);controls.recycleRestoreThrow=false;
 rb.receipts.value=[{id:'check-1',name:'待确认1',canCheck:true},{id:'check-2',name:'待确认2',canCheck:true}];controls.recycleReceipts=rb.receipts.value;before=calls.recycleRestores.length;let checks=calls.recycleChecks.length;await rb.runBatch(true);assert.equal(calls.recycleRestores.length,before);assert.equal(calls.recycleChecks.length,checks+2);
 rb.items.value=batchItems();rb.selectVisible();rb.query.value='文件1';await nextTick();assert.equal(rb.selected.value.length,0);batchProps.blocked=true;await rb.prepareBatch();assert.equal(rb.batchConfirm.value.length,0);batchProps.blocked=false;
 rb.query.value='';await nextTick();rb.items.value=batchItems();rb.selected.value=['batch-0','batch-1'];await rb.prepareBatch();controls.holdRecycleRestore=true;const invalidatedBatch=rb.runBatch();rb.invalidate();controls.holdRecycleRestore=false;controls.releaseRecycleRestore();before=calls.recycleRestores.length;await invalidatedBatch;assert.equal(calls.recycleRestores.length,before);assert.equal(rb.batchRunning.value,false);
 const p123props=reactive({accounts:[{id:'b',kind:'pan123'}],blocked:false});controls.recycleReceipts=[];const p123recycle=recycleComponent.setup(p123props,{expose(){},emit(){}});await settle();assert.equal(p123recycle.canRestore.value,true);p123recycle.items.value=batchItems();p123recycle.selected.value=['batch-0','batch-1'];await p123recycle.prepareBatch();assert.equal(p123recycle.batchConfirm.value.length,2);controls.recycleRestoreState='confirmed';before=calls.recycleRestores.length;await p123recycle.runBatch();assert.equal(calls.recycleRestores.length,before+2);assert.deepEqual(p123recycle.batchResults.value.map(r=>r.state),['confirmed','confirmed']);

 // Mixed folder/file groups use read-only whole-group preflight and sequential native moves.
 for(const kind of ['webdav','ctdav','baidu','quark','pan123','guangya']){
  const mp=reactive({accounts:[{id:'mixed',kind}],accountId:'mixed',blocked:false});const mixed=component.setup(mp,{expose(){},emit(){}});await settle();
  const isPath=['webdav','ctdav','baidu'].includes(kind),parent=isPath?'/来源':'10',dest=isPath?'/目标':'40',root=isPath?'/':kind==='guangya'?'root':'0';
  const mixedEntries=[{id:isPath?'/来源/A':'42',name:'A',folder:true,size:-1},{id:isPath?'/来源/B':'43',name:'B',folder:true,size:-1},{id:isPath?'/来源/f.txt':'44',name:'f.txt',folder:false,size:1}];
  const resetMixed=()=>{mixed.page.value={currentId:parent,breadcrumbs:[{id:root,name:'根目录'},{id:parent,name:'来源'}],entries:mixedEntries};mixed.error.value='';mixed.selectedFolderIds.value=mixedEntries.slice(0,2).map(e=>e.id);mixed.selectedIds.value=[mixedEntries[2].id]};
  resetMixed();assert.equal(mixed.canMoveSelection.value,true);mixed.openSelectedMove();assert.equal(mixed.moveTargets.value.length,3);let fw=calls.folderMoves.length,mw=calls.moves.length;await mixed.move(dest,'目标',[{id:root,name:'根目录'},{id:dest,name:'目标'}]);assert.equal(calls.folderMoves.length,fw+2);assert.equal(calls.moves.length,mw+1);assert.equal(calls.movePreflights.at(-1).length,3);assert.equal(mixed.moveResults.value.filter(r=>r.success).length,3);
  resetMixed();mixed.openSelectedMove();controls.failMovePreflight=true;fw=calls.folderMoves.length;mw=calls.moves.length;await mixed.move(dest,'目标');assert.equal(calls.folderMoves.length,fw);assert.equal(calls.moves.length,mw);assert.match(mixed.moveError.value,/整批未提交/);controls.failMovePreflight=false;
  resetMixed();mixed.openSelectedMove();controls.failFolderIds=[mixedEntries[0].id];fw=calls.folderMoves.length;mw=calls.moves.length;await mixed.move(dest,'目标');assert.equal(calls.folderMoves.length,fw+1);assert.equal(calls.moves.length,mw);assert.equal(mixed.moveResults.value.filter(r=>r.skipped).length,2);controls.failFolderIds=[];
  resetMixed();mixed.beginDrag(event(),mixedEntries[1]);assert.equal(mixed.dragSource.value.entries.length,3);assert.equal(mixed.validDestination(mixedEntries[0].id),false);mixed.clearDrag();
  resetMixed();mixed.openSelectedMove();controls.holdMovePreflight=true;fw=calls.folderMoves.length;const held=mixed.move(dest,'目标');await settle();mixed.stopMoving.value=true;controls.holdMovePreflight=false;controls.releaseMovePreflight();await held;assert.equal(calls.folderMoves.length,fw);assert.equal(mixed.moveResults.value.filter(r=>r.skipped).length,3);
 }

 for(const kind of ['baidu','ctdav','webdav','pan123']){
  const cp=reactive({accounts:[{id:'copy-fixture',kind}],accountId:'copy-fixture',blocked:false});const ce=[];const copyState=component.setup(cp,{expose(){},emit(...args){ce.push(args)}});await settle();
  const pan=kind==='pan123',parent=pan?'10':'/来源',dest=pan?'50':'/目标',rootId=pan?'0':'/';
  const trail=[{id:rootId,name:'根目录'},{id:dest,name:'目标'}]
  const originals=[{id:pan?'42':'/来源/A',name:'A',folder:true,size:-1},{id:pan?'43':'/来源/B',name:'B',folder:true,size:-1},{id:pan?'44':'/来源/f.txt',name:'f.txt',folder:false,size:12}];
  const resetCopy=()=>{copyState.page.value={currentId:parent,breadcrumbs:[{id:rootId,name:'根目录'},{id:parent,name:'来源'}],entries:originals};copyState.error.value='';copyState.selectedFolderIds.value=originals.slice(0,2).map(r=>r.id);copyState.selectedIds.value=[originals[2].id]};
  resetCopy();copyState.openSelectedCopy();assert.equal(copyState.fileOperation.value,'copy');assert.equal(copyState.moveTargets.value.length,3);let cw=calls.copies?.length??0,mw=calls.moves.length,fw=calls.folderMoves.length;await copyState.move(dest,'目标',trail);assert.equal(calls.copies.length,cw+3);assert.equal(calls.moves.length,mw);assert.equal(calls.folderMoves.length,fw);assert.equal(copyState.moveResults.value.filter(r=>r.success).length,3);assert.match(copyState.notice.value,/复制结束/);assert.equal(ce.some(e=>e[0]==='renamed'),false);
  if(pan){
   const preflight=calls.pan123CopyPreflights.at(-1);
   assert.deepEqual(preflight,calls.copies.slice(cw));
   assert.deepEqual(preflight.map(row=>row.folder),[true,true,false]);
   assert.deepEqual(preflight[0].input.destinationTrail,trail);
  }
  resetCopy();copyState.openSelectedCopy();controls.failCopyIds=[originals[0].id];cw=calls.copies.length;await copyState.move(dest,'目标',trail);assert.equal(calls.copies.length,cw+1);assert.equal(copyState.moveResults.value.filter(r=>r.skipped).length,2);controls.failCopyIds=[];
  resetCopy();copyState.openSelectedCopy();controls.failMovePreflight=true;cw=calls.copies.length;await copyState.move(dest,'目标',trail);assert.equal(calls.copies.length,cw);controls.failMovePreflight=false;
  resetCopy();copyState.openSelectedCopy();controls.holdCopy=true;cw=calls.copies.length;const copying=copyState.move(dest,'目标',trail);await settle();copyState.stopMoving.value=true;controls.holdCopy=false;controls.releaseCopy();await copying;assert.equal(calls.copies.length,cw+1);assert.equal(copyState.moveResults.value.filter(r=>r.skipped).length,2);
  resetCopy();copyState.openSelectedCopy();controls.holdCopy=true;cw=calls.copies.length;
  const stoppedCopy=copyState.move(dest,'目标',trail);
  await settle();
  controls.copyStopNotReady=true;
  await copyState.stopCurrentCopy();
  assert.match(copyState.copyStopError.value,/可稍后重试/);
  controls.copyStopNotReady=false;
  await copyState.stopCurrentCopy();
  assert.deepEqual(calls.copyStops.at(-1),{accountId:'copy-fixture',entryId:calls.copies.at(-1).input.entryId,destinationId:dest});
  controls.holdCopy=false;controls.releaseCopy();await stoppedCopy;
  assert.equal(calls.copies.length,cw+1);
  assert.equal(copyState.moveResults.value.filter(r=>r.skipped).length,2);
  if(pan){
   resetCopy();
   copyState.openCopy(originals[2]);
   const previous=calls.copies.length;
   await copyState.move(dest,'目标',trail,true);
   assert.equal(calls.copies.length,previous);
   assert.deepEqual(calls.directCopies.at(-1),{folder:false,input:{accountId:'copy-fixture',parentId:parent,destinationId:dest,entryId:'44',name:'f.txt',sourceTrail:[{id:'0',name:'根目录'},{id:parent,name:'来源'}],destinationTrail:trail}});
   assert.match(copyState.notice.value,/复制到：目标$/);
   const files=[originals[2],{id:'45',name:'g.txt',folder:false,size:5}];
   function selectDirectBatch(){
    resetCopy();
    copyState.page.value={...copyState.page.value,entries:files};
    copyState.selectedIds.value=files.map(row=>row.id);
    copyState.selectedFolderIds.value=[];
    copyState.openSelectedCopy();
   }
   selectDirectBatch();
   const priorDirect=calls.directCopies.length;
   controls.failDirectCopyPreflight=true;
   await copyState.move(dest,'目标',trail,true);
   delete controls.failDirectCopyPreflight;
   assert.equal(calls.directCopies.length,priorDirect);
   assert.match(copyState.moveError.value,/整批未提交/);
   selectDirectBatch();
   controls.failDirectCopy=true;
   await copyState.move(dest,'目标',trail,true);
   delete controls.failDirectCopy;
   assert.equal(calls.directCopies.length,priorDirect+1);
   assert.equal(copyState.moveResults.value.at(-1).skipped,true);
   selectDirectBatch();
   await copyState.move(dest,'目标',trail,true);
   assert.deepEqual(calls.directCopies.slice(-2),calls.directCopyPreflights.at(-1));
   assert.equal(calls.copies.length,previous);
   resetCopy();
   copyState.openCopy(originals[0]);
   await copyState.move(dest,'目标',trail,true);
   assert.equal(calls.copies.length,previous);
   assert.equal(calls.directFolderCopies.at(-1).input.entryId,'42');
   assert.equal(calls.directFolderCopies.at(-1).input.destinationId,dest);
   assert.match(copyState.moveResults.value[0].message,/目录结构/);
   resetCopy();
   copyState.openSelectedCopy();
   const fileWrites=calls.directCopies.length,folderWrites=calls.directFolderCopies.length;
   controls.failDirectCopyPreflight=true;
   await copyState.move(dest,'目标',trail,true);
   controls.failDirectCopyPreflight=false;
   assert.equal(calls.directCopies.length,fileWrites);
   assert.equal(calls.directFolderCopies.length,folderWrites);
   assert.equal(copyState.moveResults.value.filter(row=>row.skipped).length,3);
   resetCopy();
   copyState.openSelectedCopy();
   await copyState.move(dest,'目标',trail,true);
   assert.deepEqual(calls.directCopyPreflights.at(-1).map(row=>row.folder),[true,true,false]);
   assert.equal(calls.directCopies.length,fileWrites+1);
   assert.equal(calls.directFolderCopies.length,folderWrites+2);
   assert.equal(calls.copies.length,previous);
  }
  resetCopy();copyState.openMove(originals[0]);assert.equal(copyState.fileOperation.value,'move');copyState.moveTargets.value=[];cp.accounts[0].kind='ctfile';await settle();assert.equal(copyState.canCopy.value,false);assert.equal(copyState.canCopySelection.value,false);
 }

 {
  const qp=reactive({accounts:[{id:'quark-copy',kind:'quark'}],accountId:'quark-copy',blocked:false});
  const events=[];
  const cloud=component.setup(qp,{expose(){},emit(...args){events.push(args)}});
  await settle();
  const folder={id:'tree',name:'Tree',folder:true},file={id:'original',name:'original.txt',folder:false,size:20};
  cloud.page.value={currentId:'source',entries:[folder,file]};
  cloud.error.value='';
  assert.equal(cloud.copyAllows([folder,file]),false);
  assert.equal(cloud.copyAllows([{...file,size:0}]),false);
  const maxFile=2*1024**3,large={...file,size:maxFile};
  assert.equal(cloud.copyAllows([large,{...large,id:'second'}]),true);
  assert.equal(cloud.copyAllows([{...file,size:maxFile+1}]),false);
  assert.equal(cloud.copyAllows([large,{...large,id:'second'},{...file,id:'third',size:1}]),false);
  cloud.openCopy(folder);
  await cloud.move('nested','Tree / Nested',[folder,{id:'nested',name:'Nested'}]);
  assert.equal(events.filter(e=>e[0]==='relayCopy').length,0);
  await cloud.move('backup','备份');
  assert.deepEqual(events.at(-1),['relayCopy',[{sourceAccountId:'quark-copy',targetAccountId:'quark-copy',parentId:'source',destinationId:'backup',entryId:'tree',name:'Tree',size:0}],true]);
  cloud.openCopy(file);
  await cloud.move('backup','备份');
  assert.deepEqual(events.at(-1),['relayCopy',[{sourceAccountId:'quark-copy',targetAccountId:'quark-copy',parentId:'source',destinationId:'backup',entryId:'original',name:'original.txt',size:20}],false]);
 }

 {
  const props=reactive({accounts:[{id:'large-copy',kind:'guangya'}],accountId:'large-copy',blocked:false});
  const browser=component.setup(props,{expose(){},emit(){}});
  await settle();
  const entry={id:'large-file',name:'归档.bin',folder:false,size:2*1024*1024*1024};
  assert.equal(browser.copyAllows([entry]),true);
  assert.equal(browser.copyAllows([{...entry,size:entry.size+1}]),false);
  assert.equal(browser.copyAllows([{...entry,size:null}]),false);
  const folder={id:'copy-folder',name:'资料',folder:true,size:null};
  assert.equal(browser.copyAllows([folder]),true);
  assert.equal(browser.copyAllows([folder,entry]),true);
  assert.equal(browser.copyAllows([folder,{...entry,size:entry.size+1}]),false);
  browser.page.value={currentId:'root',breadcrumbs:[{id:'root',name:'根目录'}],entries:[folder]};
  browser.selectedFolderIds.value=[folder.id];
  browser.openSelectedCopy();
  assert.equal(browser.fileOperation.value,'copy');
  const writeCount=calls.copies.length;
  await browser.move('destination','目标',[{id:'root',name:'根目录'},{id:'destination',name:'目标'}]);
  assert.equal(calls.copies.length,writeCount+1);
  assert.equal(calls.copies.at(-1).folder,true);
  assert.equal(calls.copies.at(-1).input.entryId,folder.id);
  const group=[folder,{id:'copy-second-folder',name:'另一目录',folder:true,size:null},entry];
  const trail=[{id:'root',name:'根目录'},{id:'destination',name:'目标'}];
  function selectGroup(){
   browser.page.value={currentId:'root',breadcrumbs:trail.slice(0,1),entries:group};
   browser.selectedFolderIds.value=group.slice(0,2).map(row=>row.id);
   browser.selectedIds.value=[entry.id];
   browser.openSelectedCopy();
  }
  selectGroup();
  controls.failGuangyaCopyPreflight=true;
  const previousCopies=calls.copies.length;
  await browser.move('destination','目标',trail);
  delete controls.failGuangyaCopyPreflight;
  assert.equal(calls.copies.length,previousCopies);
  assert.match(browser.moveError.value,/整批未提交/);
  selectGroup();
  await browser.move('destination','目标',trail);
  assert.deepEqual(calls.guangyaCopyPreflights.at(-1),calls.copies.slice(previousCopies));
  assert.deepEqual(calls.copies.slice(previousCopies).map(row=>row.folder),[true,true,false]);
  selectGroup();
  await browser.move('destination','目标',trail,true);
  assert.equal(calls.guangyaDirectFolders?.length??0,0);
  browser.moveTargets.value=[];
  browser.openCopy(folder);
  await browser.move('nested','资料 / 子目录',[...trail,folder,{id:'nested',name:'子目录'}],true);
  assert.equal(calls.guangyaDirectFolders?.length??0,0);
  await browser.move('destination','目标',trail,true);
  assert.equal(calls.guangyaDirectFolders.at(-1).input.entryId,folder.id);
  assert.equal(calls.guangyaDirectFolders.at(-1).input.destinationId,'destination');
  assert.match(browser.moveResults.value[0].message,/SHA-256/);
  selectGroup();
  browser.selectedIds.value=[];
  browser.moveTargets.value=[];
  browser.openSelectedCopy();
  const priorDirect=calls.guangyaDirectFolders.length;
  controls.failGuangyaDirectPreflight=true;
  await browser.move('destination','目标',trail,true);
  assert.equal(calls.guangyaDirectFolders.length,priorDirect);
  controls.failGuangyaDirectPreflight=false;
  selectGroup();
  browser.selectedIds.value=[];
  browser.moveTargets.value=[];
  browser.openSelectedCopy();
  await browser.move('destination','目标',trail,true);
  assert.deepEqual(calls.guangyaDirectFolders.slice(priorDirect),calls.guangyaDirectPreflights.at(-1));
  ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)();
 }
 {
  const props=reactive({accounts:[{id:'lz-copy',kind:'lanzou'}],accountId:'lz-copy',blocked:false});
  const browser=component.setup(props,{expose(){},emit(){}});
  await settle();
  const record={id:'123',name:'file.zip',folder:false};
  browser.page.value={currentId:'-1',entries:[record]};browser.error.value='';
  assert.equal(browser.copyAllows([record]),true);
  const folder={id:'126',name:'Folder',folder:true};
  assert.equal(browser.copyAllows([folder]),true);
  const second={...record,id:'124',name:'second.zip'};
  assert.equal(browser.copyAllows([record,second]),true);
  browser.openCopy(record);
  await browser.move('50','目标');
  assert.equal(calls.copies.at(-1).input.accountId,'lz-copy');
  assert.match(browser.moveResults.value[0].message,/SHA256/);
  const selectBatch=()=>{
   browser.page.value={currentId:'-1',entries:[record,second,folder]};browser.error.value='';
   browser.selectedIds.value=[record.id,second.id];
   browser.selectedFolderIds.value=[folder.id];
   browser.openSelectedCopy();
  };
  const priorCopies=calls.copies.length;
  selectBatch();controls.failLanzouCopyPreflight=true;
  await browser.move('50','目标');
  assert.equal(calls.copies.length,priorCopies);
  assert.match(browser.moveError.value,/整批未提交/);
  controls.failLanzouCopyPreflight=false;
  selectBatch();await browser.move('50','目标');
  assert.deepEqual(calls.copies.slice(priorCopies),calls.lanzouCopyPreflights.at(-1));
 }
 const hc=await loadComponent('CopyHistory'),hp=reactive({accountId:'copy-a',refreshKey:'0',blocked:false}),he=[];controls.copyHistoryRows=[{id:'pending-copy',accountId:'copy-a',name:'测试目录',destination:'/target',state:'unknown',canCheck:true},{id:'done-copy',accountId:'copy-a',name:'已完成',destination:'/target',state:'confirmed',canCheck:false}];const hs=hc.setup(hp,{expose(){},emit(...args){he.push(args)}});await settle();assert.equal(hs.rows.value.length,2);assert.equal(hs.pending.value.length,1);const copiedBeforeHistory=calls.copies.length;
 await hs.openDestination(hs.rows.value[0]);
 assert.equal(calls.copyLocations?.length??0,0);
 await hs.openDestination(hs.rows.value[1]);
 assert.deepEqual(he.at(-1),['open','copy-a',{folderId:'/target'}]);
 await hs.openDestination(hs.rows.value[1],true);
 assert.equal(calls.copyResultLocations?.length??0,0);
 await hs.openDestination({...hs.rows.value[1],canOpenResult:true},true);
 assert.deepEqual(he.at(-1),['open','copy-a',{folderId:'copy-folder'}]);
 controls.holdCopyCheck=true;let beforeChecks=calls.copyChecks?.length??0;const checkingHistory=hs.check(hs.pending.value);await hs.check(hs.pending.value);assert.equal(calls.copyChecks.length,beforeChecks+1);controls.holdCopyCheck=false;controls.releaseCopyCheck();await checkingHistory;assert.equal(hs.pending.value.length,0);assert.equal(calls.copies.length,copiedBeforeHistory);assert.equal(he.at(-1)[1],false);
 controls.copyHistoryRows[0].state='unknown';controls.copyHistoryRows[0].canCheck=true;await hs.load();controls.failCopyCheck=true;await hs.check(hs.pending.value);assert.match(hs.message.value,/核对失败/);controls.failCopyCheck=false;controls.failCopyClear=true;await hs.clear();assert.equal(hs.rows.value.length,2);controls.failCopyClear=false;await hs.clear();assert.equal(hs.rows.value.length,1);assert.equal(hs.rows.value[0].state,'unknown');assert.equal(calls.copies.length,copiedBeforeHistory);
 controls.copyHistoryRows=Array.from({length:55},(_,i)=>({id:'history-'+i,accountId:'copy-a',name:'记录'+i,destination:'/target',state:'confirmed',canCheck:false}));await hs.load();assert.equal(hs.visible.value.length,50);hs.page.value=2;assert.equal(hs.visible.value.length,5);hs.query.value='记录54';await nextTick();assert.equal(hs.page.value,1);assert.equal(hs.visible.value.length,1);hs.query.value='';await nextTick();controls.failCopyHistory=true;await hs.load();assert.equal(hs.rows.value.length,0);assert.match(hs.error.value,/读取失败/);controls.failCopyHistory=false;
 controls.holdCopyHistory=true;const staleHistory=hs.load();hp.accountId='copy-b';await nextTick();assert.equal(hs.rows.value.length,0);controls.holdCopyHistory=false;controls.releaseCopyHistory();await staleHistory;await settle();assert.equal(hs.rows.value.length,0);

 const sg=await loadComponent('GlobalSearch'),sp=reactive({accounts:[{id:'search-a',kind:'quark'},{id:'search-b',kind:'baidu'}],blocked:false,refreshKey:'0'}),se=[];const ss=sg.setup(sp,{expose(){},emit(...args){se.push(args)}});
 const searchRows=Array.from({length:55},(_,i)=>({accountId:'search-a',entry:{id:'entry-'+i,name:'资料'+i,folder:i===0,size:12},parentId:'20',path:'根目录 / docs / 资料'+i,trail:[{id:'0',name:'根目录'},{id:'20',name:'docs'}]}));controls.searchResult={state:'completed',items:searchRows};ss.query.value='资料';ss.accountId.value='*';ss.includePath.value=true;await ss.start();assert.deepEqual(calls.searches.at(-1),{accountIds:['search-a','search-b'],query:'资料',includePath:true,firstMatchPerAccount:true});assert.equal(ss.visible.value.length,50);ss.page.value=2;assert.equal(ss.visible.value.length,5);ss.kind.value='folders';await nextTick();assert.equal(ss.page.value,1);assert.equal(ss.rows.value.length,1);ss.open(ss.rows.value[0]);assert.equal(se.at(-1)[0],'open');assert.equal(se.at(-1)[1].parentId,'20');sp.blocked=true;const emittedSearch=se.length;ss.open(ss.rows.value[0]);assert.equal(se.length,emittedSearch);sp.blocked=false;
 controls.searchResult={state:'running',items:[searchRows[1]]};await ss.start();assert.equal(ss.busy.value,true);await ss.stop();assert.equal(calls.searchCancels.at(-1),ss.status.value.id);controls.searchResult.state='cancelled';await new Promise(r=>setTimeout(r,160));assert.equal(ss.busy.value,false);assert.equal(ss.status.value.items.length,1);
 controls.holdSearchStart=true;const pendingSearch=ss.start();sp.refreshKey='1';await nextTick();controls.holdSearchStart=false;controls.releaseSearchStart();await pendingSearch;assert.equal(ss.status.value,null);assert.ok(calls.searchCancels.includes('search-3'));
 controls.searchResult={state:'running',items:[]};controls.failSearchPoll=true;await ss.start();await new Promise(r=>setTimeout(r,160));assert.match(ss.error.value,/搜索读取失败/);assert.equal(ss.busy.value,false);controls.failSearchPoll=false;
 sp.accounts=Array.from({length:21},(_,i)=>({id:'many-'+i,kind:'quark'}));await nextTick();ss.accountId.value='*';const beforeManySearches=calls.searches.length;await ss.start();assert.equal(calls.searches.length,beforeManySearches);assert.match(ss.error.value,/20个/);const sh=await import(fileURL('hooks.mjs'));sh.cleanup.at(-1)();
 const lp=reactive({accounts:[{id:'location-a',kind:'quark'},{id:'location-b',kind:'quark'}],accountId:'location-a',blocked:false,locationRequest:{accountId:'location-a',id:'20',breadcrumbs:[{id:'0',name:'根目录'},{id:'20',name:'docs'}],nonce:1}});const ls=component.setup(lp,{expose(){},emit(){}});await settle();assert.equal(ls.page.value.currentId,'20');assert.deepEqual(ls.page.value.breadcrumbs.map(r=>r.id),['0','20']);
 ls.singleResult.value={path:'C:/Downloads/saved.txt',bytes:1};ls.downloadOrigin.value='根目录 / docs';
 lp.locationRequest={...lp.locationRequest,id:'30',nonce:2};await settle();
 assert.equal(ls.page.value.currentId,'30');assert.equal(ls.singleResult.value?.path,'C:/Downloads/saved.txt');
 assert.equal(ls.downloadOrigin.value,'根目录 / docs');
 lp.accountId='location-b';await settle();assert.equal(ls.page.value.currentId,'0');assert.equal(ls.singleResult.value,null);
 sh.cleanup.at(-1)();
 console.log('PASS: global search all-account input, path mode, pagination/type filters, result navigation, stop retains partial, stale start cancelled, read error and account limits.');
 console.log('PASS: persistent copy history UI, only-read checks, duplicate guard, clear retains unknown, failed check visible, pagination/filtering, read failure and account isolation.');
 console.log('PASS: four-provider mixed copy, 123 whole-batch preflight, no MOVE, stop on failure, and unsupported provider guards.');
 console.log('PASS: six-provider mixed folder batches, preflight before writes, rejection without writes, error stops remainder, mixed drag and stopping during preflight.');
 const quarkRecycleProps=reactive({accounts:[{id:'q',kind:'quark'}],blocked:false});
 controls.recycleReceipts=[];
 const qr=recycleComponent.setup(quarkRecycleProps,{expose(){},emit(){}});
 await settle();
 assert.equal(qr.canRestore.value,true);assert.equal(qr.eligible.value.length,0);
 qr.items.value=batchItems().slice(0,3);
 qr.selected.value=['batch-0','batch-1'];
 qr.queueSelected();
 await qr.prepareBatch(undefined,true);
 before=calls.recycleRestores.length;
 await qr.checkBatchPlan();
 assert.equal(qr.batchPlan.value.passed,true);
 assert.equal(qr.batchConfirm.value.length,2);
 assert.equal(calls.recycleRestores.length,before);
 controls.failRecyclePreflight=true;
 await qr.checkBatchPlan();
 assert.equal(qr.batchPlan.value.passed,false);
 assert.equal(qr.batchConfirm.value.length,2);
 assert.equal(qr.restoreQueue.value.length,2);
 await qr.runBatch();
 assert.equal(calls.recycleRestores.length,before);
 assert.deepEqual(calls.recyclePreflights.at(-1),['handle-0','handle-1']);
 assert.deepEqual(qr.batchResults.value.map(r=>r.state),['skipped','skipped']);
 assert.match(qr.message.value,/整批预检查未通过.*冲突/);
 assert.deepEqual(qr.restoreQueue.value.map(row=>row.entry.id),['batch-0','batch-1']);
 controls.failRecyclePreflight=false;
 qr.items.value=batchItems().slice(0,3);qr.selected.value=['batch-0','batch-1'];await qr.prepareBatch();controls.holdRecyclePreflight=true;const stoppedPreflight=qr.runBatch();await settle();assert.match(qr.batchPhase.value,/检查整批/);before=calls.recycleRestores.length;qr.stopRequested.value=true;controls.holdRecyclePreflight=false;controls.releaseRecyclePreflight();await stoppedPreflight;assert.equal(calls.recycleRestores.length,before);assert.ok(qr.batchResults.value.every(r=>r.state==='skipped'));assert.equal(qr.restoreQueue.value.length,2);assert.deepEqual([...qr.selected.value],['batch-0','batch-1']);
 qr.items.value=batchItems().slice(0,3);qr.selected.value=['batch-0','batch-1'];await qr.prepareBatch();controls.recycleRestoreState='confirmed';before=calls.recycleRestores.length;await qr.runBatch();assert.equal(calls.recycleRestores.length,before+2);assert.deepEqual(qr.batchResults.value.map(r=>r.state),['confirmed','confirmed']);assert.equal(qr.restoreQueue.value.length,0);const preflightReads=calls.recyclePreflights.length;qr.receipts.value=[{id:'quark-check',name:'待核对',canCheck:true}];controls.recycleReceipts=qr.receipts.value;before=calls.recycleRestores.length;await qr.runBatch(true);assert.equal(calls.recyclePreflights.length,preflightReads);assert.equal(calls.recycleRestores.length,before);recycleHooks.cleanup.at(-1)();
 console.log('PASS: recycle read-only plan preview, retained queues when no writes were sent, three-provider whole-selection preflight, Quark incomplete metadata read-only, conflict without writes, stopping during preflight, mixed restore, checks without resubmission, cancellation/sequencing/error/invalidation regressions.');
 const trashDialog=await loadComponent('TrashDialog');
 const trashInputs=[{accountId:'a',parentId:'10',entry:{id:'11',name:'first.txt',folder:false,size:12}},{accountId:'a',parentId:'10',entry:{id:'12',name:'folder',folder:true,size:0}}];
 function trashSetup(){const events=[];return {events,s:trashDialog.setup({inputs:trashInputs,accountName:'fixture',location:'根目录 / 资料'},{expose(){},emit(...args){events.push(args)}})}}
 calls.trashes=[];calls.trashPrepares=[];let td=trashSetup();await td.s.start();assert.equal(calls.trashPrepares.length,0);td.s.agreed.value=true;await td.s.start();assert.deepEqual(calls.trashes,['11','12']);assert.equal(td.s.results.value.length,2);await td.s.start();assert.equal(calls.trashes.length,2);
 for(const flag of ['failTrashPrepare','nullTrashPrepare']){controls[flag]=true;td=trashSetup();td.s.agreed.value=true;const n=calls.trashes.length;await td.s.start();assert.equal(calls.trashes.length,n);assert.ok(td.s.error.value);controls[flag]=false}
 controls.trashState='unknown';td=trashSetup();td.s.agreed.value=true;await td.s.start();assert.deepEqual(td.s.results.value.map(r=>r.state),['unknown','skipped']);controls.trashState='confirmed';
 controls.holdTrash=true;td=trashSetup();td.s.agreed.value=true;const trashRunning=td.s.start();await settle();assert.equal(td.s.busy.value,true);const writesAtStop=calls.trashes.length;td.s.stop.value=true;await td.s.start();controls.holdTrash=false;controls.releaseTrash();await trashRunning;assert.equal(calls.trashes.length,writesAtStop);assert.deepEqual(td.s.results.value.map(r=>r.state),['confirmed','skipped']);
 controls.throwTrash=true;td=trashSetup();td.s.agreed.value=true;await td.s.start();assert.match(td.s.error.value,/中断/);assert.equal(td.events.filter(r=>r[0]==='changed').length,1);controls.throwTrash=false;
 controls.holdTrashPrepare=true;td=trashSetup();td.s.agreed.value=true;const disposedTrash=td.s.start();await settle();const beforeDispose=calls.trashes.length;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)();controls.holdTrashPrepare=false;controls.releaseTrashPrepare();await disposedTrash;assert.equal(calls.trashes.length,beforeDispose);
 const trashHistory=await loadComponent('TrashHistory');controls.trashRows=[{id:'unknown-trash',accountId:'a',name:'pending',original:'root',state:'unknown',canCheck:true},...Array.from({length:54},(_,i)=>({id:'trash-'+i,accountId:'a',name:'item'+i,original:'root',state:'confirmed',canCheck:false}))];const th=trashHistory.setup({accountId:'a',blocked:false},{expose(){},emit(){}});await settle();assert.equal(th.visible.value.length,50);th.page.value=2;assert.equal(th.visible.value.length,5);await th.check(th.pending.value);assert.deepEqual(calls.trashChecks,['unknown-trash']);assert.equal(th.pending.value.length,0);controls.failTrashClear=true;await th.clear();assert.match(th.message.value,/保存失败/);controls.failTrashClear=false;await th.clear();assert.equal(th.rows.value.length,0);
 const trashBrowserProps=reactive({accounts:[{id:'trash-a',name:'test123',kind:'pan123',folder:'0'}],accountId:'trash-a',blocked:false,refreshKey:0});const trashBrowser=component.setup(trashBrowserProps,{expose(){},emit(){}});await settle();assert.equal(trashBrowser.canTrash.value,true);trashBrowser.openTrash([trashBrowser.page.value.entries[1]]);assert.equal(trashBrowser.trashTarget.value.length,1);assert.equal(trashBrowser.canNavigate.value,false);assert.equal(trashBrowser.canDrop.value,false);trashBrowser.closeTrash();assert.equal(trashBrowser.canDrop.value,true);trashBrowser.openTrash(Array(51).fill(trashBrowser.page.value.entries[1]));assert.equal(trashBrowser.trashTarget.value,null);trashBrowser.openTrash([trashBrowser.page.value.entries[0]]);trashBrowserProps.refreshKey++;await settle();assert.equal(trashBrowser.trashTarget.value,null);trashBrowserProps.accounts[0].kind='quark';await settle();assert.equal(trashBrowser.canTrash.value,true);trashBrowser.openTrash(trashBrowser.page.value.entries.slice(0,2));assert.equal(trashBrowser.trashTarget.value.length,2);trashBrowser.closeTrash();trashBrowserProps.accounts[0].kind='guangya';await settle();assert.equal(trashBrowser.canTrash.value,false);(await import(fileURL('hooks.mjs'))).cleanup.at(-1)();
 const purgeComponent=await loadComponent('RecyclePurgeDialog'),purgeEntries=[{id:'purge-file',name:'disposable.txt',kind:'file',originalPath:'/old/file',size:0,restoreHandle:''},{id:'purge-folder',name:'disposable folder',kind:'folder',originalPath:'/old/folder',size:0,restoreHandle:''}];
 function purgeSetup(){const events=[];return {events,s:purgeComponent.setup({accountId:'fixture',accountName:'fixture account',page:1,entries:purgeEntries},{expose(){},emit(...args){events.push(args)}})}}
 calls.purges=[];let pd=purgeSetup();await pd.s.start();pd.s.confirmation.value='永久删除';await pd.s.start();pd.s.agreed.value=true;pd.s.confirmation.value=' 永久删除';await pd.s.start();assert.equal(calls.purges.length,0);pd.s.confirmation.value='永久删除';await pd.s.start();assert.deepEqual(calls.purges,[{handle:'purge-file',confirmation:'永久删除'},{handle:'purge-folder',confirmation:'永久删除'}]);await pd.s.start();assert.equal(calls.purges.length,2);
 for(const flag of ['failPurgePreflight','nullPurgePreflight']){pd=purgeSetup();pd.s.agreed.value=true;pd.s.confirmation.value='永久删除';controls[flag]=true;before=calls.purges.length;await pd.s.start();assert.equal(calls.purges.length,before);assert.ok(pd.s.error.value);controls[flag]=false}
 pd=purgeSetup();pd.s.agreed.value=true;pd.s.confirmation.value='永久删除';controls.holdPurgePreflight=true;const stopBeforePurge=pd.s.start();await settle();before=calls.purges.length;pd.s.stop.value=true;controls.holdPurgePreflight=false;controls.releasePurgePreflight();await stopBeforePurge;assert.equal(calls.purges.length,before);assert.ok(pd.s.results.value.every(r=>r.state==='skipped'));
 controls.purgeState='unknown';pd=purgeSetup();pd.s.agreed.value=true;pd.s.confirmation.value='永久删除';await pd.s.start();assert.deepEqual(pd.s.results.value.map(r=>r.state),['unknown','skipped']);controls.purgeState='confirmed';
 controls.holdPurge=true;pd=purgeSetup();pd.s.agreed.value=true;pd.s.confirmation.value='永久删除';const currentPurge=pd.s.start();await settle();before=calls.purges.length;pd.s.stop.value=true;await pd.s.start();controls.holdPurge=false;controls.releasePurge();await currentPurge;assert.equal(calls.purges.length,before);assert.deepEqual(pd.s.results.value.map(r=>r.state),['confirmed','skipped']);
 controls.throwPurge=true;pd=purgeSetup();pd.s.agreed.value=true;pd.s.confirmation.value='永久删除';await pd.s.start();assert.match(pd.s.error.value,/中断/);assert.deepEqual(pd.events,[['changed']]);controls.throwPurge=false;
 assert.deepEqual(pd.s.results.value.map(r=>r.state),['unknown','skipped'])
 {
  const reportComponent=await loadComponent('BatchOperationReport')
  const reportProps=reactive({operation:'永久删除回收站项目',accountName:'测试网盘',rows:[
   {name:'文件一\n第二行',state:'confirmed',message:'private server failure',handle:'private handle'},
   {name:'文件二',state:'unknown',url:'https://private.example/s/link',password:'private passcode'},
   {name:'文件三',state:'skipped'},
  ],disabled:true})
  const r=reportComponent.setup(reportProps,{expose(){},emit(){}})
  const earlierReports=calls.reports?.length??0
  const earlierPurges=calls.purges.length
  await r.copy()
  assert.equal(calls.reports?.length??0,earlierReports)
  reportProps.disabled=false
  controls.copyFail=true
  await r.copy()
  assert.match(r.feedback.value,/无法写入/)
  delete controls.copyFail
  await r.copy()
  assert.equal(calls.reports.length,earlierReports+1)
  assert.deepEqual(calls.reports.at(-1).split('\r\n'),['永久删除回收站项目','测试网盘','已确认 1 项 · 待核对 1 项 · 未执行 1 项','','已确认 · 文件一 第二行','待核对 · 文件二','未执行 · 文件三'])
  assert.equal(calls.purges.length,earlierPurges)
  ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
 }
 controls.holdPurgePreflight=true;pd=purgeSetup();pd.s.agreed.value=true;pd.s.confirmation.value='永久删除';const disposeBeforePurge=pd.s.start();await settle();before=calls.purges.length;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)();controls.holdPurgePreflight=false;controls.releasePurgePreflight();await disposeBeforePurge;assert.equal(calls.purges.length,before);
 const purgeHistory=await loadComponent('RecyclePurgeHistory');controls.purgeRows=[{id:'no-ack',accountId:'fixture',name:'disposable',original:'/old',state:'unknown',canCheck:true}];const ph=purgeHistory.setup({accountId:'fixture',blocked:false},{expose(){},emit(){}});await settle();assert.equal(ph.canClear.value,false);controls.noPurgeAck=true;before=calls.purges.length;await ph.check(ph.pending.value);assert.equal(calls.purges.length,before);assert.equal(ph.rows.value[0].state,'unknown');assert.equal(ph.canClear.value,false);controls.noPurgeAck=false;await ph.check(ph.pending.value);assert.equal(ph.rows.value[0].state,'confirmed');controls.failPurgeClear=true;await ph.clear();assert.match(ph.message.value,/保存失败/);controls.failPurgeClear=false;await ph.clear();assert.equal(ph.rows.value.length,0);
 const bluePurge=purgeComponent.setup({accountId:'blue-preview',accountName:'蓝奏',page:1,entries:[purgeEntries[1]],lanzou:true},{expose(){},emit(){}})
 bluePurge.agreed.value=true
 bluePurge.confirmation.value='永久删除'
 before=calls.purges.length
 await bluePurge.start();assert.equal(calls.purges.length,before)
 controls.failPurgePreflight=true
 await bluePurge.preview();assert.equal(bluePurge.reviewed.value,null)
 controls.failPurgePreflight=false
 await bluePurge.preview();assert.deepEqual(bluePurge.reviewed.value[0].folderFiles,['完整文件名.txt']);assert.equal(calls.purges.length,before)
 const bluePreparationCount=calls.purgePreflights.length
 await bluePurge.start()
 assert.equal(calls.purgePreflights.length,bluePreparationCount)
 assert.equal(calls.purges.length,before+1)
 const purgeBrowserProps=reactive({accounts:[{id:'q',kind:'quark'}],blocked:false});const pb=recycleComponent.setup(purgeBrowserProps,{expose(){},emit(){}});await settle();pb.prepareEmpty();assert.equal(pb.emptyTarget.value.accountId,'q');assert.equal(pb.locked.value,true);await pb.closeEmpty();assert.equal(pb.emptyTarget.value,null);pb.prepareEmpty();pb.invalidate();assert.equal(pb.emptyTarget.value,null);await settle();pb.selectionMode.value='purge';await settle();pb.items.value=[...purgeEntries,{id:'unknown-kind',name:'unknown',kind:'unknown',restoreHandle:''}];pb.selectVisible();assert.equal(pb.selectedRows.value.length,2);pb.preparePurge(pb.selectedRows.value);assert.equal(pb.purgeTarget.value.entries.length,2);assert.equal(pb.locked.value,true);pb.purgeChanged();assert.equal(pb.selected.value.length,0);const beforePurgeRefresh=calls.recycleLists.length;await pb.closePurge();assert.equal(calls.recycleLists.length,beforePurgeRefresh+1);assert.equal(pb.locked.value,false);pb.selectionMode.value='restore';await settle();assert.equal(pb.selected.value.length,0);pb.preparePurge(purgeEntries);pb.invalidate();assert.equal(pb.purgeTarget.value,null);(await import(fileURL('hooks.mjs'))).cleanup.at(-1)();
 const guangyaRecycle=recycleComponent.setup(reactive({accounts:[{id:'g',kind:'guangya'}],blocked:false}),{expose(){},emit(){}})
 await settle()
 assert.equal(guangyaRecycle.canPurge.value,true)
 assert.equal(guangyaRecycle.canEmpty.value,true)
 guangyaRecycle.selectionMode.value='purge';await settle()
 guangyaRecycle.selectVisible()
 assert.equal(guangyaRecycle.selectedRows.value.length,1)
 guangyaRecycle.preparePurge(guangyaRecycle.selectedRows.value)
 assert.equal(guangyaRecycle.purgeTarget.value.accountId,'g')
 assert.equal(guangyaRecycle.purgeTarget.value.entries[0].name,'g archive.zip')
 assert.equal(guangyaRecycle.emptyTarget.value,null)
 await guangyaRecycle.closePurge()
 const blueProps=reactive({accounts:[{id:'lz',kind:'lanzou'}],blocked:false})
 controls.recycleHistoryFail=true
 const blue=recycleComponent.setup(blueProps,{expose(){},emit(){}});await settle()
 assert.match(blue.error.value,/回执读取失败/);assert.equal(blue.items.value[0].name,'lz archive.zip')
 assert.equal(blue.canRestore.value,true);assert.equal(blue.eligible.value.length,0)
 blue.prepareEmpty();blue.preparePurge(purgeEntries);await blue.prepareRestore({...purgeEntries[0],restoreHandle:'synthetic'})
 assert.equal(blue.emptyTarget.value,null);assert.equal(blue.purgeTarget.value,null);assert.equal(blue.confirmTarget.value,null)
 controls.recycleHistoryFail=false
 await blue.load()
 const blueFile={...blue.items.value[0],id:'file:42',restoreHandle:'fixture-blue',parentId:'-1'}
 const blueFolder={...blueFile,id:'folder:42',kind:'folder',name:'folder',parentId:'12',originalPath:'原目录 ID 12 / folder'}
 const webBlueFile={...blueFile,id:'file:44',restoreHandle:'fixture-blue-web',parentId:'',originalPath:''}
 await blue.prepareRestore(webBlueFile)
 assert.equal(blue.confirmTarget.value.restoreHandle,'fixture-blue-web')
 assert.equal(blue.restorePath(webBlueFile),'根目录 / lz archive.zip')
 {
  const writes=calls.recycleRestores.length
  controls.failLanzouRestorePreview=true
  await blue.checkLanzouRestore()
  assert.match(blue.restoreOriginError.value,/根目录/)
  assert.equal(blue.lanzouRestorePreview.value,null)
  controls.failLanzouRestorePreview=false
  controls.holdLanzouRestorePreview=true
  const pendingPreview=blue.checkLanzouRestore()
  await blue.checkLanzouRestore()
  assert.deepEqual(calls.lanzouRestorePreviews,['fixture-blue-web','fixture-blue-web'])
  controls.holdLanzouRestorePreview=false
  controls.releaseLanzouRestorePreview()
  await pendingPreview
  assert.deepEqual(blue.lanzouRestorePreview.value,{evidence:'root',files:[]})
  assert.equal(blue.restoreOriginError.value,'')
  assert.equal(calls.recycleRestores.length,writes)
 }
 await blue.cancelRestore()
 assert.equal(blue.canPurgeRow(webBlueFile),false)
 assert.equal(blue.restorePath(blueFile),'根目录 / lz archive.zip')
 assert.equal(blue.restorePath(blueFolder),blueFolder.originalPath)
 await blue.prepareRestore(blueFile);assert.equal(blue.confirmTarget.value.originalPath,blueFile.originalPath);await blue.cancelRestore()
 assert.equal(blue.canEmpty.value,false)
 blue.selectionMode.value='purge';await settle()
 const unverifiedBlue={...blueFile,id:'file:43',restoreHandle:''}
 blue.items.value=[blueFile,blueFolder,unverifiedBlue,webBlueFile]
 blue.selectVisible()
 assert.deepEqual(blue.selectedRows.value.map(r=>r.id),['file:42','folder:42'])
 blue.preparePurge([blueFolder,unverifiedBlue]);assert.equal(blue.purgeTarget.value,null)
 blue.preparePurge(blue.selectedRows.value);assert.deepEqual(blue.purgeTarget.value.entries.map(r=>r.id),['file:42','folder:42'])
 await blue.closePurge()
 {
  const webFolder={...blueFolder,id:'folder:48',name:'网页回收目录',restoreHandle:'',parentId:'',originalPath:''}
  blue.items.value=[...blue.items.value,webFolder]
  const row=blue.rows.value.at(-1)
  const beforePreview=calls.purgePreflights.length,beforeWrites=calls.purges.length
  blue.previewLanzouRowPurge({...row,id:'folder:49'})
  assert.equal(blue.purgeTarget.value,null)
  blue.previewLanzouRowPurge(row)
  assert.equal(blue.purgeTarget.value.page,0)
  assert.equal(blue.purgeTarget.value.autoPreview,true)
  assert.equal(calls.purgePreflights.length,beforePreview)
  controls.failPurgePreflight=true
  const preview=purgeComponent.setup({...blue.purgeTarget.value,lanzou:true},{expose(){},emit(){}})
  await settle()
  assert.deepEqual(calls.purgePreflights.at(-1),{accountId:blue.accountId.value,page:0,entries:blue.purgeTarget.value.entries})
  assert.equal(preview.reviewed.value,null)
  assert.ok(preview.error.value)
  preview.agreed.value=true
  preview.confirmation.value='永久删除'
  await preview.start()
  assert.equal(calls.purges.length,beforeWrites)
  controls.failPurgePreflight=false
  await preview.preview()
  assert.equal(preview.reviewed.value.length,1)
  assert.equal(calls.purges.length,beforeWrites)
  ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
  await blue.closePurge()
  blue.previewLanzouRowPurge(blue.rows.value.find(row=>row.id===webBlueFile.id))
  assert.equal(blue.purgeTarget.value.entries[0].restoreHandle,'')
  await blue.closePurge()
  blue.listBrowseOnly.value=true
  blue.previewLanzouRowPurge(row)
  assert.equal(blue.purgeTarget.value,null)
  blue.listBrowseOnly.value=false
 }
 blue.receipts.value=[{entryId:blueFile.id,state:'unknown'}]
 assert.equal(blue.canPurgeRow(blueFile),false)
 await blue.openLanzouRecycle();assert.equal(new URL(events.openedURLs.at(-1)).hostname,'up.woozooo.com')
 ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
 console.log('PASS: permanent recycle deletion guards, selection and refresh isolation, and LanZou verified-item purge, restore destinations and website entry.');
 const folderPreview=await loadComponent('RecycleFolderPreview')
 const inventoryNames=Array.from({length:52},(_,i)=>'原始完整文件名'+i+'.txt')
 controls.lanzouFolderPreview={remoteFiles:['原始…'],originalFiles:inventoryNames,hasOriginal:true,comparison:{matching:1,originalOnly:51,currentOnly:0}}
 const inventory=folderPreview.setup({accountId:'lz',accountName:'蓝奏',entryId:'folder:42',name:'资料'},{expose(){},emit(){}})
 await settle()
 assert.deepEqual(inventory.visible.value,['原始…'])
 assert.equal(inventory.comparison.value.matching,1)
 inventory.source.value='original'
 await settle()
 assert.equal(inventory.pageCount.value,2)
 inventory.page.value=2
 assert.deepEqual(inventory.visible.value,inventoryNames.slice(50))
 inventory.query.value='文件名51'
 await settle()
 assert.equal(inventory.page.value,1)
 assert.deepEqual(inventory.visible.value,[inventoryNames[51]])
 controls.lanzouFolderPreview={remoteFiles:[],originalFiles:null,hasOriginal:false}
 await inventory.load()
 assert.equal(inventory.original.value,null)
 assert.equal(inventory.comparison.value,null)
 assert.equal(inventory.source.value,'remote')
 assert.deepEqual(inventory.names.value,[])
 ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
 console.log('PASS: LanZou folder preview separates current names from local inventory and handles legacy receipts.')
 const folderShares=await loadComponent('LanzouFolderShares')
 const shareRoot={id:'-1',name:'根目录'}, shareFolder={id:'20',name:'项目资料',folder:true}, shareFile={id:'20',name:'同 ID 文件.txt',folder:false}
 controls.lanzouSharePages={
  '-1':{currentId:'-1',breadcrumbs:[shareRoot],entries:[shareFolder]},
  '20':{currentId:'20',breadcrumbs:[shareRoot,shareFolder],entries:[shareFile]},
 }
 const folderShareProps=reactive({accounts:[{id:'blue-share-a',kind:'lanzou',name:'同名'},{id:'blue-share-b',kind:'lanzou',name:'同名'}],blocked:false})
 const folderEvents=[]
 const shareBrowser=folderShares.setup(folderShareProps,{expose(){},emit(...args){folderEvents.push(args)}})
 await settle()
 const websitesBeforeBlue=events.openedURLs.length
 await shareBrowser.openWebsite()
 assert.deepEqual(events.openedURLs.slice(websitesBeforeBlue),['https://up.woozooo.com/'])
 folderShareProps.blocked=true
 await shareBrowser.openWebsite()
 folderShareProps.blocked=false
 await settle()
 assert.equal(events.openedURLs.length,websitesBeforeBlue+1)
 shareBrowser.goToFolder('20')
 await settle()
 assert.deepEqual(shareBrowser.trail.value.map(row=>row.id),['-1','20'])
 shareBrowser.openItem(shareBrowser.rows.value[0])
 assert.equal(shareBrowser.single.value.editPassword,false)
 assert.equal(shareBrowser.single.value.parentId,'20')
 assert.equal(shareBrowser.single.value.accountName,'蓝奏云 · 同名（1）')
 assert.equal(shareBrowser.single.value.entry.folder,false)
 shareBrowser.goToFolder('-1')
 assert.equal(shareBrowser.folderId.value,'20')
 shareBrowser.closeLinks()
 shareBrowser.openItem(shareBrowser.rows.value[0],true)
 assert.equal(shareBrowser.single.value.editPassword,true)
 const singleShare=await loadComponent('ShareDialog')
 const edit=singleShare.setup({...shareBrowser.single.value,kind:'lanzou'},{expose(){},emit(){}})
 const shareWrites=calls.shares?.length??0
 assert.equal(edit.modify.value,true)
 assert.equal(calls.shares?.length??0,shareWrites)
 edit.password.value='New123'
 await edit.submit()
 assert.equal(calls.shares.at(-1).action,'password')
 assert.equal(calls.shares.at(-1).accountId,'blue-share-a')
 assert.equal(calls.shares.at(-1).parentId,'20')
 assert.equal(calls.shares.at(-1).folder,false)
 const clearSingle=singleShare.setup({...shareBrowser.single.value,kind:'lanzou'},{expose(){},emit(){}})
 clearSingle.access.value='clear'
 const beforeSingleClear=calls.shares.length
 await clearSingle.submit()
 assert.equal(calls.shares.length,beforeSingleClear)
 clearSingle.confirmPublic.value=true
 await clearSingle.submit()
 assert.equal(calls.shares.length,beforeSingleClear+1)
 assert.equal(calls.shares.at(-1).clearPassword,true)
 assert.equal(calls.shares.at(-1).password,'')
 shareBrowser.accountId.value='blue-share-b'
 await settle()
 assert.equal(shareBrowser.single.value,null)
 assert.equal(shareBrowser.folderId.value,'-1')
 assert.deepEqual(folderEvents.at(-1),['mutation',false])
 shareBrowser.openLinks()
 assert.deepEqual(shareBrowser.batch.value.entries.map(row=>row.id),['20'])
 assert.deepEqual(shareBrowser.batch.value.initialSelection,[])
 shareBrowser.closeLinks()
 const nestedShareFolder={id:'30',name:'归档'}
 const nestedShareTrail=[shareRoot,shareFolder,nestedShareFolder]
 const foundShareEntry={...shareFile,name:'归档项目'}
 const foundLocation={accountId:'blue-share-b',parentId:'30',path:'根目录 / 资料 / 归档 / 归档项目',trail:nestedShareTrail,entry:foundShareEntry}
 controls.lanzouSharePages['30']={
  currentId:'30',breadcrumbs:nestedShareTrail,
  entries:[{...foundShareEntry,folder:true},...Array.from({length:49},(_,i)=>({id:String(100+i),name:'其他 '+i,folder:false})),foundShareEntry],
 }
 const shareSearchPropsFixed=reactive({accounts:folderShareProps.accounts,fixedAccountId:'blue-share-b'})
 const fixedShareSearch=sg.setup(shareSearchPropsFixed,{expose(){},emit(){}})
 fixedShareSearch.query.value='归档项目'
 fixedShareSearch.accountId.value='*'
 controls.searchResult={state:'completed',items:[foundLocation]}
 await fixedShareSearch.start()
 assert.deepEqual(calls.searches.at(-1),{accountIds:['blue-share-b'],query:'归档项目',includePath:false,firstMatchPerAccount:false})
 shareSearchPropsFixed.fixedAccountId='blue-share-a'
 await nextTick()
 assert.equal(fixedShareSearch.status.value,null)
 ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
 delete controls.searchResult
 const readsBeforeLocation=calls.lists.length
 const sharesBeforeLocation=calls.shares.length
 shareBrowser.view.value='search'
 shareBrowser.openSearchItem({...foundLocation,accountId:'blue-share-a'},false,false)
 shareBrowser.openSearchItem(foundLocation,false,true)
 assert.equal(shareBrowser.single.value,null)
 const directListCount=calls.lists.length
 shareBrowser.openSearchItem(foundLocation,false,false)
 assert.equal(shareBrowser.single.value.parentId,'30')
 assert.equal(shareBrowser.single.value.sourcePath,'根目录 / 项目资料 / 归档')
 assert.equal(shareBrowser.single.value.editPassword,false)
 assert.equal(shareBrowser.view.value,'search')
 shareBrowser.closeLinks()
 shareBrowser.view.value='candidates'
 shareBrowser.toggleSearchQueued(foundLocation,true,false)
 shareBrowser.openSearchItem(foundLocation,true,false)
 assert.equal(shareBrowser.single.value.editPassword,true)
 shareBrowser.closeLinks()
 assert.equal(shareBrowser.view.value,'candidates')
 assert.equal(shareBrowser.readQueue.value.length,1)
 assert.equal(calls.lists.length,directListCount)
 assert.equal(calls.shares.length,sharesBeforeLocation)
 shareBrowser.clearQueue()
 shareBrowser.view.value='search'
 await shareBrowser.openSearchResult({...foundLocation,accountId:'blue-share-a'})
 assert.equal(calls.lists.length,readsBeforeLocation)
 await shareBrowser.openSearchResult(foundLocation)
 assert.deepEqual(calls.lists.at(-1),{accountId:'blue-share-b',parent:'30'})
 assert.equal(shareBrowser.page.value,2)
 assert.deepEqual(shareBrowser.locatedEntry.value,foundShareEntry)
 assert.equal(shareBrowser.rows.value[0].folder,false)
 assert.equal(shareBrowser.single.value,null)
 assert.equal(calls.shares.length,sharesBeforeLocation)
 shareBrowser.openItem(shareBrowser.rows.value[0])
 assert.equal(shareBrowser.single.value.parentId,'30')
 shareBrowser.closeLinks()
 const originalShareWrites=calls.shares.length
 shareBrowser.toggleQueued(shareBrowser.rows.value[0],true)
 shareBrowser.goToFolder('-1')
 await settle()
 shareBrowser.toggleQueued(shareBrowser.rows.value[0],true)
 shareBrowser.toggleQueued(shareBrowser.rows.value[0],true)
 assert.equal(shareBrowser.readQueue.value.length,2)
 assert.deepEqual(shareBrowser.readQueue.value.map(row=>row.sourcePath),['根目录 / 项目资料 / 归档','根目录'])
 shareBrowser.openQueue()
 assert.equal(calls.lanzouShareReads,undefined)
 assert.equal(shareBrowser.queuedBatch.value.accountId,'blue-share-b')
 assert.equal(shareBrowser.queuedBatch.value.accountName,'蓝奏云 · 同名（2）')
 const folderBatchReader=await loadComponent('BatchManagedShareReadDialog')
 const readBatchEvents=[]
 const readBatch=folderBatchReader.setup({kind:'lanzou',...shareBrowser.queuedBatch.value,readLink:shareBrowser.readQueuedLink,canLocate:true},{expose(){},emit(...event){readBatchEvents.push(event)}})
 controls.failLanzouParent='30'
 await readBatch.run()
 assert.deepEqual(calls.lanzouShareReads,[
  {accountId:'blue-share-b',parentId:'30',id:'20',name:'归档项目',folder:false},
  {accountId:'blue-share-b',parentId:'-1',id:'20',name:'项目资料',folder:true}
 ])
 assert.deepEqual(readBatch.rows.value.map(row=>row.state),['failed','ready'])
 await readBatch.copy(true)
 assert.equal(calls.reports.at(-1),'https://fixture.lanzoub.com/b20')
 delete controls.failLanzouParent
 await readBatch.run('failed')
 assert.equal(calls.lanzouShareReads.length,3)
 assert.equal(readBatch.ready.value.length,2)
 readBatch.filter.value='https://fixture.lanzoub.com/i20?pwd=abc'
 assert.deepEqual(readBatch.visible.value.map(row=>row.name),['归档项目'])
 await readBatch.copy(true)
 assert.equal(calls.reports.at(-1),'https://fixture.lanzoub.com/i20')
 readBatch.filter.value='https://fixture.lanzoub.com/I20'
 assert.equal(readBatch.visible.value.length,0)
 readBatch.filter.value='归档'
 const [hit]=readBatch.visible.value
 assert.equal(hit.name,'归档项目')
 readBatch.locate(hit)
 assert.deepEqual(readBatchEvents.at(-1),['locate',hit.handle])
 const priorLists=calls.lists.length
 await shareBrowser.locateQueuedShare('stale-handle')
 assert.equal(calls.lists.length,priorLists)
 await shareBrowser.locateQueuedShare(hit.handle)
 assert.deepEqual(calls.lists.at(-1),{accountId:'blue-share-b',parent:'30'})
 assert.equal(shareBrowser.locatedEntry.value.name,'归档项目')
 assert.equal(shareBrowser.readQueue.value.length,2)
 assert.equal(calls.shares.length,originalShareWrites)
 ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
 shareBrowser.closeLinks()
 const folderBatchEditor=await loadComponent('BatchShareExpiryDialog')
 const setupFolderEdit=()=>{
  shareBrowser.openQueue(true)
  return folderBatchEditor.setup({kind:'lanzou',...shareBrowser.queuedBatch.value,directoryPassword:shareBrowser.directoryPassword},{expose(){},emit(){}})
 }
 const editBlocked=setupFolderEdit()
 editBlocked.agreed.value=true
 editBlocked.password.value='invalid!'
 await editBlocked.run()
 assert.equal(calls.lanzouShareReads.length,3)
 assert.equal(calls.lanzouShareEdits,undefined)
 editBlocked.password.value='New123'
 controls.failLanzouParent='-1'
 await editBlocked.run()
 delete controls.failLanzouParent
 assert.match(editBlocked.error.value,/未修改任何分享/)
 assert.equal(calls.lanzouShareReads.length,5)
 assert.equal(calls.lanzouShareEdits,undefined)
 ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
 shareBrowser.closeLinks()
 const editUnknown=setupFolderEdit()
 editUnknown.agreed.value=true
 editUnknown.password.value='New123'
 controls.lanzouEditUnknown=true
 await editUnknown.run()
 delete controls.lanzouEditUnknown
 assert.deepEqual(calls.lanzouShareEdits,[
  {accountId:'blue-share-b',parentId:'30',id:'20',name:'归档项目',folder:false,url:'https://fixture.lanzoub.com/i20',originalPassword:'fixture',password:'New123',reads:7}
 ])
 assert.deepEqual(editUnknown.rows.value.map(row=>row.state),['unknown','skipped'])
 assert.equal(editUnknown.canRetry.value,false)
 editUnknown.prepareRetry()
 assert.deepEqual(editUnknown.rows.value.map(row=>row.state),['unknown','skipped'])
 ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
 shareBrowser.closeLinks()
 const clearBatch=setupFolderEdit()
 clearBatch.access.value='clear'
 clearBatch.password.value='Old1'
 await clearBatch.run()
 assert.equal(calls.lanzouShareReads.length,7)
 assert.equal(calls.lanzouShareClears,undefined)
 clearBatch.agreed.value=true
 controls.groupLanzouDirectories=true
 await clearBatch.run()
 delete controls.groupLanzouDirectories
 assert.deepEqual(calls.lanzouDirectoryGroups.at(-1).entries.map(entry=>entry.parentId),['30','-1'])
 assert.equal(clearBatch.checkedLinks.value,2)
 assert.equal(calls.lanzouShareEdits.length,1)
 assert.deepEqual(calls.lanzouShareClears,[
  {accountId:'blue-share-b',parentId:'30',id:'20',name:'归档项目',folder:false,url:'https://fixture.lanzoub.com/i20',originalPassword:'',reads:8},
  {accountId:'blue-share-b',parentId:'-1',id:'20',name:'项目资料',folder:true,url:'https://fixture.lanzoub.com/b20',originalPassword:'fixture',reads:8}
 ])
 assert.deepEqual(clearBatch.rows.value.map(row=>row.state),['ready','ready'])
 assert.equal(clearBatch.canRetry.value,false)
 ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
 {
  const attempts=[],reads=[]
  let deliberatelyFail=true
  const targets=['first','second','third'].map(id=>({id,name:id,canEdit:true}))
  const links=id=>({url:'https://fixture.lanzoub.com/'+id,password:deliberatelyFail?'Old':'Fresh'})
  const editor=folderBatchEditor.setup({accountName:'测试',kind:'lanzou',targets,directoryPassword:{
   read:async id=>links(id),
   readGroup:async ids=>{reads.push(ids);return ids.map(links)},
   update:async(id,original,password)=>{
    attempts.push({id,originalPassword:original.password,password})
    if(id==='second'&&deliberatelyFail)return {state:'failed',message:'原提取码已变化'}
    return {state:'ready',message:'已核对'}
   },
  }},{expose(){},emit(){}})
  editor.password.value='New1'
  editor.agreed.value=true
  await editor.run()
  assert.deepEqual(editor.rows.value.map(row=>row.state),['ready','failed','skipped'])
  assert.equal(editor.canRetry.value,true)
  assert.deepEqual(attempts.map(row=>row.id),['first','second'])
  deliberatelyFail=false
  editor.prepareRetry()
  assert.deepEqual(editor.rows.value.map(row=>row.state),['ready','queued','queued'])
  assert.match(editor.rows.value[1].message,/已变化/)
  assert.equal(editor.agreed.value,false)
  await editor.run()
  assert.equal(attempts.length,2)
  editor.agreed.value=true
  await editor.run()
  assert.deepEqual(reads,[['first','second','third'],['second','third']])
  assert.deepEqual(attempts.slice(2),[
   {id:'second',originalPassword:'Fresh',password:'New1'},
   {id:'third',originalPassword:'Fresh',password:'New1'},
  ])
  assert.deepEqual(editor.rows.value.map(row=>row.state),['ready','ready','ready'])
  ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
 }
 const formerTarget=shareBrowser.readQueue.value[0].id
 shareBrowser.accountId.value='blue-share-a'
 await settle()
 assert.equal(shareBrowser.queuedBatch.value,null)
 assert.equal(shareBrowser.readQueue.value.length,0)
 await assert.rejects(shareBrowser.readQueuedLink(formerTarget))
 assert.equal(calls.lanzouShareReads.length,8)
 shareBrowser.accountId.value='blue-share-b'
 await settle()
 shareBrowser.toggleQueued(shareBrowser.rows.value[0],true)
 shareBrowser.refreshFolder()
 await settle()
 assert.equal(shareBrowser.readQueue.value.length,0)
 controls.lanzouSharePages['30'].entries[50]={...foundShareEntry,name:'更新后的名称'}
 shareBrowser.view.value='search'
 await shareBrowser.openSearchResult(foundLocation)
 assert.match(shareBrowser.error.value,/搜索结果已变化/)
 {
  const receipt={accountId:'blue-share-b',canLocate:true,result:{id:'receipt',kind:'lanzou',state:'ready'}};
  const duplicate={...receipt,result:{...receipt.result,id:'duplicate',name:'重复回执'}};
  controls.shareHistory=[receipt,duplicate];
  controls.shareLocation={accountId:receipt.accountId,parentId:'30',entry:{...controls.lanzouSharePages['30'].entries[50]}};
  const local=historyComponent.setup({accounts:folderShareProps.accounts},{expose(){},emit(){}});
  await settle();
  local.selectedLanzou.value=[receipt.result.id];
  local.openLanzouBatch();
  assert.equal(local.lanzouBatch.value.accountId,receipt.accountId);
  await local.readLanzouReceipt(receipt.result.id);
  assert.deepEqual(calls.localBlueReads.at(-1),{accountId:receipt.accountId,id:receipt.result.id});
  await assert.rejects(local.readLanzouReceipt('unselected'));
  await local.readLanzouReceipts([receipt.result.id]);
  assert.deepEqual(calls.localBlueGroups.at(-1),{accountId:receipt.accountId,ids:[receipt.result.id]});
  await assert.rejects(local.readLanzouReceipts([receipt.result.id,'unselected']));
  const original={url:'https://fixture.lanzoub.com/i123',password:'fixture'};
  await assert.rejects(local.receiptPassword.clear(receipt.result.id,original));
  local.closeLanzouBatch();
  const closeEditEvents=[];
  const openReceiptEdit=()=>{
   local.openLanzouBatch(true);
   return folderBatchEditor.setup({kind:'lanzou',...local.lanzouBatch.value,directoryPassword:local.receiptPassword},{expose(){},emit(...args){closeEditEvents.push(args)}});
  };
  local.selectedLanzou.value=[receipt.result.id,duplicate.result.id];
  const duplicateEdit=openReceiptEdit();
  duplicateEdit.access.value='clear';
  await duplicateEdit.run();
  assert.equal(duplicateEdit.started.value,false);
  duplicateEdit.agreed.value=true;
  await duplicateEdit.run();
  assert.match(duplicateEdit.error.value,/同一个分享链接/);
  assert.equal(calls.localBlueEdits,undefined);
  duplicateEdit.close();
  assert.deepEqual(closeEditEvents.at(-1),['close',false]);
  local.closeLanzouEdit(closeEditEvents.at(-1)[1]);
  assert.deepEqual(local.selectedLanzou.value,[receipt.result.id,duplicate.result.id]);
  const stoppedEdit=openReceiptEdit();
  stoppedEdit.agreed.value=true;
  stoppedEdit.password.value='New123';
  const singleReads=calls.localBlueReads.length;
  controls.holdLocalBlueGroup=true;
  const checking=stoppedEdit.run();
  await settle();
  assert.deepEqual(calls.localBlueGroups.at(-1).ids,[receipt.result.id,duplicate.result.id]);
  stoppedEdit.stopping.value=true;
  controls.releaseLocalBlueGroup();
  await checking;
  delete controls.holdLocalBlueGroup;
  assert.equal(calls.localBlueReads.length,singleReads);
  assert.equal(calls.localBlueEdits,undefined);
  assert.deepEqual(stoppedEdit.rows.value.map(row=>row.state),['skipped','skipped']);
  stoppedEdit.close();
  assert.deepEqual(closeEditEvents.at(-1),['close',false]);
  local.closeLanzouEdit(false);
  local.selectedLanzou.value=[receipt.result.id];
  const checkedEdit=openReceiptEdit();
  await assert.rejects(local.receiptPassword.update(duplicate.result.id,original,'New123'));
  checkedEdit.agreed.value=true;
  checkedEdit.password.value='New123';
  await checkedEdit.run();
  assert.deepEqual(calls.localBlueEdits,[{accountId:receipt.accountId,id:receipt.result.id,url:original.url,originalPassword:original.password,password:'New123',clear:false}]);
  assert.equal(checkedEdit.rows.value[0].state,'ready');
  local.closeLanzouEdit(true);
  await settle();
  assert.deepEqual(local.selectedLanzou.value,[]);
  await local.locateLanzou(local.rows.value[0]);
  assert.deepEqual(calls.shareLocations.at(-1),{accountId:receipt.accountId,id:'receipt'});
  assert.equal(local.view.value,'lanzou');
  const linked=folderShares.setup({accounts:folderShareProps.accounts,initialLocation:local.lanzouLocation.value},{expose(){},emit(){}});
  await settle();
  assert.equal(linked.accountId.value,'blue-share-b');
  assert.equal(linked.folderId.value,'30');
  assert.equal(linked.locatedEntry.value.name,'更新后的名称');
  linked.accountId.value='blue-share-a';
  await settle();
  assert.equal(linked.folderId.value,'-1');
  assert.equal(linked.locatedEntry.value,null);
  for(const cleanup of (await import(fileURL('hooks.mjs'))).cleanup.slice(-2))cleanup();
  delete controls.shareLocation;
  controls.shareHistory=[];
  console.log('PASS: a Lanzou local share receipt opens the exact account and source entry.');
 }

 assert.equal(shareBrowser.locatedEntry.value,null)
 assert.equal(shareBrowser.single.value,null)
 ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
 console.log('PASS: LanZou directory shares browse children, preserve file identity, isolate accounts and edit only on explicit submit.')
 console.log('PASS: LanZou cross-directory queue keeps file and folder identities, reads only after confirmation, retries failed items, copies successful links and clears on account change or refresh.')
 console.log('PASS: LanZou batch password changes reject invalid codes, preflight every selected link before writes, and stop after an uncertain write.')
 {
 const shareListSource=await fs.readFile(path.join(root,'src/CloudShareList.vue'),'utf8')
 {
  const providers=[
   ['quark','https://pan.quark.cn/'],['pan123','https://yun.123pan.cn/shareList'],
   ['baidu','https://pan.baidu.com/disk/main#/share/my'],['guangya','https://www.guangyapan.com/'],
  ]
  const officialProps=reactive({accounts:providers.map(([kind])=>({id:'private-'+kind,kind,endpoint:'https://fixture.invalid/'})),blocked:false})
  const official=managedComponent.setup(officialProps,{expose(){},emit(){}})
  for(const [kind,url] of providers){
   official.accountId.value='private-'+kind
   await settle()
   const before={urls:events.openedURLs.length,lists:calls.managedLists.length,shares:calls.shares.length}
   await official.openWebsite()
   assert.deepEqual(events.openedURLs.slice(before.urls),[url])
   assert.equal(calls.managedLists.length,before.lists)
   assert.equal(calls.shares.length,before.shares)
   officialProps.blocked=true
   await official.openWebsite()
   assert.equal(events.openedURLs.length,before.urls+1)
   officialProps.blocked=false
  }
  ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
  console.log('PASS: official share links open fixed provider sites without including account or share details.')
 }
 const shareListTemplate=parse(shareListSource).descriptor.template.content
 assert.doesNotMatch(shareListTemplate,/跨账号查找|跨页搜索|搜索分享名称或链接/)
 assert.match(shareListTemplate,/选择云端分享账号/)
 assert.match(shareListTemplate,/读取原分享链接/)
 assert.match(shareListTemplate,/加入跨页分享清单/)
 const batchReader=await loadComponent('BatchManagedShareReadDialog')
 const batchReadProps=reactive({accounts:[{id:'batch-baidu',kind:'baidu'},{id:'batch-quark',kind:'quark'}],refreshKey:'0'})
 const batchReadBrowser=managedComponent.setup(batchReadProps,{expose(){},emit(){}})
 await settle()
 const batchReadTargets=[
  {id:'read-a',name:'已有 A',canRevoke:true},
  {id:'read-b',name:'已有 B',canRevoke:false},
  {id:'read-c',name:'已有 C',canRevoke:true},
 ]
 batchReadBrowser.items.value=batchReadTargets
 batchReadBrowser.selectBatch()
 batchReadBrowser.prepareBatch()
 assert.equal(batchReadBrowser.batchTargets.value,null)
 const readBefore=calls.managedReads.length
 const revokeBefore=calls.revocations.length
 const createBefore=calls.shares.length
 batchReadBrowser.prepareBatchRead()
 assert.equal(batchReadBrowser.locked.value,true)
 assert.equal(calls.managedReads.length,readBefore)
 assert.deepEqual(batchReadBrowser.batchReadTargets.value.map(row=>row.id),['read-a','read-b','read-c'])
 const reader=batchReader.setup({accountName:'百度网盘 · 测试',kind:'baidu',targets:batchReadBrowser.batchReadTargets.value},{expose(){},emit(){}})
 controls.managedReadResults=Object.fromEntries(batchReadTargets.map(row=>[row.id,{name:row.name,url:'https://pan.baidu.com/s/'+row.id,password:'A1b2',expiry:'永久',secret:'never-copy-secret'}]))
 controls.holdManagedRead=true
 const reading=reader.run()
 await settle()
 await reader.run()
 reader.stopRequested.value=true
 controls.holdManagedRead=false
 controls.releaseManagedRead()
 await reading
 assert.deepEqual(calls.managedReads.slice(readBefore),['read-a'])
 assert.deepEqual(reader.rows.value.map(row=>row.state),['ready','queued','queued'])
 controls.failManagedHandle='read-b'
 await reader.run()
 assert.deepEqual(reader.rows.value.map(row=>row.state),['ready','failed','ready'])
 await reader.copy(true)
 assert.equal(calls.reports.at(-1),'https://pan.baidu.com/s/read-a?pwd=A1b2\nhttps://pan.baidu.com/s/read-c?pwd=A1b2')
 reader.autoFill.value=false
 await reader.copy()
 assert.match(calls.reports.at(-1),/文件：已有 A\n链接：https:\/\/pan\.baidu\.com\/s\/read-a\n提取码：A1b2\n永久/)
 assert.doesNotMatch(calls.reports.at(-1),/已有 B|never-copy-secret|模拟原链接失败/)
 delete controls.failManagedHandle
 await reader.run('failed')
 assert.equal(reader.ready.value.length,3)
 assert.deepEqual(calls.managedReads.slice(readBefore),['read-a','read-b','read-c','read-b'])
 assert.equal(calls.revocations.length,revokeBefore)
 assert.equal(calls.shares.length,createBefore)
 const batchReadHooks=await import(fileURL('hooks.mjs'))
 batchReadHooks.cleanup.at(-1)()
 batchReadProps.refreshKey='1'
 await settle()
 assert.equal(batchReadBrowser.batchReadTargets.value,null)
 assert.deepEqual(batchReadBrowser.batchSelection.value,[])
 batchReadHooks.cleanup.at(-1)()
 const staleReader=batchReader.setup({accountName:'测试',kind:'baidu',targets:batchReadTargets},{expose(){},emit(){}})
 controls.holdManagedRead=true
 const staleBatchRead=staleReader.run()
 await settle()
 const beforeDispose=calls.managedReads.length
 batchReadHooks.cleanup.at(-1)()
 controls.holdManagedRead=false
 controls.releaseManagedRead()
 await staleBatchRead
 assert.equal(calls.managedReads.length,beforeDispose)
 assert.equal(staleReader.ready.value.length,0)
 delete controls.managedReadResults
 console.log('PASS: existing-share batch read stays read-only, retains partial results, retries failures and copies only verified fields.')
 const quarkTargets=Array.from({length:12},(_,i)=>({id:'qread-'+i,name:'原分享 '+i}))
 controls.quarkShareReadResults=Object.fromEntries(quarkTargets.slice(1).map(row=>[row.id,{name:row.name,url:'https://pan.quark.cn/s/'+row.id,expiry:'永久',password:''}]))
 const quarkReader=batchReader.setup({accountName:'夸克测试',kind:'quark',targets:quarkTargets},{expose(){},emit(){}})
 const quarkSinglesBefore=calls.managedReads.length
 controls.holdQuarkShareReadGroup=true
 const quarkInitialRead=quarkReader.run()
 await settle()
 assert.deepEqual(calls.quarkShareReadGroups.at(-1),quarkTargets.slice(0,10).map(row=>row.id))
 quarkReader.stopRequested.value=true
 controls.holdQuarkShareReadGroup=false
 controls.releaseQuarkShareReadGroup()
 await quarkInitialRead
 assert.equal(quarkReader.ready.value.length,9)
 assert.equal(calls.managedReads.length,quarkSinglesBefore)
 assert.equal(quarkReader.queued.value,3)
 await quarkReader.run()
 assert.deepEqual(calls.quarkShareReadGroups.at(-1),['qread-0','qread-10','qread-11'])
 assert.deepEqual(calls.managedReads.slice(quarkSinglesBefore),['qread-0'])
 assert.equal(quarkReader.ready.value.length,12)
 batchReadHooks.cleanup.at(-1)()
 delete controls.quarkShareReadResults
 console.log('PASS: Quark bulk read groups requests, retains completed links on stop, and falls back only for missing details.')
 }
 {
  const shareQueueProps=reactive({accounts:[{id:'q',kind:'quark'},{id:'b',kind:'baidu'}],refreshKey:'0'})
  const one={id:'first-handle',selectionKey:'share-one',name:'同名分享',canRevoke:true}
  const moved={...one,id:'new-handle'}
  const other={id:'second-handle',selectionKey:'share-two',name:'同名分享',canRevoke:false}
  controls.cloudSearchPages={1:{page:1,items:[one],hasMore:true,next:''},2:{page:2,items:[moved,other],hasMore:false,next:''}}
  const shareQueue=managedComponent.setup(shareQueueProps,{expose(){},emit(){}})
  await settle()
  shareQueue.selectBatch()
  shareQueue.queueReadSelection()
  await shareQueue.load(2)
  shareQueue.selectBatch()
  shareQueue.queueReadSelection()
  assert.deepEqual(shareQueue.batchReadQueue.value.map(row=>[row.page,row.share.id]),[[2,'new-handle'],[2,'second-handle']])
  const readCount=calls.managedReads.length
  shareQueue.prepareBatchRead(undefined,true)
  assert.deepEqual(shareQueue.batchReadTargets.value.map(row=>row.id),['new-handle','second-handle'])
  assert.equal(calls.managedReads.length,readCount)
  assert.equal(shareQueue.batchTargets.value,null)
  await shareQueue.closeBatchRead()
  shareQueue.removeQueuedShare('share-two')
  assert.equal(shareQueue.batchReadQueue.value.length,1)
  shareQueue.accountId.value='b'
  await settle()
  assert.equal(shareQueue.batchReadQueue.value.length,0)
  assert.equal(shareQueue.batchReadTargets.value,null)
  ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
  delete controls.cloudSearchPages
  console.log('PASS: share read queue preserves selections across pages, deduplicates reissued handles and isolates accounts.')
 }
 {
  const share={id:'private-1',name:'加密资料',selectionKey:'private-key',canRevoke:true}
  controls.quarkSharePages={private:{
   1:{items:[share],page:1,hasMore:true},
   2:{items:[{...share,id:'private-2',selectionKey:'private-key-2'}],page:2,hasMore:false},
  }}
  const filtered=managedComponent.setup(reactive({accounts:[{id:'q',kind:'quark'},{id:'p',kind:'pan123'}],refreshKey:'0'}),{expose(){},emit(){}})
  await settle()
  filtered.chooseQuarkShareFilter('private')
  await settle()
  assert.deepEqual(calls.managedQuark.at(-1),{accountId:'q',page:1,filter:'private'})
  await filtered.load(2)
  assert.deepEqual(calls.managedQuark.at(-1),{accountId:'q',page:2,filter:'private'})
  filtered.selectBatch()
  filtered.queueReadSelection()
  assert.equal(filtered.batchReadQueue.value.length,1)
  filtered.chooseQuarkShareFilter('expired')
  await settle()
  assert.equal(filtered.batchReadQueue.value.length,0)
  assert.deepEqual(calls.managedQuark.at(-1),{accountId:'q',page:1,filter:'expired'})
  filtered.accountId.value='p'
  await settle()
  assert.equal(filtered.quarkShareFilter.value,'')
  assert.deepEqual(calls.managedLists.at(-1),{accountId:'p',page:1})
  ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
  delete controls.quarkSharePages
  console.log('PASS: Quark share filters apply to pagination and invalidate cross-page selections.')
 }
 {
  const browser=managedComponent.setup(reactive({accounts:[{id:'q',kind:'quark'},{id:'p',kind:'pan123'}],refreshKey:'0'}),{expose(){},emit(){}})
  await settle()
  const row={...browser.items.value[0],quarkRestricted:120}
  await browser.openRestrictions({...row,quarkRestricted:0})
  assert.equal(calls.quarkRestrictionReads?.length??0,0)
  await browser.openRestrictions(row)
  assert.equal(browser.restrictions.value.reported,120)
  assert.equal(browser.restrictions.value.items[0].reason,'网盘给出的原因')
  browser.closeRestrictions()
  assert.equal(browser.restrictions.value,null)
  controls.holdQuarkRestrictions=true
  const pending=browser.openRestrictions(row)
  await settle()
  await browser.openRestrictions(row)
  assert.deepEqual(calls.quarkRestrictionReads,[row.id,row.id])
  browser.accountId.value='p'
  await settle()
  controls.holdQuarkRestrictions=false
  controls.releaseQuarkRestrictions()
  await pending
  assert.equal(browser.restrictions.value,null)
  assert.equal(browser.restrictionTarget.value,'')
  ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
  console.log('PASS: Quark share restriction details are opt-in, deduplicated while loading, and discarded on account change.')
 }
 {
  const paidRow={id:'paid-1',name:'付费资料一',selectionKey:'paid-key-1',priceFen:350,earningsFen:0,canRevoke:true,canEdit:false}
  const second={...paidRow,id:'paid-2',name:'付费资料二',selectionKey:'paid-key-2',canEdit:true,canLimitTraffic123:true}
  controls.paidSharePage={items:[paidRow],page:1,hasMore:true,next:'paid-next'}
  controls.cloudSearchTokens={'paid-next':{items:[second],page:2,hasMore:false,next:''}}
  const paid=managedComponent.setup(reactive({accounts:[{id:'p',kind:'pan123'},{id:'b',kind:'baidu'}],refreshKey:'0'}),{expose(){},emit(){}})
  await settle()
  paid.chooseShareType('paid')
  await settle()
  assert.equal(calls.managedPaid.at(-1),'p')
  assert.equal(paid.items.value[0].earningsFen,0)
  assert.equal(paid.canRevoke.value,true)
  const beforePaidRevoke=calls.revocations.length
  paid.prepareRevoke(paid.items.value[0])
  assert.equal(paid.confirmTarget.value.id,'paid-1')
  paid.cancelRevoke()
  assert.equal(calls.revocations.length,beforePaidRevoke)
  paid.selectBatch()
  assert.equal(paid.canRevokeBatch.value,true)
  assert.equal(calls.revokeLoads.at(-1),'p')
  paid.prepareBatch()
  assert.deepEqual(paid.batchTargets.value.map(row=>row.id),['paid-1'])
  await paid.closeBatch(false)
  assert.equal(calls.revocations.length,beforePaidRevoke)
  controls.revokes=[{id:'paid-receipt',accountId:'p',name:paidRow.name,state:'unknown',paid123:true,canCheck:true}]
  await paid.load()
  assert.equal(paid.visibleRevokes.value[0].paid123,true)
  delete controls.revokes
  assert.equal(paid.canEditBatchSettings.value,true)
  paid.selectBatch()
  paid.prepareBatchExpiry()
  assert.equal(paid.batchExpiryTargets.value,null)
  await paid.load(2)
  assert.deepEqual(calls.managedNext.at(-1),{accountId:'p',token:'paid-next'})
  paid.selectBatch()
  paid.queueReadSelection()
  assert.equal(paid.batchReadQueue.value.length,1)
  paid.prepareBatchExpiry(true)
  const editor=await loadComponent('BatchShareExpiryDialog')
  const paidBatch=editor.setup({accountName:'123 云盘',kind:'pan123',paid:paid.paidView.value,targets:paid.batchExpiryTargets.value},{expose(){},emit(){}})
  assert.equal(paidBatch.edit123.value,'permissions')
  paidBatch.edit123.value='expiry'
  paidBatch.days.value=7
  paidBatch.agreed.value=true
  assert.equal(paidBatch.canStart.value,false)
  paidBatch.edit123.value='permissions'
  paidBatch.permission.value=4
  assert.equal(paidBatch.canStart.value,true)
  await paid.closeBatchExpiry(false)
  paid.chooseShareType('free')
  await settle()
  assert.equal(paid.batchReadQueue.value.length,0)
  assert.deepEqual(calls.managedLists.at(-1),{accountId:'p',page:1})
  paid.chooseShareType('paid')
  await settle()
  paid.accountId.value='b'
  await settle()
  assert.equal(paid.shareType.value,'free')
  assert.deepEqual(calls.managedLists.at(-1),{accountId:'b',page:1})
  ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
  delete controls.cloudSearchTokens
  delete controls.paidSharePage
  console.log('PASS: 123 paid share list, revoke selection and confirmation, receipt loading, and account/type isolation.')
 }
 {
  const activationComponent=await loadComponent('BatchShareExpiryDialog')
  const frozen=['a','b','c'].map(id=>({id:'frozen-'+id,selectionKey:'key-'+id,name:'同名',canActivate:true,canEdit:false}))
  const browser=managedComponent.setup(reactive({accounts:[{id:'bd',kind:'baidu'}],refreshKey:'0'}),{expose(){},emit(){}})
  await settle()
  browser.items.value=[frozen[0],{...frozen[1],canActivate:false,canEdit:true}]
  browser.selectBatch()
  browser.prepareBatchExpiry(false,true)
  assert.equal(browser.batchExpiryTargets.value,null)
  browser.items.value=[frozen[0]]
  browser.selectBatch()
  browser.queueReadSelection()
  browser.page.value=2
  browser.items.value=frozen.slice(1)
  browser.selectBatch()
  browser.queueReadSelection()
  browser.prepareBatchExpiry(true,true)
  assert.equal(browser.batchActivate.value,true)
  assert.deepEqual(browser.batchExpiryTargets.value.map(row=>row.id),frozen.map(row=>row.id))
  const setup=()=>activationComponent.setup({accountName:'百度账号',kind:'baidu',activate:true,targets:browser.batchExpiryTargets.value},{expose(){},emit(){}})
  const beforeWrites=calls.shareActivations.length
  const rejected=setup()
  await rejected.run()
  assert.equal(calls.activationPreflight,undefined)
  rejected.agreed.value=true
  controls.failActivationPreflight=true
  await rejected.run()
  controls.failActivationPreflight=false
  assert.equal(calls.shareActivations.length,beforeWrites)
  assert.deepEqual(rejected.rows.value.map(row=>row.state),['skipped','skipped','skipped'])
  const batch=setup()
  batch.agreed.value=true
  await Promise.all([batch.run(),batch.run()])
  assert.deepEqual(calls.activationPreflight.at(-1),frozen.map(row=>row.id))
  assert.deepEqual(calls.shareActivations.slice(beforeWrites),['frozen-a','frozen-b'])
  assert.deepEqual(batch.rows.value.map(row=>row.state),['ready','unknown','skipped'])
  assert.equal(batch.canRetry.value,false)
  batch.prepareRetry()
  assert.equal(batch.rows.value[2].state,'skipped')
  await batch.check(batch.rows.value[1])
  assert.equal(batch.canRetry.value,true)
  assert.deepEqual(calls.shareActivations.slice(beforeWrites),['frozen-a','frozen-b'])
  batch.prepareRetry()
  await batch.run()
  assert.deepEqual(calls.shareActivations.slice(beforeWrites),['frozen-a','frozen-b'])
  batch.agreed.value=true
  await batch.run()
  assert.deepEqual(calls.activationPreflight.at(-1),['frozen-c'])
  assert.deepEqual(calls.shareActivations.slice(beforeWrites),['frozen-a','frozen-b','frozen-c'])
  console.log('PASS: Baidu frozen shares are selected across pages, with whole-batch preflight and stop on unknown.')
 }
 {
  const trafficComponent=await loadComponent('BatchShareExpiryDialog')
  const targets=['a','b','c'].map(id=>({id:'traffic-'+id,name:'同名',canEdit:true,canLimitTraffic123:true}))
  const setup=(items=targets)=>trafficComponent.setup({accountName:'123 云盘',kind:'pan123',targets:items},{expose(){},emit(){}})
  const limitNotEligible=setup([targets[0],{...targets[1],canLimitTraffic123:false}])
  limitNotEligible.edit123.value='limit'
  limitNotEligible.limitMode.value='unlimited'
  limitNotEligible.agreed.value=true
  assert.equal(limitNotEligible.canStart.value,false)
  const beforePermissions=calls.sharePermissions.length
  const rejected=setup()
  rejected.edit123.value='permissions'
  rejected.permission.value=4
  await rejected.run()
  assert.equal(calls.trafficPreflights,undefined)
  rejected.agreed.value=true
  controls.failTrafficPreflight=true
  await rejected.run()
  controls.failTrafficPreflight=false
  assert.equal(calls.sharePermissions.length,beforePermissions)
  assert.deepEqual(rejected.rows.value.map(row=>row.state),['skipped','skipped','skipped'])
  const permitted=setup()
  permitted.edit123.value='permissions'
  permitted.permission.value=4
  permitted.agreed.value=true
  await Promise.all([permitted.run(),permitted.run()])
  assert.deepEqual(calls.trafficPreflights.at(-1),{handles:targets.map(row=>row.id),permission:4,limit:0})
  assert.deepEqual(calls.sharePermissions.slice(beforePermissions),['traffic-a','traffic-b'].map(handle=>({handle,mode:4})))
  assert.deepEqual(permitted.rows.value.map(row=>row.state),['ready','unknown','skipped'])
  const cap=setup()
  cap.edit123.value='limit'
  cap.limitMode.value='limit'
  cap.limitAmount.value='2'
  cap.agreed.value=true
  await cap.run()
  assert.deepEqual(calls.trafficPreflights.at(-1),{handles:targets.map(row=>row.id),permission:0,limit:2**31})
  assert.deepEqual(calls.shareTraffic.at(-1),{handle:'traffic-a',limit:2**31})
  const combined=setup()
  combined.edit123.value='permissions'
  combined.permission.value=4
  combined.changeLimitWithPermission.value=true
  combined.agreed.value=true
  assert.equal(combined.canStart.value,false)
  combined.limitMode.value='unlimited'
  const previousModes=calls.sharePermissions.length,previousLimits=calls.shareTraffic.length,previousCombined=calls.combinedSettings.length
  await combined.run()
  assert.deepEqual(calls.combinedPreflights.at(-1),{handles:targets.map(row=>row.id),permission:4,limit:0})
  assert.deepEqual(calls.combinedSettings.slice(previousCombined),['traffic-a','traffic-b'].map(handle=>({handle,mode:4,limit:0})))
  assert.equal(calls.sharePermissions.length,previousModes)
  assert.equal(calls.shareTraffic.length,previousLimits)
  assert.deepEqual(combined.rows.value.map(row=>row.state),['ready','unknown','skipped'])
  console.log('PASS: 123 batch download permissions and limits verify the whole selection and stop after an uncertain result.')
 }
 {
  const expiryComponent=await loadComponent('BatchShareExpiryDialog')
  const items=[{id:'expiry-a',name:'同名',canEdit:true},{id:'expiry-b',name:'同名',canEdit:true}]
  const expiryProps=reactive({accounts:[{id:'bd',kind:'baidu'},{id:'gy',kind:'guangya'},{id:'q',kind:'quark'}],refreshKey:'0'})
  const expiryBrowser=managedComponent.setup(expiryProps,{expose(){},emit(){}})
  await settle()
  expiryBrowser.items.value=items
  expiryBrowser.selectBatch()
  expiryBrowser.prepareBatchExpiry()
  assert.equal(expiryBrowser.batchExpiryTargets.value.length,2)
  const setupExpiry=kind=>expiryComponent.setup({accountName:'测试',kind,targets:expiryBrowser.batchExpiryTargets.value},{expose(){},emit(){}})
  const beforeWrites=calls.shareSettings.length
  const failedBatch=setupExpiry('baidu')
  failedBatch.days.value=3
  await failedBatch.run()
  assert.equal(calls.shareSettings.length,beforeWrites)
  failedBatch.agreed.value=true
  controls.failExpiryPreflight=true
  await failedBatch.run()
  controls.failExpiryPreflight=false
  assert.match(failedBatch.error.value,/未修改任何分享/)
  assert.equal(calls.shareSettings.length,beforeWrites)
  assert.deepEqual(calls.expiryPreflight.at(-1),{handles:['expiry-a','expiry-b'],days:3})
  const hooks=await import(fileURL('hooks.mjs'))
  hooks.cleanup.at(-1)()
  const unknownBatch=setupExpiry('pan123')
  unknownBatch.days.value=3
  unknownBatch.agreed.value=true
  assert.equal(unknownBatch.canStart.value,false)
  unknownBatch.days.value=7
  controls.settingsUnknown=true
  await unknownBatch.run()
  controls.settingsUnknown=false
  assert.deepEqual(calls.shareSettings.slice(beforeWrites),[{handle:'expiry-a',password:'',days:7}])
  assert.deepEqual(unknownBatch.rows.value.map(row=>row.state),['unknown','skipped'])
  const [pendingRow,skippedRow]=unknownBatch.rows.value
  const checksBefore=calls.shareChecks.length
  await unknownBatch.check(skippedRow)
  assert.equal(calls.shareChecks.length,checksBefore)
  controls.shareCheckResult={id:'different-receipt'}
  await unknownBatch.check(pendingRow)
  assert.equal(pendingRow.state,'unknown')
  assert.equal(pendingRow.receiptId,'settings-fixture')
  assert.match(unknownBatch.error.value,/原回执已保留/)
  delete controls.shareCheckResult
  controls.holdShareCheck=true
  const checkPending=unknownBatch.check(pendingRow)
  await unknownBatch.check(pendingRow)
  assert.deepEqual(calls.shareChecks.slice(checksBefore),['settings-fixture','settings-fixture'])
  controls.holdShareCheck=false
  controls.releaseShareCheck()
  await checkPending
  assert.deepEqual(unknownBatch.rows.value.map(row=>row.state),['ready','skipped'])
  assert.equal(unknownBatch.done.value,1)
  assert.equal(pendingRow.receiptId,undefined)
  assert.equal(calls.shareSettings.length,beforeWrites+1)
  hooks.cleanup.at(-1)()
  const stoppedBatch=setupExpiry('baidu')
  stoppedBatch.days.value=7
  stoppedBatch.agreed.value=true
  controls.holdExpiryPreflight=true
  const pending=stoppedBatch.run()
  await settle()
  stoppedBatch.stopping.value=true
  controls.holdExpiryPreflight=false
  controls.releaseExpiryPreflight()
  await pending
  assert.equal(calls.shareSettings.length,beforeWrites+1)
  hooks.cleanup.at(-1)()
  expiryBrowser.accountId.value='gy'
  await settle()
  expiryBrowser.items.value=items
  expiryBrowser.selectBatch()
  expiryBrowser.prepareBatchExpiry()
  assert.equal(expiryBrowser.batchExpiryTargets.value.length,2)
  const guangyaBatch=setupExpiry('guangya')
  guangyaBatch.days.value=7
  guangyaBatch.agreed.value=true
  await guangyaBatch.run()
  guangyaBatch.access.value='set'
  guangyaBatch.password.value='bad'
  await guangyaBatch.run()
  assert.equal(calls.guangyaSettingsPreflight,undefined)
  assert.equal(calls.shareSettings.length,beforeWrites+1)
  guangyaBatch.password.value='New4'
  await guangyaBatch.run()
  assert.deepEqual(calls.guangyaSettingsPreflight.at(-1),{handles:['expiry-a','expiry-b'],password:'New4',days:7})
  assert.deepEqual(calls.shareSettings.slice(beforeWrites+1),['expiry-a','expiry-b'].map(handle=>({handle,password:'New4',days:7})))
  assert.deepEqual(guangyaBatch.rows.value.map(row=>row.state),['ready','ready'])
  hooks.cleanup.at(-1)()
  const clearBatch=setupExpiry('guangya')
  clearBatch.days.value=0
  clearBatch.access.value='clear'
  clearBatch.password.value='New4'
  clearBatch.agreed.value=true
  controls.settingsUnknown=true
  await clearBatch.run()
  controls.settingsUnknown=false
  assert.deepEqual(calls.guangyaSettingsPreflight.at(-1),{handles:['expiry-a','expiry-b'],password:'',days:0})
  assert.deepEqual(calls.shareSettings.slice(beforeWrites+3),[{handle:'expiry-a',password:'',days:0}])
  assert.deepEqual(clearBatch.rows.value.map(row=>row.state),['unknown','skipped'])
  hooks.cleanup.at(-1)()
  const blockedSettings=setupExpiry('guangya')
  blockedSettings.days.value=7
  blockedSettings.access.value='clear'
  blockedSettings.agreed.value=true
  controls.failGuangyaSettingsPreflight=true
  await blockedSettings.run()
  controls.failGuangyaSettingsPreflight=false
  assert.match(blockedSettings.error.value,/未修改任何分享/)
  assert.equal(calls.shareSettings.length,beforeWrites+4)
  hooks.cleanup.at(-1)()
  expiryBrowser.accountId.value='q'
  await settle()
  assert.equal(expiryBrowser.batchExpiryTargets.value,null)
  assert.equal(expiryBrowser.canEditBatchSettings.value,false)
  hooks.cleanup.at(-1)()
  const resumed=expiryComponent.setup({accountName:'光鸭账号',kind:'guangya',targets:items},{expose(){},emit(){}})
  resumed.days.value=7
  resumed.access.value='set'
  resumed.password.value='New4'
  resumed.agreed.value=true
  const writesBeforeResume=calls.shareSettings.length
  controls.holdSettings=true
  const firstRun=resumed.run()
  await settle()
  resumed.stopping.value=true
  controls.holdSettings=false
  controls.releaseSettings()
  await firstRun
  assert.deepEqual(resumed.rows.value.map(row=>row.state),['ready','skipped'])
  assert.equal(resumed.canRetry.value,true)
  resumed.prepareRetry()
  assert.equal(resumed.agreed.value,false)
  resumed.agreed.value=true
  controls.failGuangyaSettingsPreflight=true
  await resumed.run()
  controls.failGuangyaSettingsPreflight=false
  assert.deepEqual(resumed.rows.value.map(row=>row.state),['ready','skipped'])
  assert.equal(calls.shareSettings.length,writesBeforeResume+1)
  resumed.prepareRetry()
  resumed.agreed.value=true
  await resumed.run()
  assert.deepEqual(calls.guangyaSettingsPreflight.at(-1),{handles:['expiry-b'],password:'New4',days:7})
  assert.deepEqual(calls.shareSettings.slice(writesBeforeResume),['expiry-a','expiry-b'].map(handle=>({handle,password:'New4',days:7})))
  assert.deepEqual(resumed.rows.value.map(row=>row.state),['ready','ready'])
  hooks.cleanup.at(-1)()
  console.log('PASS: batch share expiry preflights all targets and preserves access codes, with stop on unknown results.')
 }
 const recycleSearch=recycleComponent.setup(reactive({accounts:[{id:'b',kind:'baidu'},{id:'lz',kind:'lanzou'}],blocked:false}),{expose(){},emit(){}})
 await settle()
 const recycleRow=(id,name,originalPath)=>({id,name,originalPath,kind:'file',restoreHandle:''})
 const firstRecycleRow=recycleRow('1','同名.zip','/旧目录/同名.zip')
 const secondRecycleRow=recycleRow('2','另一项.txt','/旧目录/另一项.txt')
 controls.recycleSearchPages={
  1:{page:1,hasMore:true,items:[firstRecycleRow]},
  2:{page:2,hasMore:false,items:[secondRecycleRow]},
 }
 recycleSearch.query.value='旧目录'
 await settle()
 const restoreWrites=calls.recycleRestores.length,purgeWrites=calls.purges.length
 await recycleSearch.searchAcrossPages()
 assert.deepEqual(recycleSearch.searchPages.value,[{page:1,count:1},{page:2,count:1}])
 assert.deepEqual(recycleSearch.searchMatches.value.map(row=>[row.page,row.entry.id]),[[1,'1'],[2,'2']])
 const [oldFirst,oldSecond]=recycleSearch.searchMatches.value
 const listsAfterSearch=calls.recycleLists.length
 recycleSearch.selectionMode.value='purge'
 recycleSearch.queueSearchMatch(oldFirst)
 recycleSearch.queueSearchMatch(oldSecond)
 recycleSearch.queueSearchMatch(oldSecond)
 assert.deepEqual(recycleSearch.purgeQueue.value.map(row=>[row.page,row.entry.id]),[[1,'1'],[2,'2']])
 assert.equal(calls.recycleLists.length,listsAfterSearch)
 recycleSearch.prepareQueuedPurge()
 assert.deepEqual(recycleSearch.purgeTarget.value.selections.map(row=>row.page),[1,2])
 await recycleSearch.closePurge()
 recycleSearch.purgeQueue.value=[]
 recycleSearch.selectionMode.value='restore'
 await recycleSearch.openSearchPage(2)
 assert.deepEqual(recycleSearch.rows.value.map(row=>row.id),['2'])
 assert.equal(calls.recycleRestores.length,restoreWrites)
 assert.equal(calls.purges.length,purgeWrites)
 controls.recycleSearchPages[2].items=[firstRecycleRow]
 await recycleSearch.searchAcrossPages()
 assert.deepEqual(recycleSearch.searchPages.value,[{page:1,count:1}])
 assert.deepEqual(recycleSearch.searchMatches.value.map(row=>row.entry.id),['1'])
 recycleSearch.queueSearchMatch(oldSecond)
 assert.equal(recycleSearch.restoreQueue.value.length,0)
 assert.match(recycleSearch.searchSummary.value,/重复.*不完整/)
 controls.recycleSearchPages[2]={page:2,hasMore:false,incomplete:true,items:[secondRecycleRow]}
 await recycleSearch.searchAcrossPages()
 assert.match(recycleSearch.searchSummary.value,/不完整/)
 const [reliableMatch,incompleteMatch]=recycleSearch.searchMatches.value
 assert.equal(reliableMatch.incomplete,false)
 assert.equal(recycleSearch.searchQueueBlock(incompleteMatch),'来源页不完整')
 recycleSearch.selectionMode.value='purge'
 recycleSearch.queueSearchMatch(incompleteMatch)
 assert.equal(recycleSearch.purgeQueue.value.length,0)
 recycleSearch.selectionMode.value='restore'
 await recycleSearch.openSearchPage(2)
 assert.match(recycleSearch.error.value,/操作已禁用/)
 controls.holdRecycle=true
 const stoppedRecycleSearch=recycleSearch.searchAcrossPages()
 await settle()
 recycleSearch.searchStop.value=true
 controls.holdRecycle=false
 controls.releaseRecycle()
 await stoppedRecycleSearch
 assert.deepEqual(recycleSearch.searchPages.value,[{page:1,count:1}])
 assert.match(recycleSearch.searchSummary.value,/已停止.*不完整/)
 assert.equal(recycleSearch.searchCanContinue.value,true)
 await recycleSearch.searchAcrossPages(true)
 assert.deepEqual(recycleSearch.searchPages.value,[{page:1,count:1},{page:2,count:1}])
 assert.equal(recycleSearch.searchCanContinue.value,false)
 assert.match(recycleSearch.searchSummary.value,/不完整/)
 assert.deepEqual(recycleSearch.searchMatches.value.map(row=>[row.page,row.incomplete]),[[2,true]])
 assert.equal(calls.recycleRestores.length,restoreWrites)
 assert.equal(calls.purges.length,purgeWrites)
 recycleSearch.accountId.value='lz'
 await settle()
 assert.equal(recycleSearch.searchSummary.value,'')
 assert.deepEqual(recycleSearch.searchMatches.value,[])
 recycleSearch.query.value='同名'
 await settle()
 await recycleSearch.searchAcrossPages()
 assert.match(recycleSearch.searchSummary.value,/仅搜索网页可识别/)
 delete controls.recycleSearchPages
 ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
 console.log('PASS: recycle search queues valid matches from multiple source pages and excludes stale and incomplete matches before mutation preflight.')
 {
  const queueProps=reactive({accounts:[{id:'q',kind:'quark'},{id:'b',kind:'baidu'}],blocked:false})
  const first={...recycleRow('restore-1','第一项','/目录一/第一项'),restoreHandle:'handle-page-1'}
  const later={...recycleRow('restore-2','第二项','/目录二/第二项'),restoreHandle:'handle-page-2'}
  controls.recycleReceipts=[]
  controls.recycleSearchPages={1:{page:1,items:[first],hasMore:true},2:{page:2,items:[later],hasMore:false}}
  const queuedRecycle=recycleComponent.setup(queueProps,{expose(){},emit(){}})
  await settle()
  const preflightCount=calls.recyclePreflights.length
  const writeCount=calls.recycleRestores.length
  queuedRecycle.query.value='目录'
  await settle()
  await queuedRecycle.searchAcrossPages()
  for(const row of queuedRecycle.searchMatches.value)queuedRecycle.queueSearchMatch(row)
  assert.equal(queuedRecycle.page.value,1)
  queuedRecycle.toggleSelected(queuedRecycle.rows.value[0])
  queuedRecycle.queueSelected()
  assert.deepEqual(queuedRecycle.restoreQueue.value.map(row=>row.page),[1,2])
  await queuedRecycle.prepareBatch(undefined,true)
  assert.deepEqual(queuedRecycle.batchConfirm.value.map(row=>row.id),['restore-2','restore-1'])
  assert.equal(calls.recyclePreflights.length,preflightCount)
  assert.equal(calls.recycleRestores.length,writeCount)
  await queuedRecycle.cancelBatch()
  assert.equal(queuedRecycle.restoreQueue.value.length,2)
  controls.failRecyclePreflight=true
  await queuedRecycle.prepareBatch(undefined,true)
  await queuedRecycle.runBatch()
  assert.deepEqual(calls.recyclePreflights.at(-1),['handle-page-2','handle-page-1'])
  assert.equal(calls.recycleRestores.length,writeCount)
  assert.deepEqual(queuedRecycle.restoreQueue.value.map(row=>row.entry.id),['restore-1','restore-2'])
  controls.failRecyclePreflight=false
  await queuedRecycle.prepareBatch(undefined,true)
  controls.recycleRestoreState='confirmed'
  await queuedRecycle.runBatch()
  assert.deepEqual(calls.recycleRestores.slice(writeCount),['handle-page-2','handle-page-1'])
  await queuedRecycle.load(1)
  queuedRecycle.selectVisible()
  queuedRecycle.queueSelected()
  assert.equal(queuedRecycle.restoreQueue.value.length,1)
  queuedRecycle.accountId.value='b'
  await settle()
  assert.equal(queuedRecycle.restoreQueue.value.length,0)
  delete controls.recycleSearchPages
  controls.recycleRestoreState='unknown'
  ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
  console.log('PASS: queued restore deduplicates across pages, preflights all targets, sends later pages first and resets on account change.')
 }
 {
  const purgeQueueProps=reactive({accounts:[{id:'q',kind:'quark'},{id:'b',kind:'baidu'}],blocked:false})
  const first=recycleRow('purge-1','同名项目','/一/同名项目')
  const later={...recycleRow('purge-2','同名项目','/二/同名项目'),kind:'folder'}
  controls.recycleSearchPages={1:{page:1,items:[first],hasMore:true},2:{page:2,items:[later],hasMore:false}}
  const browser=recycleComponent.setup(purgeQueueProps,{expose(){},emit(){}})
  await settle()
  browser.selectionMode.value='purge'
  await nextTick()
  browser.selectVisible()
  browser.queueSelected()
  await browser.load(2)
  browser.selectVisible()
  browser.queueSelected()
  const count=calls.purges.length
  browser.prepareQueuedPurge()
  assert.deepEqual(browser.purgeTarget.value.selections.map(row=>[row.page,row.entry.id]),[[1,'purge-1'],[2,'purge-2']])
  const dialog=purgeComponent.setup(browser.purgeTarget.value,{expose(){},emit(){}})
  await dialog.start()
  assert.equal(calls.purges.length,count)
  dialog.agreed.value=true
  dialog.confirmation.value='永久删除'
  controls.failPurgePreflight=true
  await dialog.start()
  controls.failPurgePreflight=false
  assert.match(dialog.status.value,/未提交永久删除/)
  assert.equal(calls.purges.length,count)
  assert.deepEqual(calls.crossPagePurgePreflights.at(-1).selections.map(row=>row.page),[1,2])
  const hooks=await import(fileURL('hooks.mjs'))
  hooks.cleanup.at(-1)()
  await browser.closePurge()
  purgeQueueProps.refreshKey='fresh'
  await settle()
  assert.equal(browser.purgeQueue.value.length,0)
  hooks.cleanup.at(-1)()
  delete controls.recycleSearchPages
  console.log('PASS: cross-page recycle purge is account-bound, checks all source pages and submits nothing on stale selection.')
 }
 const restoreWritesBeforeCoverage=calls.recycleRestores.length
 const blueCoverage=recycleComponent.setup(reactive({accounts:[{id:'lz',kind:'lanzou'},{id:'b',kind:'baidu'}],blocked:false}),{expose(){},emit(){}})
 await settle()
 const coverageFolder={...purgeEntries[1],id:'folder:101',name:'folder'}
 const coverageFile={...purgeEntries[0],id:'file:1',name:'file'}
 controls.recycleCoverage=[
  {name:'folder',kind:'folder',page:2,state:'candidate',entry:coverageFolder},
  {name:'pending',kind:'file',page:1,state:'pending'},
  {name:'unknown',kind:'file',page:1,state:'website'},
  ...Array.from({length:22},(_,i)=>({name:'unverified '+i,kind:'file',page:1,state:'website'})),
  {name:'file',kind:'file',page:1,state:'candidate',entry:coverageFile},
 ]
 await blueCoverage.loadCoverage()
 assert.equal(calls.recycleCoverage.at(-1),'lz')
 assert.equal(blueCoverage.coverageCounts.value.candidate,2)
 assert.equal(blueCoverage.coverageCounts.value.pending,1)
 blueCoverage.toggleCoverageSelection(blueCoverage.coverage.value[1])
 assert.deepEqual(blueCoverage.coverageSelection.value,[])
 blueCoverage.toggleCoverageSelection(blueCoverage.coverageVisible.value[0])
 blueCoverage.coveragePage.value=2
 blueCoverage.toggleCoverageSelection(blueCoverage.coverageVisible.value[0])
 blueCoverage.prepareCoveragePurge()
 const target=blueCoverage.purgeTarget.value
 assert.equal(target.accountId,'lz')
 assert.equal(target.page,0)
 assert.deepEqual(target.entries.map(entry=>entry.id),['folder:101','file:1'])
 const crossPageDialog=purgeComponent.setup({...target,entries:[coverageFile],lanzou:true},{expose(){},emit(){}})
 crossPageDialog.agreed.value=true
 crossPageDialog.confirmation.value='永久删除'
 await crossPageDialog.start()
 assert.equal(calls.purges.length,purgeWrites)
 await crossPageDialog.preview()
 assert.equal(calls.purgePreflights.at(-1).page,0)
 assert.equal(crossPageDialog.canStart.value,true)
 assert.equal(calls.purges.length,purgeWrites)
 await blueCoverage.closePurge()
 blueCoverage.coverageFilter.value='website'
 await nextTick()
 assert.equal(blueCoverage.coverageVisible.value[0].name,'unknown')
 await blueCoverage.openCoveragePage(blueCoverage.coverage.value[0])
 assert.equal(calls.recycleLists.at(-1).page,2)
 assert.equal(blueCoverage.coverage.value,null)
 assert.deepEqual(blueCoverage.coverageSelection.value,[])
 assert.equal(calls.recycleRestores.length,restoreWritesBeforeCoverage)
 assert.equal(calls.purges.length,purgeWrites)
 const listedCoverageFile={...coverageFile,fileId:'1',parentId:'',originalPath:'',restoreHandle:''}
 controls.recycleCoverage=[{name:'file',kind:'file',page:1,state:'candidate',entry:listedCoverageFile}]
 await blueCoverage.loadCoverage()
 blueCoverage.toggleCoverageSelection(blueCoverage.coverage.value[0])
 blueCoverage.prepareCoveragePurge()
 assert.equal(blueCoverage.purgeTarget.value.page,0)
 assert.deepEqual(blueCoverage.purgeTarget.value.entries,[listedCoverageFile])
 assert.equal(calls.purges.length,purgeWrites)
 await blueCoverage.closePurge()
 controls.recycleCoverage=[]
 await blueCoverage.loadCoverage()
 assert.equal(blueCoverage.coverage.value,null)
 assert.match(blueCoverage.coverageError.value,/未能确认/)
 const beforeCoverageSwitch=calls.recycleCoverage.length
 blueCoverage.accountId.value='b'
 await settle()
 assert.equal(blueCoverage.coverageError.value,'')
 await blueCoverage.loadCoverage()
 assert.equal(calls.recycleCoverage.length,beforeCoverageSwitch)
 delete controls.recycleCoverage
 ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
 console.log('PASS: LanZou recycle coverage is account-scoped, read-only and reloads the selected list page.')
 historyProps.accounts[0].name='归档账号'
 controls.shareHistory=[readyReceipt,{...unknownReceipt,accountId:'removed'}]
 await history.refresh()
 assert.equal(history.accountLabel(readyReceipt),'夸克网盘 · 归档账号')
 assert.equal(history.accountLabel(controls.shareHistory[1]),'夸克网盘 · 原账号已移除')
 history.query.value='归档账号'
 assert.deepEqual(history.rows.value.map(row=>row.result.id),['history-ready'])
 history.query.value=''
 const batchHistoryProps=reactive({accounts:[{id:'a',kind:'quark',name:'归档账号'},{id:'baidu-history',kind:'baidu',name:'网盘归档'}],blocked:false})
 const batchHistory=historyComponent.setup(batchHistoryProps,{expose(){},emit(){}})
 const baiduReceipt={...readyReceipt,accountId:'baidu-history',result:{
  ...readyReceipt.result,id:'baidu-ready',kind:'baidu',name:'Archive.zip',url:'https://pan.baidu.com/s/1Archive',password:'B2c3',
 }}
 const failedReceipt={...unknownReceipt,result:{...unknownReceipt.result,id:'failed-history',state:'failed',url:'https://pan.quark.cn/s/failed'}}
 controls.shareHistory=[{...readyReceipt,action:'create'},{...baiduReceipt,action:'settings'},{...unknownReceipt,action:'password'},failedReceipt]
 await settle()
 await batchHistory.refresh()
 assert.equal(batchHistory.copyableRows.value.length,2)
 const beforeBatchCopy={reads:calls.shareHistoryReads,checks:calls.shareChecks.length,creates:calls.shares.length}
 await batchHistory.copyFiltered()
 assert.match(calls.reports.at(-1),/夸克网盘 · 归档账号\n文件：One\.zip/)
 assert.match(calls.reports.at(-1),/百度网盘 · 网盘归档\n文件：Archive\.zip/)
 assert.doesNotMatch(calls.reports.at(-1),/Two\.zip|never-copy-history|baidu-ready/)
 assert.equal(batchHistory.message.value,'已复制 2 条完整分享信息')
 assert.deepEqual(batchHistory.availableActions.value.map(([key])=>key),['create','password','settings','unknown'])
 batchHistory.action.value='settings'
 assert.deepEqual(batchHistory.rows.value.map(row=>row.result.id),['baidu-ready'])
 await batchHistory.copyFiltered(true)
 assert.equal(calls.reports.at(-1),'https://pan.baidu.com/s/1Archive?pwd=B2c3')
 batchHistory.action.value='password'
 assert.equal(batchHistory.copyableRows.value.length,0)
 batchHistory.action.value='unknown'
 assert.deepEqual(batchHistory.rows.value.map(row=>row.result.id),['failed-history'])
 batchHistory.action.value=''
 batchHistory.kind.value='baidu'
 await batchHistory.copyFiltered(true)
 assert.equal(calls.reports.at(-1),'https://pan.baidu.com/s/1Archive?pwd=B2c3')
 batchHistory.autoFill.value=false
 await batchHistory.copyFiltered(true)
 assert.equal(calls.reports.at(-1),'https://pan.baidu.com/s/1Archive')
 const copiesAfterFiltering=calls.reports.length
 batchHistoryProps.blocked=true
 await batchHistory.copyFiltered(true)
 batchHistoryProps.blocked=false
 batchHistory.status.value='failed'
 await batchHistory.copyFiltered()
 assert.equal(calls.reports.length,copiesAfterFiltering)
 batchHistory.status.value=''
 batchHistory.kind.value=''
 controls.copyFail=true
 await batchHistory.copyFiltered()
 assert.match(batchHistory.error.value,/模拟剪贴板失败/)
 delete controls.copyFail
 assert.deepEqual({reads:calls.shareHistoryReads,checks:calls.shareChecks.length,creates:calls.shares.length},beforeBatchCopy)
 console.log('PASS: local share history copies filtered saved links or full details without cloud requests, with provider labels and access-code preferences.')
 {
  const paged=historyComponent.setup(reactive({accounts:[
   {id:'first-blue',kind:'lanzou',name:'归档'},{id:'second-blue',kind:'lanzou',name:'归档'},
  ]}),{expose(){},emit(){}})
  const receipt=(index,accountId)=>({
   ...readyReceipt,accountId,canLocate:true,action:'get',
   result:{...readyReceipt.result,kind:'lanzou',id:'blue-'+index,name:'蓝奏记录 '+index,url:'https://fixture.lanzoub.com/i'+index},
  })
  controls.shareHistory=[
   ...Array.from({length:54},(_,index)=>receipt(index,'first-blue')),
   receipt(54,'second-blue'),
  ]
  await settle()
  await paged.refresh()
  assert.equal(paged.pageCount.value,2)
  assert.equal(paged.visibleRows.value.length,50)
  paged.selectedLanzou.value=['blue-0']
  const readsBeforePage=calls.shareHistoryReads
  paged.changePage(2)
  assert.equal(paged.visibleRows.value.length,5)
  assert.deepEqual(paged.selectedLanzouRows.value.map(row=>row.result.id),['blue-0'])
  assert.equal(paged.canSelectLanzou(paged.visibleRows.value.at(-1)),false)
  assert.deepEqual([...paged.availableAccounts.value.values()],['蓝奏云 · 归档（1）','蓝奏云 · 归档（2）'])
  assert.equal(calls.shareHistoryReads,readsBeforePage)
  await paged.copyFiltered(true)
  assert.equal(calls.reports.at(-1).split('\n').length,55)
  paged.accountFilter.value=[...paged.availableAccounts.value.keys()][1]
  await settle()
  assert.equal(paged.page.value,1)
  assert.deepEqual(paged.visibleRows.value.map(row=>row.result.id),['blue-54'])
  assert.deepEqual(paged.selectedLanzou.value,[])
  await paged.copyFiltered(true)
  assert.equal(calls.reports.at(-1),'https://fixture.lanzoub.com/i54')
  paged.accountFilter.value=''
  await settle()
  paged.changePage(2)
  controls.shareHistory=controls.shareHistory.slice(0,20)
  await paged.refresh()
  await settle()
  assert.equal(paged.page.value,1)
  assert.equal(paged.visibleRows.value.length,20)
  ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
  console.log('PASS: local share history pages without extra reads, distinguishes duplicate account labels, retains Lanzou selection across pages, and copies the full filter.')
 }
 {
  const props=reactive({accounts:[{id:'qa',kind:'quark'},{id:'qb',kind:'quark'}],refreshKey:'0'})
  const checkBatch=historyComponent.setup(props,{expose(){},emit(){}})
  const receipt=(index,accountId)=>({
   ...readyReceipt,accountId,result:{...readyReceipt.result,id:'review-'+index},
  })
  controls.shareHistory=[
   ...Array.from({length:22},(_,index)=>receipt(index,'qa')),receipt(22,'qb'),
  ]
  await settle()
  await checkBatch.refresh()
  assert.equal(checkBatch.pageCheckTargets.value.length,0)
  checkBatch.accountFilter.value=[...checkBatch.availableAccounts.value.keys()][0]
  await settle()
  assert.equal(checkBatch.pageCheckTargets.value.length,20)
  const beforeChecks=calls.shareChecks.length,beforeWrites=calls.shares.length
  controls.holdShareCheck=true
  const batchCheckPending=checkBatch.checkPage()
  await checkBatch.checkPage()
  assert.deepEqual(calls.shareChecks.slice(beforeChecks),['review-0'])
  checkBatch.stopPageCheck()
  controls.holdShareCheck=false
  controls.releaseShareCheck()
  await batchCheckPending
  assert.equal(checkBatch.message.value,'已检查 1 / 20 条原回执')
  assert.deepEqual(calls.shareChecks.slice(beforeChecks),['review-0'])
  controls.holdShareCheck=true
  const staleCheck=checkBatch.checkPage()
  props.refreshKey='1'
  await settle()
  controls.holdShareCheck=false
  controls.releaseShareCheck()
  await staleCheck
  assert.deepEqual(calls.shareChecks.slice(beforeChecks),['review-0','review-1'])
  assert.equal(checkBatch.message.value,'')
  assert.equal(checkBatch.busy.value,false)
  assert.equal(checkBatch.records.value.length,23)
  const resumedAt=calls.shareChecks.length
  await checkBatch.checkPage()
  assert.deepEqual(calls.shareChecks.slice(resumedAt),Array.from({length:20},(_,index)=>'review-'+index))
  assert.equal(checkBatch.message.value,'已检查 20 / 20 条原回执')
  assert.equal(checkBatch.pageCheckTargets.value.length,2)
  const continuedAt=calls.shareChecks.length
  await checkBatch.checkPage()
  assert.deepEqual(calls.shareChecks.slice(continuedAt),['review-20','review-21'])
  assert.equal(checkBatch.pageCheckTargets.value.length,0)
  await checkBatch.checkPage()
  assert.equal(calls.shareChecks.length,continuedAt+2)
  await checkBatch.checkPage(true)
  assert.deepEqual(calls.shareChecks.slice(continuedAt+2),Array.from({length:20},(_,index)=>'review-'+index))
  assert.equal(calls.shares.length,beforeWrites)
  ;(await import(fileURL('hooks.mjs'))).cleanup.at(-1)()
  console.log('PASS: local receipt batch check uses at most 20 per run, continues with unread receipts after stopping, and never crosses account scope.')
 }
 {
  const dialog=shareComponent.setup({
   accountId:'p',parentId:'0',kind:'pan123',entry:{id:'22',name:'资料',folder:true},
  },{expose(){},emit(){}})
  dialog.paid.value=true
  dialog.paidPrice.value='12.05'
  dialog.paidDescription.value='资源说明'
  dialog.days.value=7
  dialog.protectedShare.value=true
  const beforePaid=calls.shares.length
  await dialog.submit()
  assert.equal(calls.shares.length,beforePaid)
  dialog.confirmPaid.value=true
  dialog.paidPrice.value='10000'
  await dialog.submit()
  assert.equal(calls.shares.length,beforePaid)
  dialog.paidPrice.value='12.05'
  controls.shareUnknown=true
  await dialog.submit()
  controls.shareUnknown=false
  await dialog.submit()
  assert.deepEqual(calls.shares.slice(beforePaid),[{
   accountId:'p',parentId:'0',entryId:'22',name:'资料',folder:true,action:'create',password:'',days:0,
   paidPriceFen123:1205,paidDescription123:'资源说明',
  }])
  assert.equal(dialog.resultLink.value,'')
  console.log('PASS: 123 paid creation requires consent, sends exact integer cents, and does not copy a pending link.')
 }
 controls.shareHistory=[]
 console.log('PASS: trash confirmation/whole-batch preflight/null response/unknown stop/stop-after-current/double submit/unmount, receipt pagination/read-only check/save errors, browser locking/refresh/provider guards and existing cloud UI regressions.');
 console.log('PASS: upload and move regressions, ID folder trails/picker paths, DAV/123/Guangya/Quark download capability/progress/cancel/errors/concurrency, folder retry preservation/guards/cancel, navigation guards and listener cleanup. Synthetic component checks only; native gestures and dialogs not exercised.');
} finally {assert.equal(path.dirname(path.resolve(dir)),path.resolve(os.tmpdir()));assert.ok(path.basename(dir).startsWith('multipan-cloud-drag-'));await fs.rm(dir,{recursive:true,force:true});}
