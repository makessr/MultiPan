// Synthetic UI fixture. This module is only used by the separate preview config;
// it never reads local accounts, credentials or files and makes no cloud calls.
import {emit} from './events';
const kinds=['lanzou','quark','pan123','baidu','ctdav','guangya'];
const names=['蓝奏云','夸克网盘','123 云盘','百度网盘','诚通网盘','光鸭云盘'];
const accounts=kinds.map((kind,i)=>({id:'preview-'+i,name:'演示账号',kind,endpoint:'https://preview.invalid',username:'',folder:i===0?'-1':['quark','pan123'].includes(kind)?'0':kind==='guangya'?'root':'/',folderDisplay:'根目录',userAgent:''}));
let tasks:any[]=['running','failed','success','paused'].map((status,i)=>({id:'preview-task-'+i,kind:kinds[i],accountName:'演示账号',accountId:accounts[i].id,file:{name:['产品设计方案.pdf','项目素材.zip','会议记录.docx','年度备份.zip'][i],size:48234496,path:'C:\\Preview\\fixture.zip'},folder:accounts[i].folder,folderDisplay:i===1?'根目录 / 项目备份':'根目录',status,sent:status==='success'?48234496:status==='running'?18329108:0,created:new Date().toISOString(),message:status==='failed'?'模拟网络中断，请检查连接后重新上传。':status==='paused'?'上传已停止':status==='success'?'上传完成':'正在上传'}));
export async function DesktopPlatform(){return 'windows'}
export async function Snapshot(){return {accounts,tasks:tasks.map(t=>({...t,file:{...t.file}})),downloads:queueJobs.map(j=>({...j,input:{...j.input},result:{...j.result}})),settings:transferSettings,storageError:''}}
const roots=(id:string)=>accounts.find(a=>a.id===id)?.folder??'/';
const entries=[{id:'work',name:'工作资料',folder:true,size:-1},{id:'photos',name:'旅行照片',folder:true,size:-1},{id:'archive',name:'软件备份',folder:true,size:-1},{id:'1',name:'2026 年度计划.pdf',folder:false,size:2818048},{id:'2',name:'项目素材.zip',folder:false,size:48234496},{id:'3',name:'产品设计方案.fig',folder:false,size:18350080},{id:'4',name:'会议记录.docx',folder:false,size:286720},{id:'5',name:'安装包与使用说明.zip',folder:false,size:124780544}];
export async function ListCloudFiles(id:string,parent:string){if(new URLSearchParams(location.search).get('accountError')==='guangya'&&accounts.find(a=>a.id===id)?.kind==='guangya')throw new Error('光鸭登录失效或访问被拒绝，请重新登录 [GY-AUTH/0.8.1]（模拟）');await new Promise(r=>setTimeout(r,160));return {currentId:parent,breadcrumbs:parent===roots(id)?[{id:roots(id),name:'根目录'}]:[{id:roots(id),name:'根目录'},{id:parent,name:entries.find(e=>e.id===parent)?.name??'工作资料'}],entries:parent===roots(id)?entries:entries.filter(e=>!e.folder)}}
export async function ListCloudFolders(id:string,parent:string){const page=await ListCloudFiles(id,parent);return {...page,folders:entries.filter(e=>e.folder)}}
export async function PickFiles(){return [{path:'C:\\Preview\\产品设计方案.pdf',name:'产品设计方案.pdf',size:2871296,modified:0}]}
export async function Enqueue(files:any[],targets:any[]){tasks=targets.flatMap((target,i)=>files.map(file=>({id:'task-'+i,kind:accounts.find(a=>a.id===target.accountId)?.kind,accountName:'演示账号',accountId:target.accountId,file,folder:target.folder,folderDisplay:'根目录 / 工作资料',status:i?'success':'running',sent:i?file.size:Math.round(file.size*.38),created:new Date().toISOString(),message:i?'上传完成':'正在上传'})));return tasks.length}
export async function SetDefaultFolder(id:string,folder:string,path:string){const a=accounts.find(a=>a.id===id);if(a){a.folder=folder;a.folderDisplay=path}}
export async function SaveAccount(input:any){const {secret,...values}=input;const a=accounts.find(a=>a.id===input.id);if(a){Object.assign(a,values);return {...a}}return {...accounts[0],...values}}
export async function CreateCloudFolder(input:any){entries.unshift({id:'new',name:input.name,folder:true,size:-1});return {id:'new',name:input.name}}
export async function RenameCloudEntry(input:any){const e=entries.find(e=>e.id===input.entryId);if(e)e.name=input.newName;return {name:input.newName}}
export async function MoveCloudFile(){return {}}
export async function MoveCloudFolder(){return {}}
export async function Stop(id:string){const t=tasks.find(t=>t.id===id);if(t)t.status='paused'}
export async function Retry(id:string){const t=tasks.find(t=>t.id===id);if(t)t.status='running'}
const delay=(ms:number)=>new Promise(r=>setTimeout(r,ms));
let cancelled=false;
export async function DownloadCloudFile(input:any){cancelled=false;for(let i=0;i<=5;i++){if(cancelled)return {cancelled:true};emit('multipan:download-progress',{received:i*1048576,total:5242880});await delay(650)}return {path:'C:\\Downloads\\'+input.name,bytes:5242880,verification:'unverified',details:'没有可用的原件或可信内容摘要；大小符合不代表完整性已验证。'}}
const items=()=>[{name:'项目说明.pdf',status:'saved',result:{path:'C:\\Downloads\\项目说明.pdf',verification:'verified',details:'下载内容与已记录的原件 SHA256 一致。'}},{name:'素材.zip',status:'saved',result:{path:'C:\\Downloads\\素材.zip',verification:'unverified',details:'没有可用的原件或可信内容摘要。'}},{name:'附件.txt',status:'failed',message:'网络连接暂时中断，请重试。',result:{}}];
export async function DownloadCloudFiles(inputs:any[]){return {items:items().slice(0,inputs.length),directory:'C:\\Downloads',cancelled:false}}
export async function DownloadCloudFolder(){return {items:items(),directory:'C:\\Downloads\\工作资料',retryId:'preview-retry',cancelled:false}}
export async function RetryCloudFolder(){return {items:items().map(i=>({...i,status:'saved',message:'',result:{...i.result,path:i.result.path??'C:\\Downloads\\附件.txt',verification:i.result.verification??'verified'}})),directory:'C:\\Downloads\\工作资料',cancelled:false}}
export function CancelDownload(){cancelled=true}
export async function CopyDownloadReport(){}
export async function CancelLanzouLogin(){}

export async function OpenDownloadFolder(_path:string){}

export async function OpenUploadFolder(_taskID:string){}

let transferSettings={uploadConcurrency:3,downloadConcurrency:1,uploadKiB:0,downloadKiB:0,closeToTray:false};
let queueJobs:any[]=[{id:'queue-preview',input:{accountId:'preview-0',parentId:'-1',entryId:'fixture',name:'项目素材.zip'},destination:'C:\\Downloads\\项目素材.zip',kind:'lanzou',status:'paused',message:'已暂停；继续时重新获取节点并检查续传条件',received:8388608,total:48234496,created:'2026-11-16',result:{}},{id:'saved-preview',input:{accountId:'preview-1',parentId:'0',entryId:'fixture2',name:'会议记录.docx'},destination:'C:\\Downloads\\会议记录.docx',kind:'quark',status:'saved',message:'已保存',received:286720,total:286720,created:'2026-11-16',result:{verification:'unverified',details:'没有可信原件摘要，不声称完整性已验证'}}];
export async function SaveTransferSettings(value:any){transferSettings={...value}}
export async function QueueCloudDownloads(inputs:any[]){return inputs.length}
export async function QueueCloudFolder(){return 2}
export async function ControlQueuedDownload(id:string,resume:boolean){const j=queueJobs.find(x=>x.id===id);if(j)j.status=resume?'queued':'paused'}
export async function ClearSavedDownloadHistory(){queueJobs=queueJobs.filter(j=>j.status!=='saved')}
export async function OpenQueuedDownloadFolder(){}
export async function RestartUpload(){}
export async function PickUploadFolder(){return [{path:'C:/Preview/项目资料',name:'项目资料',relativePath:'项目资料',directory:true,size:0,modified:1},{path:'C:/Preview/项目资料/说明.txt',name:'说明.txt',relativePath:'项目资料/说明.txt',directory:false,size:1024,modified:1}]}

export async function BatchQueuedDownloads(ids:string[],action:string){const out:any[]=[];for(const id of new Set(ids)){const j=queueJobs.find(x=>x.id===id);let applied=false,message='当前状态不适用';if(j){if(action==='resume'&&['paused','failed'].includes(j.status)){j.status='queued';applied=true;message='继续请求已提交'}else if(action==='pause'&&['queued','running'].includes(j.status)){j.status='paused';applied=true;message='暂停请求已提交'}else if(action==='remove'&&!['queued','running'].includes(j.status)){queueJobs=queueJobs.filter(x=>x.id!==id);applied=true;message='记录已移除，文件未删除'}}out.push({id,name:j?.input.name??'',applied,message})}return out}

export async function ControlAllQueuedDownloads(action:string){return BatchQueuedDownloads(queueJobs.filter(j=>action==='pause'?['queued','running'].includes(j.status):['paused','failed'].includes(j.status)).map(j=>j.id),action)}
export async function ClearQueuedDownloadPartials(ids:string[]){return ids.map(id=>{const j=queueJobs.find(x=>x.id===id);const applied=!!j&&['paused','failed'].includes(j.status);if(applied){j.received=0;j.message='续传临时文件已清理，继续时从头下载'}return {id,name:j?.input.name??'',applied,message:applied?'续传临时文件已清理，正式文件未删除':'仅允许暂停或失败任务'}})}
export async function ExportQueuedDownloadReport(){return 'C:/Preview/MultiPan-download-results.txt'}

queueJobs.push(...Array.from({length:120},(_,i)=>({id:'page-fixture-'+i,input:{accountId:'preview-1',parentId:'0',entryId:'fixture-page-'+i,name:'分页示例'+(i+1)+'.zip'},destination:'C:/Preview/archive/分页示例'+(i+1)+'.zip',kind:i%2?'quark':'baidu',status:i%4?'saved':'failed',message:i%4?'模拟已保存':'模拟网络中断',received:i%4?1048576:0,total:1048576,created:'2026-11-16',result:{verification:'unverified',details:'模拟记录，不对应真实文件'}})));

// Larger upload fixture exercises paging without scheduling any real work.
tasks.push(...Array.from({length:80},(_,i)=>{const a=accounts[Math.floor(i/40)];const status=i%4===0?'success':i%3===0?'failed':'paused';return {id:'upload-page-'+i,batchId:'preview-directory-batch',accountId:a.id,kind:a.kind,accountName:'演示账号',file:{name:'目录备份'+(i+1)+'.zip',path:'C:/Preview/fixture.zip',size:1048576,relativePath:'项目资料/目录备份'+(i+1)+'.zip'},folder:a.folder,folderDisplay:'根目录 / 项目资料',status,sent:status==='success'?1048576:0,created:'2026-11-16',message:status==='success'?'上传完成':status==='paused'?'已暂停':i%2?'上传会话已过期 ExpiredToken':'登录已过期，请重新登录'}}));

export async function CheckUploadPlan(files:any[],targets:any[]){await delay(300);return {stamp:'preview-stamp',items:files.flatMap((f,fileIndex)=>targets.map((t,targetIndex)=>({fileIndex,targetIndex,name:f.relativePath||f.name,kind:accounts.find(a=>a.id===t.accountId)?.kind,destination:accounts.find(a=>a.id===t.accountId)?.folderDisplay??t.folder,status:!f.directory&&f.name.endsWith('.pdf')&&targetIndex===0?'conflict':f.directory?'reuse':'ready',message:!f.directory&&f.name.endsWith('.pdf')&&targetIndex===0?'目标已有同名项目，默认跳过；模拟检查，无真实文件':'模拟检查通过'})))}}
export async function EnqueueReviewedUploads(files:any[],targets:any[],skips:any[],_stamp:string){const batchId='preview-created-'+Date.now();const omitted=new Set(skips.map(s=>s.fileIndex+':'+s.targetIndex));const created:any[]=[];files.forEach((file,fi)=>targets.forEach((t,ti)=>{if(omitted.has(fi+':'+ti))return;const a=accounts.find(a=>a.id===t.accountId);created.push({id:batchId+'-'+fi+'-'+ti,batchId,kind:a?.kind,accountId:t.accountId,accountName:'演示账号',file:{...file},folder:t.folder,folderDisplay:a?.folderDisplay??t.folder,status:'queued',sent:0,created:new Date().toISOString(),message:'模拟等待任务，不会实际上传'})}));tasks.push(...created);return created.length}
export async function RetryFailedUploads(ids:string[]){return [...new Set(ids)].map(id=>{const t=tasks.find(t=>t.id===id);const applied=t?.status==='failed';if(applied){t.status='queued';t.sent=0;t.message='模拟重试请求已提交'}return {id,name:t?.file.relativePath||t?.file.name||'',applied,message:applied?'重试请求已提交':'当前状态未提交'}})}

const previewShares=new Map<string,any>();
export async function ShareCloudEntry(input:any){await delay(300);const a=accounts.find(a=>a.id===input.accountId);const id='preview-share-'+previewShares.size;const result={id,state:'ready',name:input.name,kind:a?.kind,url:a?.kind==='lanzou'?'https://preview.lanzoub.com/iMock':a?.kind==='guangya'?'https://www.guangyapan.com/s/preview-demo':a?.kind==='baidu'?'https://pan.baidu.com/s/1preview-demo':a?.kind==='pan123'?'https://123.share.123pan.cn/123pan/preview-demo':'https://pan.quark.cn/s/preview-demo',password:input.action==='get'?'read':input.password,days:input.days,message:'模拟分享结果，不对应真实分享；没有云端修改',canCheck:true};previewShares.set(id,{...result,accountId:input.accountId,created:new Date().toISOString(),sourceParent:input.action==='create'?input.parentId:undefined,sourceTargets:input.targets??[{id:input.entryId,name:input.name,folder:input.folder}],paidPriceFen123:input.paidPriceFen123});return {...result}}
export async function CheckCloudShare(id:string){return {...previewShares.get(id),message:'模拟检查原操作结果，没有重新创建'}}

export async function CloudShares(){return [...previewShares.values()].map((r:any)=>({accountId:r.accountId,created:r.created,canOpenSource:!!r.sourceParent,canRecreate:!!r.sourceParent&&r.kind!=='baidu',result:{...r}}))}

export async function ClearCloudShareHistory(){let count=0;for(const [id,result] of previewShares){if(['ready','failed'].includes(result.state)){previewShares.delete(id);count++}}return count}
export async function ClearSelectedCloudShareHistory(ids:string[]){
 if(!ids.length||new Set(ids).size!==ids.length||ids.some(id=>!['ready','failed'].includes(previewShares.get(id)?.state??''))){
  throw Error('所选分享记录已变化，本次未清理。请刷新后重试')
 }
 for(const id of ids)previewShares.delete(id)
 return ids.length
}
export async function LocateCreatedShareSource(accountId:string,id:string){
 const row=previewShares.get(id)
 if(row?.accountId!==accountId||!row.sourceParent)throw Error('预览数据没有原文件目录')
 if(row.kind==='baidu')return {accountId,parentId:row.sourceParent}
 return {accountId,parentId:row.sourceParent,targets:row.sourceTargets,recreation:{receiptId:id,attemptId:crypto.randomUUID(),protected:!!row.password,days:row.days,paidPriceFen123:row.paidPriceFen123}}
}
export async function RecreateCloudShare(receiptID:string,_attemptID:string,input:any){
 const source=previewShares.get(receiptID)
 if(source?.accountId!==input.accountId||source.sourceParent!==input.parentId)throw Error('预览回执来源已变化')
 return ShareCloudEntry(input)
}
export async function ManagedCloudShares(accountId:string,page:number){const kind=accounts.find(a=>a.id===accountId)?.kind??'quark';return {items:page===1&&!previewRevokedKinds.has(kind)?[{id:'preview-'+kind,name:(kind==='pan123'?'123资料':kind==='baidu'?'百度资料':kind==='guangya'?'光鸭资料':'项目资料')+'.zip',status:'有效性待核对',canRevoke:true,canEdit:kind==='guangya'||kind==='baidu',created:'',expiry:kind==='pan123'?'到期：2099-12-12':'有效期以网盘显示为准'}]:[],page,hasMore:page===1,next:page===1&&kind==='pan123'?'preview-cursor':''}}
export async function ReadManagedCloudShare(id:string){const kind=id.includes('pan123')?'pan123':id.includes('baidu')?'baidu':id.includes('guangya')?'guangya':'quark';return {name:(kind==='pan123'?'123资料':kind==='baidu'?'百度资料':kind==='guangya'?'光鸭资料':'项目资料')+'.zip',url:kind==='pan123'?'https://www.123pan.com/s/preview':kind==='baidu'?'https://pan.baidu.com/s/preview':kind==='guangya'?'https://www.guangyapan.com/s/preview':'https://pan.quark.cn/s/preview',password:'A1b2',expiry:'有效期以网盘显示为准',message:'模拟读取原分享，没有云端创建或修改'}}
const previewRevokedKinds=new Set<string>();const previewRevokes:any[]=[];
export async function ManagedCloudShareRevokes(accountId:string){return previewRevokes.filter(r=>r.accountId===accountId).map(({accountId,...r})=>r)}
export async function RevokeManagedCloudShare(handle:string){await delay(200);const kind=handle.includes('pan123')?'pan123':handle.includes('baidu')?'baidu':handle.includes('guangya')?'guangya':'quark';const accountId=accounts.find(a=>a.kind===kind)?.id;const result={id:'preview-revoke-'+kind,name:(kind==='pan123'?'123资料':kind==='baidu'?'百度资料':kind==='guangya'?'光鸭资料':'项目资料')+'.zip',state:'confirmed',message:'模拟原分享已撤销，网盘文件保留；没有真实云端操作',created:new Date().toISOString(),canCheck:false};previewRevokes.push({...result,accountId});previewRevokedKinds.add(kind);return result}
export async function CheckManagedCloudShareRevoke(id:string){return {...previewRevokes.find(r=>r.id===id)}}
export async function ClearManagedCloudShareRevokes(accountId:string){const remaining=previewRevokes.filter(r=>r.accountId!==accountId),n=previewRevokes.length-remaining.length;previewRevokes.splice(0,previewRevokes.length,...remaining);return n}

export async function ManagedCloudSharesNext(accountId:string,token:string){return {items:[],page:2,hasMore:false,next:''}}

export const ShareBatchCloudEntry=ShareCloudEntry

export async function UpdateManagedCloudShare(handle:string,password:string,days:number){await delay(300);return {id:"preview-settings",state:"ready",kind:"guangya",name:"模拟原分享",url:"https://www.guangyapan.com/s/preview-demo",password,days,message:"模拟设置结果；没有真实云端操作",canCheck:true}}

export async function ListCloudRecycle(accountId:string,page:number){await delay(180);const kind=accounts.find(a=>a.id===accountId)?.kind;if(new URLSearchParams(location.search).get('recycleError')===kind)throw Error('模拟登录已失效，请重新登录后刷新 [RECYCLE/preview]');return {items:page===1?[{id:'preview-folder',name:'旧项目资料',kind:'folder',size:null,originalPath:kind==='baidu'?'/工作资料/旧项目资料':['pan123','quark'].includes(kind??'')?'原目录 ID 77 / 旧项目资料':'',restoreHandle:kind==='baidu'?'preview-restore-folder':kind==='pan123'?'preview123-restore-folder':kind==='quark'?'previewquark-restore-folder':''},{id:'preview-archive',name:'项目素材与交接备份.zip',kind:'file',size:48234496,originalPath:kind==='baidu'?'/工作资料/项目素材与交接备份.zip':['pan123','quark'].includes(kind??'')?'原目录 ID 77 / 项目素材与交接备份.zip':'',restoreHandle:kind==='baidu'?'preview-restore-archive':kind==='pan123'?'preview123-restore-archive':kind==='quark'?'previewquark-restore-archive':''},{id:'preview-long',name:'较长文件名示例：2026 年度归档资料与多网盘备份说明文档.pdf',kind:'file',size:2818048,originalPath:kind==='baidu'?'/工作资料/归档/年度归档资料.pdf':''},{id:'preview-zero',name:'空白笔记.txt',kind:'file',size:0,originalPath:''}]:[],page,hasMore:page===1}}

let previewRecycleRestores:any[]=[];
export async function CloudRecycleRestores(accountId:string){return previewRecycleRestores.filter(r=>r.accountId===accountId)}
export async function RestoreCloudRecycle(handle:string){await delay(280);const r={id:'preview-restore-result',entryId:handle.endsWith('folder')?'preview-folder':'preview-archive',name:handle.endsWith('folder')?'旧项目资料':'项目素材与交接备份.zip',accountId:handle.startsWith('preview123')?'preview-2':handle.startsWith('previewquark')?'preview-1':'preview-3',state:'unknown',message:'模拟结果待确认；只检查原结果，没有实际云端操作',canCheck:true,created:new Date().toISOString()};previewRecycleRestores=[r];return r}
export async function CheckCloudRecycleRestore(id:string){await delay(200);const r={...previewRecycleRestores[0],state:'confirmed',canCheck:false,message:'模拟原位置已核对，没有发送新的还原请求'};previewRecycleRestores=[r];return r}
export async function ClearCloudRecycleRestores(accountId:string){const n=previewRecycleRestores.filter(r=>r.accountId===accountId&&r.state!=='unknown').length;previewRecycleRestores=previewRecycleRestores.filter(r=>r.accountId!==accountId||r.state==='unknown');return n}

export async function ValidateCloudMoveBatch(entries:any[]){await delay(180);if(entries.length>50)throw Error("最多50项")}

export async function CopyCloudEntry(entry:any){await delay(240);copyHistoryPreview.push({id:'copy-'+copyHistoryPreview.length,accountId:entry.input.accountId,name:entry.input.name,destination:entry.input.destinationId,state:'unknown',message:'模拟复制结果待核对；没有实际云端操作',canCheck:true,created:new Date().toISOString()});throw Error('模拟结果待核对，请检查本机复制记录')}

let copyHistoryPreview:any[]=[];
export async function CloudCopyHistory(id:string){await delay(140);return copyHistoryPreview.filter(r=>r.accountId===id)}
export async function CheckCloudCopy(id:string){await delay(200);const r=copyHistoryPreview.find(r=>r.id===id);if(!r)throw Error('模拟记录已不存在');Object.assign(r,{state:'confirmed',canCheck:false,message:'模拟源和目标完整元数据清单一致，没有发送新的复制请求'});return r}
export async function ClearCloudCopyHistory(id:string){const old=copyHistoryPreview.length;copyHistoryPreview=copyHistoryPreview.filter(r=>r.accountId!==id||r.state==='unknown');return old-copyHistoryPreview.length}

let searchPreview:any=null,searchPreviewPolls=0;
export async function StartCloudSearch(input:any){await delay(130);searchPreviewPolls=0;const matches=input.accountIds.flatMap((id:string)=>[{accountId:id,entry:{id:'work-note',name:'交接资料.txt',folder:false,size:2048},parentId:'work',path:'根目录 / 工作资料 / 交接资料.txt',trail:[{id:roots(id),name:'根目录'},{id:'work',name:'工作资料'}]},{accountId:id,entry:{id:'work',name:'工作资料',folder:true,size:null},parentId:roots(id),path:'根目录 / 工作资料',trail:[{id:roots(id),name:'根目录'}]}]).filter((r:any)=>(input.includePath?r.path:r.entry.name).toLowerCase().includes(input.query.toLowerCase()));const chosen=(input.firstMatchPerAccount||input.accountIds.length>1)?matches.filter((r:any,i:number,all:any[])=>all.findIndex(x=>x.accountId===r.accountId)===i):matches;searchPreview={id:'preview-search',state:'running',message:'正在逐目录扫描（隔离演示）',scanned:0,directories:0,current:'根目录',items:[],issues:[],matches:chosen};return {...searchPreview,matches:undefined}}
export async function CloudSearchStatus(id:string){await delay(100);searchPreviewPolls++;if(searchPreview.state==='running'){searchPreview.scanned=searchPreviewPolls*25;searchPreview.directories=searchPreviewPolls*3;searchPreview.items=searchPreview.matches;if(searchPreviewPolls>=2){searchPreview.state='completed';searchPreview.message='演示扫描结束，没有访问真实网盘'}}return {...searchPreview,matches:undefined}}
export async function CancelCloudSearch(id:string){if(searchPreview?.id===id){searchPreview.state='cancelled';searchPreview.message='演示搜索已停止，结果为已扫描部分'}}

let trashPreview:any[]=[];
export async function PrepareCloudTrash(inputs:any[]){await delay(180);return inputs.map(r=>({name:r.entry.name,folder:r.entry.folder,handle:JSON.stringify(r)}))}
export async function TrashCloudEntry(handle:string){await delay(500);const row=JSON.parse(handle),r={id:'trash-'+trashPreview.length,accountId:row.accountId,name:row.entry.name,original:row.parentId+' / '+row.entry.name,state:'unknown',message:'隔离演示：结果待核对，没有访问或修改真实网盘',created:new Date().toISOString(),canCheck:true};trashPreview.push(r);return r}
export async function CloudTrashHistory(id:string){await delay(100);return trashPreview.filter(r=>r.accountId===id)}
export async function CheckCloudTrash(id:string){await delay(200);const r=trashPreview.find(r=>r.id===id);Object.assign(r,{state:'confirmed',canCheck:false,message:'隔离演示：原ID在回收站核对通过，没有重新提交'});return r}
export async function ClearCloudTrashHistory(id:string){const old=trashPreview.length;trashPreview=trashPreview.filter(r=>r.accountId!==id||r.state==='unknown');return old-trashPreview.length}

export async function ValidateCloudRecycleRestoreBatch(handles:string[]){await delay(220);if(!handles.length||handles.length>50)throw Error('请选择1至50项')}

let purgePreview:any[]=[];
export async function PrepareCloudRecyclePurge(accountId:string,page:number,entries:any[]){await delay(200);return entries.map(r=>({name:r.name,handle:JSON.stringify({accountId,entry:r})}))}
export async function PurgeCloudRecycle(handle:string,confirmation:string){if(confirmation!=='永久删除')throw Error('缺少确认');await delay(400);const s=JSON.parse(handle),r={id:'purge-'+purgePreview.length,accountId:s.accountId,name:s.entry.name,original:s.entry.originalPath,state:'unknown',message:'隔离演示：受理回执未确认，不以记录消失当作删除成功；没有实际云端操作',created:new Date().toISOString(),canCheck:true};purgePreview.push(r);return r}
export async function CloudRecyclePurgeHistory(id:string){await delay(100);return purgePreview.filter(r=>r.accountId===id)}
export async function CheckCloudRecyclePurge(id:string){await delay(150);const r=purgePreview.find(r=>r.id===id);r.message='隔离演示：缺受理回执仍待核对，没有再次提交';return r}
export async function ClearCloudRecyclePurgeHistory(id:string){const old=purgePreview.length;purgePreview=purgePreview.filter(r=>r.accountId!==id||!['confirmed','not_sent'].includes(r.state));return old-purgePreview.length}
