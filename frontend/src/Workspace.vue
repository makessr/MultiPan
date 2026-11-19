<script setup lang="ts">
import {trapDialogFocus} from './dialogFocus'
import {ref,computed,watch,onMounted,onUnmounted,nextTick} from 'vue'
import {filterUploadTasks,uploadCounts,uploadTaskReport,uploadSourceKey,uploadGroups,uploadGroupKey,type UploadFilter,type UploadSort} from './uploadTasks'
import {downloadPage} from './downloadView'
import {transferRecovery} from './transferRecovery'
import {APP_VERSION} from './version'
import {Events} from '@wailsio/runtime'
import TransfersPanel from './TransfersPanel.vue'
import ApplicationUpdates from './ApplicationUpdates.vue'
import ShareHistory from './ShareHistory.vue'
import GlobalSearch from './GlobalSearch.vue'
import RelayPanel from './RelayPanel.vue'
import AccountHealth from './AccountHealth.vue'
import PendingReview from './PendingReview.vue'
import SessionRenewal from './SessionRenewal.vue'
import UploadReview from './UploadReview.vue'
import {useUploadReview} from './useUploadReview'
import {matchUploadFolder,type UploadFolderMatch} from './uploadFolderMatch'
import FolderPicker from './FolderPicker.vue'
import CloudFiles from './CloudFiles.vue'
import AppIcon from './AppIcon.vue'
import * as api from '../bindings/multipan/desktopservice'
import type {Account,AccountInput,File as SourceFile,Task,DownloadJob,TransferSettings,CloudSearchResult,CloudFolder,RelayInput} from '../bindings/multipan/internal/transfer/models'
const durableDownloads=ref<DownloadJob[]>([]),settings=ref<TransferSettings>({uploadConcurrency:3,downloadConcurrency:1,uploadKiB:0,downloadKiB:0,closeToTray:false})
const accounts=ref<Account[]>([]),tasks=ref<Task[]>([]),files=ref<SourceFile[]>([]),selected=ref<string[]>([])
const folders=ref<Record<string,string>>({}),error=ref(''),notice=ref(''),storageError=ref(''),tab=ref('files'),filter=ref<UploadFilter>('all'),busy=ref(false),modal=ref(false),saving=ref(false)
function blank():AccountInput{return {id:'',name:'',kind:'lanzou',endpoint:'',username:'',folder:'-1',folderDisplay:'根目录',userAgent:'',secret:''}}
const accountNameInput=ref<HTMLInputElement|null>(null)
const accountRefresh=ref<Record<string,number>>({})
const browserAccountId=ref(''),cloudMoving=ref(false),downloadActive=ref(false),downloadItems=ref(0)
const relayFileCopy=ref<{inputs:RelayInput[];directory:boolean}|null>(null)
function openRelayFileCopy(inputs:RelayInput[],directory:boolean){
 relayFileCopy.value={inputs,directory}
 tab.value='relay'
}
function downloadStatus(active:boolean,count:number){downloadActive.value=active;downloadItems.value=count}
function accountLabel(a:Account){const same=accounts.value.filter(other=>other.kind===a.kind);return cloudName(a.kind)+(same.length>1?' '+(same.findIndex(other=>other.id===a.id)+1):'')}
const fileLocation=ref<{accountId:string;id:string;breadcrumbs?:CloudFolder[];nonce:number}|null>(null)
let locationNonce=0
function openFileLocation(accountId:string,parentId:string,breadcrumbs?:CloudFolder[]){
 if(cloudMoving.value||!parentId||!accounts.value.some(a=>a.id===accountId))return
 browserAccountId.value=accountId
 fileLocation.value={accountId,id:parentId,breadcrumbs,nonce:++locationNonce}
 tab.value='files'
}
function openSearchLocation(row:CloudSearchResult){openFileLocation(row.accountId,row.parentId,row.trail??[])}
function openFiles(a?:Account){if(cloudMoving.value)return;if(a)browserAccountId.value=a.id;tab.value='files'}
async function browserRenamed(_id:string){await refresh()}
async function browserSaved(_id:string,_folder:string,_path:string){await refresh()}

const localFileCount=computed(()=>files.value.filter(f=>!f.directory).length),localDirectoryCount=computed(()=>files.value.filter(f=>f.directory).length)
const folderAccount=ref<Account|null>(null),folderPurpose=ref<'upload'|'default'>('upload'),folderPaths=ref<Record<string,string>>({})
const uploadFolderTrails=ref<Record<string,CloudFolder[]>>({})
const folderMatches=ref<Record<string,UploadFolderMatch>>({}),folderMatchStatus=ref('')
const folderMatchLabels={searching:'正在查找目录…',missing:'未找到相近目录',ambiguous:'有多个相近路径，请手动选择',error:'目录读取失败，可手动选择'}
let folderMatchRun=0
function clearFolderMatches(){folderMatchRun++;folderMatches.value={};folderMatchStatus.value=''}
function isCloud(kind:string){return ['lanzou','quark','pan123','baidu','ctfile','ctdav','guangya'].includes(kind)}
function cloudName(kind:string){return ({lanzou:'蓝奏云',quark:'夸克网盘',pan123:'123 云盘',baidu:'百度网盘',guangya:'光鸭云盘',ctfile:'诚通 OpenAPI',ctdav:'诚通网盘',webdav:'WebDAV'} as Record<string,string>)[kind]??kind}
function cloudMark(kind:string){return kind==='guangya'?'光':['ctfile','ctdav'].includes(kind)?'诚':kind==='baidu'?'百':kind==='pan123'?'123':kind==='quark'?'夸':kind==='lanzou'?'蓝':'W'}
function rootFolder(kind:string){return kind==='guangya'?'root':kind==='ctfile'?'d0':['quark','pan123'].includes(kind)?'0':kind==='lanzou'?'-1':'/'}
function folderName(a:Account){return folderPaths.value[a.id]||a.folderDisplay||(a.folder===rootFolder(a.kind)?'根目录':`目录 ${a.folder}`)}
function browse(a:Account,purpose:'upload'|'default'='upload'){folderPurpose.value=purpose;folderAccount.value=a}
function validUploadTrail(a:Account,id:string,trail:CloudFolder[]){
 return trail[0]?.id===rootFolder(a.kind)&&trail.at(-1)?.id===id&&
  new Set(trail.map(folder=>folder.id)).size===trail.length&&
  trail.slice(1).every(folder=>folder.name&&folder.name!=='目录 '+folder.id&&!folder.name.includes(' / '))
}
async function folderChosen(id:string,path:string,trail:CloudFolder[]=[]){
 const a=folderAccount.value
 if(!a)return
 folderAccount.value=null
 if(folderPurpose.value==='default'){
  form.value.folder=id;form.value.folderDisplay=path
  return
 }
 folders.value[a.id]=id;folderPaths.value[a.id]=path
 if(validUploadTrail(a,id,trail))uploadFolderTrails.value[a.id]=trail
 else delete uploadFolderTrails.value[a.id]
 await suggestUploadFolders(a,id,trail)
}

async function suggestUploadFolders(source:Account,id:string,trail:CloudFolder[]){
 clearFolderMatches()
 const targets=accounts.value.filter(a=>a.id!==source.id&&(isCloud(a.kind)||a.kind==='webdav'))
 if(!targets.length||id===rootFolder(source.kind))return
 if(!validUploadTrail(source,id,trail)){
  folderMatchStatus.value='请从根目录逐级选择，以查找其他网盘的相近目录。'
  return
 }
 const run=folderMatchRun
 const current=()=>run===folderMatchRun
 const names=trail.slice(1).map(folder=>folder.name)
 const pending=targets.map(account=>({account,folder:folders.value[account.id]})).values()
 for(const target of targets)folderMatches.value[target.id]={state:'searching'}
 async function findNext(){
  for(const {account,folder} of pending){
   if(!current())return
   let result:UploadFolderMatch
   try{
    result=await matchUploadFolder(rootFolder(account.kind),names,parent=>api.ListCloudFolders(account.id,parent),current)
   }catch{
    result={state:'error'}
   }
   if(!current())return
   if(folder===folders.value[account.id])folderMatches.value[account.id]=result
   else delete folderMatches.value[account.id]
  }
 }
 await Promise.all(Array.from({length:Math.min(3,targets.length)},findNext))
}

function folderMatchChoices(accountId:string){
 const match=folderMatches.value[accountId]
 return match?.state==='found'?match.choices:[]
}
function useFolderMatch(accountId:string,id:string){
 const match=folderMatches.value[accountId]
 if(busy.value||tab.value!=='upload'||match?.state!=='found'||!accounts.value.some(a=>a.id===accountId))return
 const choice=match.choices.find(folder=>folder.id===id)
 if(!choice)return
 folders.value[accountId]=choice.id
 folderPaths.value[accountId]=choice.path
 uploadFolderTrails.value[accountId]=choice.trail
 delete folderMatches.value[accountId]
}
watch(tab,()=>{if(tab.value!=='upload')clearFolderMatches()},{flush:'sync'})
watch(()=>JSON.stringify([accountRefresh.value,accounts.value.map(a=>[a.id,a.kind,a.endpoint,a.username,a.folder])]),()=>{
 clearFolderMatches()
 uploadFolderTrails.value={}
},{flush:'sync'})

const loginMode=ref(true); const form=ref<AccountInput>(blank()),total=computed(()=>files.value.reduce((s,f)=>s+f.size,0)),active=computed(()=>tasks.value.filter(t=>['running','queued'].includes(t.status)).length)
const taskQuery=ref(''),taskKind=ref(''),taskSort=ref<UploadSort>('newest'),taskPage=ref(1),taskPageSize=ref(50)
const taskFilters:{id:UploadFilter;label:string}[]=[{id:'all',label:'全部'},{id:'active',label:'进行中'},{id:'attention',label:'需处理'},{id:'done',label:'已完成'}]
const taskCounts=computed(()=>uploadCounts(tasks.value))
const taskKinds=computed(()=>[...new Set(tasks.value.map(t=>t.kind))].sort((a,b)=>cloudName(a).localeCompare(cloudName(b),'zh-CN')))
const taskGroup=ref(''),failedOnly=ref(false),groupPage=ref(1),taskFilterArea=ref<HTMLElement|null>(null)
const groups=computed(()=>uploadGroups(tasks.value))
const groupPages=computed(()=>Math.max(1,Math.ceil(groups.value.length/6)))
const shownGroups=computed(()=>groups.value.slice((groupPage.value-1)*6,groupPage.value*6))
watch(groupPages,n=>{if(groupPage.value>n)groupPage.value=n})
watch(()=>groups.value.map(g=>g.key),keys=>{if(taskGroup.value&&!keys.includes(taskGroup.value))taskGroup.value=''})
function viewGroup(key:string,failures=false){if(taskControlling.value)return;taskGroup.value=key;failedOnly.value=failures;filter.value='all';taskQuery.value='';taskKind.value='';taskPage.value=1;void nextTick(()=>taskFilterArea.value?.scrollIntoView({behavior:'smooth',block:'start'}))}
const selectedGroup=computed(()=>groups.value.find(g=>g.key===taskGroup.value))
const visible=computed(()=>filterUploadTasks(tasks.value,filter.value,taskKind.value,taskQuery.value,taskSort.value).filter(t=>(!taskGroup.value||uploadGroupKey(t)===taskGroup.value)&&(!failedOnly.value||t.status==='failed')))
const visibleFailures=computed(()=>visible.value.filter(t=>t.status==='failed'))
const taskPageView=computed(()=>downloadPage(visible.value,taskPage.value,taskPageSize.value))
watch(()=>taskPageView.value.page,value=>{if(taskPage.value!==value)taskPage.value=value})
watch(()=>taskPageView.value.items.map(t=>t.id).join('\u0000'),()=>{const ids=new Set(taskPageView.value.items.map(t=>t.id));taskSelection.value=taskSelection.value.filter(id=>ids.has(id))})
function goTaskPage(value:number){if(!taskControlling.value){taskSelection.value=[];taskPage.value=downloadPage(visible.value,value,taskPageSize.value).page}}
function clearTaskFilters(){taskSelection.value=[];filter.value='all';taskKind.value='';taskQuery.value='';taskSort.value='newest';taskPage.value=1;taskGroup.value='';failedOnly.value=false}
const taskSelection=ref<string[]>([]),taskControlling=ref(false),taskBatchResults=ref<{name:string;message:string;failed:boolean;submitted?:boolean}[]>([])
const taskBatchStop=ref(false),taskBatchTotal=ref(0),taskBatchName=ref(''),taskBatchAction=ref('')
const taskBatchSubmitted=computed(()=>taskBatchResults.value.filter(r=>r.submitted).length)
const taskBatchFailed=computed(()=>taskBatchResults.value.filter(r=>r.failed).length)
const taskBatchSkipped=computed(()=>taskBatchResults.value.filter(r=>!r.failed&&!r.submitted).length)
function stopTaskBatch(){if(taskControlling.value)taskBatchStop.value=true}
function canTaskAction(t:Task,action:'stop'|'retry'){return (action==='stop'?['queued','running']:['failed','paused']).includes(t.status)}
const selectableTasks=computed(()=>taskPageView.value.items.filter(t=>canTaskAction(t,'stop')||canTaskAction(t,'retry')))
const chosenTasks=computed(()=>selectableTasks.value.filter(t=>taskSelection.value.includes(t.id)))
const chosenStops=computed(()=>chosenTasks.value.filter(t=>canTaskAction(t,'stop')).length)
const chosenRetries=computed(()=>chosenTasks.value.filter(t=>canTaskAction(t,'retry')).length)
function toggleTask(id:string,checked:boolean){if(taskControlling.value)return;if(!checked)taskSelection.value=taskSelection.value.filter(x=>x!==id);else if(taskSelection.value.length<200&&!taskSelection.value.includes(id))taskSelection.value.push(id)}
function selectVisibleTasks(checked:boolean){if(taskControlling.value)return;taskSelection.value=checked?selectableTasks.value.slice(0,200).map(t=>t.id):[]}
watch([filter,taskKind,taskQuery,taskSort,taskPageSize,taskGroup,failedOnly],()=>{taskPage.value=1;taskSelection.value=[]})
watch(taskPage,()=>taskSelection.value=[])
async function controlChosen(action:'stop'|'retry'){
 if(taskControlling.value)return
 const targets=chosenTasks.value.filter(t=>canTaskAction(t,action)).map(t=>({id:t.id,name:t.file.name}))
 if(!targets.length)return
 taskControlling.value=true;taskBatchResults.value=[];taskBatchStop.value=false;taskBatchTotal.value=targets.length;taskBatchAction.value=action==='stop'?'批量停止':'批量重新上传';taskBatchName.value=''
 try{for(const target of targets){if(taskBatchStop.value){taskBatchResults.value.push({name:target.name,message:'已停止后续处理，未提交请求',failed:false});continue}taskBatchName.value=target.name;const current=tasks.value.find(t=>t.id===target.id);if(!current||!canTaskAction(current,action)){taskBatchResults.value.push({name:target.name,message:'状态已变化，已跳过',failed:false});continue}try{if(action==='stop')await api.Stop(target.id);else await api.Retry(target.id);taskBatchResults.value.push({name:target.name,message:action==='stop'?'停止请求已提交':'重新上传请求已提交',failed:false,submitted:true})}catch(e){taskBatchResults.value.push({name:target.name,message:msg(e),failed:true})}}await refresh()}finally{taskSelection.value=[];taskControlling.value=false;taskBatchName.value=''}
}
async function retryFailedTasks(rows:Task[]=visible.value){
 if(taskControlling.value)return
 const ids=rows.filter(t=>t.status==='failed').slice(0,1000).map(t=>t.id)
 if(!ids.length)return
 taskControlling.value=true;taskBatchTotal.value=ids.length;taskBatchAction.value='重试失败项';taskBatchResults.value=[];taskBatchName.value='提交失败任务清单'
 try{const results=await api.RetryFailedUploads(ids);taskBatchResults.value=(results??[]).map(r=>({name:r.name,message:r.message,failed:false,submitted:r.applied}));await refresh()}
 catch(e){error.value=msg(e);taskBatchResults.value=ids.map(id=>({name:rows.find(t=>t.id===id)?.file.name??id,message:'本次未确认提交，请刷新任务状态后核对',failed:true}))}
 finally{taskControlling.value=false;taskBatchName.value='';taskSelection.value=[]}
}
async function copyFailures(){if(copyingTask.value||!visibleFailures.value.length)return;const report=visibleFailures.value.slice(0,1000).map(t=>uploadTaskReport(t,APP_VERSION,cloudName(t.kind),labels[t.status]??t.status)).join('\n\n');copyingTask.value='failures';try{await api.CopyDownloadReport(report);taskCopyStatus.value='当前筛选的失败结果已复制（最多1000项）；分享前请检查文件名、目录和错误说明。'}catch(e){taskCopyStatus.value=msg(e)}finally{copyingTask.value=''}}
const labels:Record<string,string>={queued:'等待中',running:'上传中',success:'已完成',failed:'失败',paused:'已停止'}
function bytes(n:number){if(n<1024)return `${n} B`;const units=['KB','MB','GB','TB'];let v=n/1024,i=0;while(v>=1024&&i<3){v/=1024;i++}return `${v.toFixed(v<10?1:0)} ${units[i]}`}
function percent(t:Task){return t.status==='success'?100:t.file.size?Math.min(99,Math.round(t.sent/t.file.size*100)):0}
function msg(e:unknown){return e instanceof Error?e.message:String(e)}
let polling=false,timer:ReturnType<typeof setInterval>
async function refresh(){if(polling)return;polling=true;try{const previous=accounts.value,s=await api.Snapshot();for(const a of s.accounts??[]){const old=previous.find(item=>item.id===a.id);if(folders.value[a.id]===undefined||old&&folders.value[a.id]===old.folder&&folderPaths.value[a.id]===(old.folderDisplay||'根目录')){folders.value[a.id]=a.folder;folderPaths.value[a.id]=a.folderDisplay||'根目录'}}accounts.value=s.accounts??[];tasks.value=s.tasks??[];durableDownloads.value=s.downloads??[];if(s.settings)settings.value=s.settings;storageError.value=s.storageError}catch(e){error.value='读取本机任务失败：'+msg(e)}finally{polling=false}}

function localFileKey(path:string){return path.replaceAll('/','\\').toLowerCase()}
function filePlanKey(f:SourceFile){return uploadSourceKey(f)}
function appendFiles(incoming:SourceFile[]){const known=new Set(files.value.map(filePlanKey));let added=0,duplicates=0;for(const f of incoming){const key=filePlanKey(f);if(known.has(key)){duplicates++;continue};files.value.push(f);known.add(key);added++};return {added,duplicates}}
async function pickFolder(){error.value='';try{appendFiles(await api.PickUploadFolder()??[])}catch(e){error.value=msg(e)}}
async function pick(){error.value='';try{appendFiles(await api.PickFiles()??[])}catch(e){error.value=msg(e)}}
type DroppedFiles={targetId:string;files:SourceFile[];directories:number;unreadable:number;duplicates:number;messages?:string[]}
function acceptDrop(drop:DroppedFiles){if(drop?.targetId!=='upload-dropzone'||tab.value!=='upload'||modal.value||folderAccount.value||busy.value||!Array.isArray(drop?.files))return;error.value='';notice.value='';const {added,duplicates}=appendFiles(drop.files);const repeated=duplicates+(drop.duplicates??0);if(added)notice.value=`已加入 ${added} 个本地项目${repeated?`，${repeated} 个重复项目已忽略`:''}`;else if(repeated)notice.value='文件已在上传列表中，无需重复添加';const skipped:string[]=[];if(drop.directories)skipped.push(`${drop.directories} 个未能扫描的文件夹`);if(drop.unreadable)skipped.push(`${drop.unreadable} 个无法访问的项目`);if(drop.messages?.length)skipped.push(...drop.messages);if(skipped.length)error.value='已忽略：'+skipped.join('、')}
const ctAuth=ref({mode:'email',email:'',password:'',phone:'',code:''}),ctChallenge=ref({timestamp:0,checksum:''}),ctSmsBusy=ref(false),ctSmsCooldown=ref(false)
let ctSmsTimer:ReturnType<typeof setTimeout>|undefined
async function openCtWebDAV(){try{await api.OpenCtWebDAV()}catch(e){error.value=msg(e)}}
async function openCtAuthorization(){try{await api.OpenCtAuthorization()}catch(e){error.value=msg(e)}}
async function sendCtSMS(){ctSmsBusy.value=true;error.value='';const phone=ctAuth.value.phone;try{const challenge=await api.RequestCtSMS(phone);ctSmsCooldown.value=true;ctSmsTimer=setTimeout(()=>ctSmsCooldown.value=false,60000);if(modal.value&&ctAuth.value.phone===phone){ctChallenge.value=challenge;notice.value='验证码已发送，请查看短信'}}catch(e){error.value=msg(e)}finally{ctSmsBusy.value=false}}
function resetCtLogin(){ctAuth.value={mode:'email',email:'',password:'',phone:'',code:''};ctChallenge.value={timestamp:0,checksum:''}}
function openAccount(a?:Account){if(cloudMoving.value)return;form.value=a?{...a,secret:''}:blank();resetCtLogin();loginMode.value=!a&&!['ctfile','ctdav'].includes(form.value.kind);modal.value=true;error.value='';void nextTick(()=>accountNameInput.value?.focus())}
function changeKind(){form.value.folder=rootFolder(form.value.kind);form.value.folderDisplay='根目录';form.value.endpoint='';form.value.secret='';form.value.username=form.value.kind==='ctfile'?'public':'';resetCtLogin();loginMode.value=!['ctfile','ctdav'].includes(form.value.kind)}
function closeModal(){modal.value=false;form.value.secret='';resetCtLogin()}
async function saveAccount(){
 saving.value=true;error.value=''
 try{
  let a:Account
  if(['ctfile','ctdav'].includes(form.value.kind)){a=await api.SaveAccount({...form.value})}
  else if(isCloud(form.value.kind)&&loginMode.value){a=await ({quark:api.LoginQuark,pan123:api.Login123,baidu:api.LoginBaidu,guangya:api.LoginGuangya,lanzou:api.LoginLanzou}[form.value.kind as 'quark'|'pan123'|'baidu'|'guangya'|'lanzou'])(form.value.id,form.value.name,form.value.folder)}
  else {a=await api.SaveAccount({...form.value})}
  folders.value[a.id]=a.folder;folderPaths.value[a.id]=a.folderDisplay||'根目录';closeModal();await refresh();accountRefresh.value[a.id]=(accountRefresh.value[a.id]??0)+1;notice.value='账号已保存到本机'
 }catch(e){error.value=msg(e)}finally{saving.value=false;ctAuth.value.password='';ctAuth.value.code=''}
}
async function removeAccount(){if(!window.confirm(`移除账号「${form.value.name}」？远端文件不会删除。`))return;try{await api.DeleteAccount(form.value.id);selected.value=selected.value.filter(x=>x!==form.value.id);closeModal();await refresh()}catch(e){error.value=msg(e)}}
const uploadFlow=useUploadReview(async(count,skipped)=>{notice.value=`已创建 ${count} 个上传任务${skipped?'，跳过 '+skipped+' 项（未创建任务）':''}`;files.value=[];await refresh();tab.value='tasks';clearTaskFilters()},active=>busy.value=active)
async function start(){if(busy.value)return;error.value='';await uploadFlow.begin(files.value,selected.value.map(id=>({accountId:id,folder:folders.value[id]??''})))}
const openingUploadFolder=ref(''),uploadFolderStatus=ref('')
async function openTaskFolder(t:Task){if(openingUploadFolder.value)return;openingUploadFolder.value=t.id;uploadFolderStatus.value='';try{await api.OpenUploadFolder(t.id);uploadFolderStatus.value='已请求打开「'+t.file.name+'」所在的本地文件夹'}catch(e){uploadFolderStatus.value=msg(e)}finally{openingUploadFolder.value=''}}
const copyingTask=ref(''),taskCopyStatus=ref('')
async function copyTaskResult(t:Task){if(copyingTask.value)return;copyingTask.value=t.id;taskCopyStatus.value='';try{await api.CopyDownloadReport(uploadTaskReport(t,APP_VERSION,cloudName(t.kind),labels[t.status]??t.status));taskCopyStatus.value='已复制「'+t.file.name+'」的任务结果；发送前请检查文件名、目录和错误说明。'}catch(e){taskCopyStatus.value=msg(e)}finally{copyingTask.value=''}}
async function control(t:Task){if(taskControlling.value)return;try{if(['queued','running'].includes(t.status))await api.Stop(t.id);else await api.Retry(t.id);await refresh()}catch(e){error.value=msg(e)}}
function editAccount(id:string,relogin=false){if(cloudMoving.value)return;const a=accounts.value.find(a=>a.id===id);if(!a){error.value='账号不存在，请添加账号后重新创建任务';return}openAccount(a);loginMode.value=relogin&&['lanzou','quark','pan123','baidu','guangya'].includes(a.kind)}
function recoverAccount(id:string){editAccount(id,true)}
const pendingReceipt=ref<{tab:string;kind:string;accountId:string;receiptId:string}|null>(null)
function openPendingModule(kind:string,accountId:string,receiptId:string){
 if(cloudMoving.value)return
 const target=({session_renewal:'renewal',share:'shares',share_settings:'shares',share_revoke:'shares',copy:'files',trash:'files',relay:'relay'} as Record<string,string>)[kind]
 if(!target)return
 pendingReceipt.value=target==='shares'?{tab:target,kind,accountId,receiptId}:null
 if(accounts.value.some(a=>a.id===accountId))browserAccountId.value=accountId
 tab.value=target
}
watch(tab,next=>{if(pendingReceipt.value?.tab!==next)pendingReceipt.value=null},{flush:'sync'})
function uploadRecovery(t:Task){return transferRecovery('upload',t.status,t.message)}
let offDrop:(()=>void)|undefined
onMounted(async()=>{offDrop=Events.On('multipan:files-dropped',event=>acceptDrop(event.data as DroppedFiles));await refresh();timer=setInterval(refresh,800)});onUnmounted(()=>{clearFolderMatches();offDrop?.();clearInterval(timer);if(ctSmsTimer)clearTimeout(ctSmsTimer)})
const primaryNavigation = [
 {id:'files',label:'文件管理'},
 {id:'search',label:'目录搜索'},
 {id:'shares',label:'分享记录'},
] as const
const navigationGroups = [
 {id:'transfers',label:'传输',items:[{id:'tasks',label:'上传任务'},{id:'downloads',label:'下载结果'},{id:'queue',label:'下载队列与历史'},{id:'relay',label:'网盘中转'}]},
 {id:'maintenance',label:'设置与维护',items:[{id:'settings',label:'传输设置'},{id:'review',label:'待核对中心'},{id:'health',label:'账号连接检查'},{id:'renewal',label:'123 登录维护'}]},
] as const
type NavigationGroup = typeof navigationGroups[number]['id']
const currentGroup = computed(() => navigationGroups.find(group => group.items.some(item => item.id === tab.value))?.id)
const expandedGroup = ref<NavigationGroup>()
watch(tab, () => { expandedGroup.value = currentGroup.value }, {flush:'sync'})
function toggleNavigationGroup(group:NavigationGroup){
 expandedGroup.value = expandedGroup.value === group ? undefined : group
}
function navigationDisabled(page:string){
 return cloudMoving.value && !(downloadActive.value && (page === 'files' || page === 'downloads'))
}
</script>
<template>
<div class="shell desktop-shell">
 <aside class="desktop-sidebar">
 <div class="brand"><img class="brand-mark" src="/icon.png" alt=""/>MultiPan</div>
 <button type="button" class="primary sidebar-upload" :disabled="navigationDisabled('upload')"
  :aria-current="tab==='upload'?'page':undefined" @click="tab='upload'">
  <AppIcon name="upload"/>多网盘上传
 </button>
 <div class="account-heading">我的网盘<button aria-label="添加网盘" title="添加网盘" :disabled="cloudMoving" @click="openAccount()"><AppIcon name="plus"/></button></div>
 <div class="sidebar-accounts"><div v-for="a in accounts" :key="a.id" class="account-item" :class="{active:tab==='files'&&(browserAccountId===a.id||!browserAccountId&&accounts[0]?.id===a.id)}"><button class="account-open" :disabled="cloudMoving" :title="accountLabel(a)" @click="openFiles(a)"><span class="disk-icon" :class="a.kind">{{cloudMark(a.kind)}}</span><strong>{{accountLabel(a)}}</strong></button><button class="account-settings" :disabled="cloudMoving" :aria-label="'编辑'+accountLabel(a)" title="编辑网盘账号与登录信息" @click="openAccount(a)"><AppIcon name="edit"/></button></div></div>
 <nav aria-label="工作区" class="workspace-nav">
  <button v-for="item in primaryNavigation" :key="item.id" :disabled="navigationDisabled(item.id)"
   :aria-current="tab===item.id?'page':undefined" @click="tab=item.id">{{item.label}}</button>
  <div v-for="group in navigationGroups" :key="group.id">
   <button type="button" class="workspace-group" :class="{'contains-current':currentGroup===group.id}"
    :aria-expanded="expandedGroup===group.id" :aria-controls="'workspace-nav-'+group.id" @click="toggleNavigationGroup(group.id)">
    {{group.label}}
    <b v-if="group.id==='transfers'&&active&&expandedGroup!==group.id" :aria-label="active+' 个上传任务进行中'">{{active}}</b>
    <AppIcon name="forward"/>
   </button>
   <div v-show="expandedGroup===group.id" :id="'workspace-nav-'+group.id" class="workspace-subnav">
    <button v-for="item in group.items" :key="item.id" :disabled="navigationDisabled(item.id)"
     :aria-current="tab===item.id?'page':undefined" @click="tab=item.id">
     {{item.label}}
     <b v-if="item.id==='tasks'&&active">{{active}}</b>
     <b v-if="item.id==='downloads'&&downloadItems">{{downloadItems}}</b>
    </button>
   </div>
  </div>
 </nav>
 </aside>
 <main id="main-content" class="desktop-main"><div class="page">
 <div v-if="error||storageError" class="banner error" role="alert"><span>{{error||storageError}}</span><button v-if="error" aria-label="关闭错误提示" @click="error=''">×</button></div>
 <div v-if="notice" class="banner notice" role="status"><span>{{notice}}</span><button aria-label="关闭提示" @click="notice=''">×</button></div>
 <div v-show="tab==='files'||tab==='downloads'" class="files-surface"><CloudFiles :location-request="fileLocation" :accounts="accounts" :account-id="browserAccountId" :view="tab==='downloads'?'downloads':'files'" :refresh-key="accountRefresh[browserAccountId||accounts[0]?.id||'']??0" @edit="editAccount" :blocked="modal||!!folderAccount||busy||!['files','downloads'].includes(tab)" @queued="refresh" @relay-copy="openRelayFileCopy" @mutation="cloudMoving=$event" @download-status="downloadStatus" @downloads="tab='downloads'" @uploads="tab='tasks'" @view-files="tab='files'" @account="browserAccountId=$event" @saved="browserSaved" @renamed="browserRenamed" @add="openAccount()"/></div><SessionRenewal v-if="tab==='renewal'" :accounts="accounts" :refresh-key="JSON.stringify(accountRefresh)" :blocked="modal||!!folderAccount||busy" @mutation="cloudMoving=$event" @account="editAccount($event,true)"/><PendingReview v-if="tab==='review'" :accounts="accounts" :refresh-key="JSON.stringify(accountRefresh)" :blocked="modal||!!folderAccount||busy" @mutation="cloudMoving=$event" @open="openPendingModule"/><AccountHealth v-if="tab==='health'" :accounts="accounts" :refresh-key="JSON.stringify(accountRefresh)" :blocked="cloudMoving||modal||!!folderAccount||busy" @account="editAccount($event,true)"/><RelayPanel v-if="tab==='relay'" :accounts="accounts" :file-copy="relayFileCopy" @copy-consumed="relayFileCopy=null" :blocked="modal||!!folderAccount||busy"/><GlobalSearch v-if="tab==='search'" :accounts="accounts" :initial-account-id="browserAccountId" :refresh-key="JSON.stringify(accountRefresh)" :blocked="cloudMoving||modal||!!folderAccount||busy" @open="openSearchLocation"/><ShareHistory :review-target="pendingReceipt" v-if="tab==='shares'" :accounts="accounts" :refresh-key="JSON.stringify(accountRefresh)" :blocked="modal||!!folderAccount||busy" @mutation="cloudMoving=$event" @files="tab='files'"/><TransfersPanel v-if="tab==='queue'||tab==='settings'" :view="tab" :jobs="durableDownloads" :settings="settings" @refresh="refresh" @account="recoverAccount"/><ApplicationUpdates v-show="tab==='settings'" :blocked="cloudMoving||modal||!!folderAccount||busy"/><p v-if="tab==='settings'" class="app-version">MultiPan {{APP_VERSION}}</p><template v-if="tab==='upload'">
 <div class="heading"><h1>多网盘上传</h1></div>
 <div class="upload-layout"><section id="upload-dropzone" class="source-panel" :data-file-drop-target="!modal&&!folderAccount&&!busy?'':undefined"><div class="section-title"><h2>本地项目 <em v-if="files.length">{{files.length}}</em></h2><div><button v-if="files.length" class="text-button" @click="files=[]">清空</button><button class="secondary" @click="pick">选择文件</button><button class="secondary" :disabled="busy" @click="pickFolder">选择文件夹</button></div></div>
 <button v-if="!files.length" class="file-picker" @click="pick"><AppIcon name="upload"/><strong>拖入文件或文件夹</strong></button>
 <div v-else class="file-list"><div v-for="f in files" :key="filePlanKey(f)" class="file-row"><span class="file-symbol">↥</span><div class="file-detail"><strong>{{f.relativePath||f.name}}{{f.directory?'（目录）':''}}</strong><small :title="f.path">{{f.path}}</small></div><span class="size">{{bytes(f.size)}}</span><button class="icon-button" :aria-label="'移除'+f.name" @click="files=files.filter(x=>filePlanKey(x)!==filePlanKey(f))">×</button></div></div>
 <div v-if="files.length" class="source-footer">{{localFileCount}} 个文件、{{localDirectoryCount}} 个目录 · {{bytes(total)}}</div></section>
 <section class="targets-panel"><div class="section-title"><h2>目标网盘</h2></div>
 <p v-if="folderMatchStatus" class="muted" role="status">{{folderMatchStatus}}</p>
 <div v-if="!accounts.length" class="target-empty"><span>尚未添加网盘</span><button class="text-button" @click="openAccount()">添加网盘</button></div>
 <div v-for="a in accounts" :key="a.id" class="target-row" :class="{chosen:selected.includes(a.id)}"><label class="target-select"><input type="checkbox" v-model="selected" :value="a.id"/><span class="disk-icon" :class="a.kind">{{cloudMark(a.kind)}}</span><div><strong>{{accountLabel(a)}}</strong></div></label><div v-if="isCloud(a.kind)" class="folder-field folder-target"><span>保存到</span><strong :title="folderName(a)">{{folderName(a)}}</strong><button class="secondary" @click="browse(a)">选择目录</button></div><div v-else class="folder-field"><span>保存目录</span><input v-model="folders[a.id]" :aria-label="a.name+'保存目录'" placeholder="/备份" @input="delete folderMatches[a.id];delete uploadFolderTrails[a.id]"/><button v-if="a.kind==='webdav'" type="button" class="secondary" @click="browse(a)">选择目录</button></div>
 <div v-if="folderMatches[a.id]" class="upload-folder-match" aria-live="polite">
  <template v-if="folderMatches[a.id].state==='found'">
   <span class="muted">候选目录</span>
   <button v-for="choice in folderMatchChoices(a.id)" :key="choice.id" type="button" class="secondary" :disabled="busy"
    :title="choice.path" :aria-label="accountLabel(a)+'使用 '+choice.path" @click="useFolderMatch(a.id,choice.id)">{{choice.path}}</button>
  </template>
  <span v-else class="muted">{{folderMatchLabels[folderMatches[a.id].state]}}</span>
 </div></div></section>
 </div><div class="upload-summary"><div><strong>{{files.length}} 个项目 <span>×</span> {{selected.length}} 个网盘</strong></div><button class="primary start" :disabled="!files.length||!selected.length||busy" @click="start">{{busy?'正在创建任务…':'开始上传'}}</button></div>
 <details class="operation-details"><summary>重名与上传限制</summary><p>WebDAV、夸克、百度拒绝覆盖同名项目。123 核验一致则完成。蓝奏云的重名、类型与大小限制以账号为准，当前不分割大文件或修改扩展名。</p></details>
 </template>
 <template v-else-if="tab==='tasks'"><div class="heading"><h1>上传任务</h1><button class="primary" @click="tab='upload'">新建上传</button></div>
 
 <div class="filters" role="group" aria-label="上传任务状态"><button v-for="item in taskFilters" :key="item.id" :class="{selected:filter===item.id}" :disabled="taskControlling" :aria-pressed="filter===item.id" @click="filter=item.id;failedOnly=false">{{item.label}} {{taskCounts[item.id]}}</button></div>
 <details v-if="groups.length" class="upload-groups"><summary>文件夹与上传批次进度 · {{groups.length}} 组</summary><p class="preview-note">按本次上传、目标网盘和根文件夹独立汇总；进度包含实际保留的任务，不含上传前跳过项。传输字节不等于上传成功。</p><div class="upload-group-grid"><article v-for="g in shownGroups" :key="g.key" class="upload-group-card"><div><strong>{{g.title}}</strong><span class="status">{{g.complete?'全部完成':g.failed?'有失败项':g.active?'进行中':'未完成'}}</span></div><small>{{cloudName(g.kind)}} / {{g.folder}}</small><small class="upload-group-created">{{g.tasks[0]?.created?new Date(g.tasks[0].created).toLocaleString():'旧版任务'}}</small><p>{{g.files}} 个文件 · {{g.directories}} 个目录 · {{g.percent}}%</p><progress :value="g.percent" max="100" :aria-label="g.title+'整体上传进度'"/><p>已完成 {{g.success}} · 失败 {{g.failed}} · 已暂停 {{g.paused}} · 等待/上传 {{g.active}}</p><small>{{bytes(g.sentBytes)}} / {{bytes(g.totalBytes)}}</small><div class="saved-location-actions"><button class="secondary small" :disabled="taskControlling" @click="viewGroup(g.key)">查看本组</button><button v-if="g.failed" class="secondary small" :disabled="taskControlling" @click="viewGroup(g.key,true)">查看本组失败项</button></div></article></div><div v-if="groupPages>1" class="queue-pagination"><button class="secondary" :disabled="groupPage===1" @click="groupPage--">上一组页</button><span>{{groupPage}} / {{groupPages}}</span><button class="secondary" :disabled="groupPage===groupPages" @click="groupPage++">下一组页</button></div></details>
 <div ref="taskFilterArea" class="upload-failure-anchor"></div><div v-if="taskGroup||failedOnly" class="upload-task-actions"><strong>{{selectedGroup?selectedGroup.title+' · '+cloudName(selectedGroup.kind):'所有上传任务'}}{{failedOnly?' · 仅失败项':''}}</strong><button class="secondary" :disabled="taskControlling" @click="taskGroup='';failedOnly=false">查看全部任务</button></div>
 <div v-if="tasks.length" class="upload-task-actions"><button class="secondary" :disabled="taskControlling" @click="failedOnly=!failedOnly;filter='all'">{{failedOnly?'显示其他状态':'只看失败项'}}</button><button class="secondary" :disabled="taskControlling||!visibleFailures.length" @click="retryFailedTasks()">重试当前筛选的失败项（{{Math.min(1000,visibleFailures.length)}}）</button><button class="secondary" :disabled="taskControlling||!!copyingTask||!visibleFailures.length" @click="copyFailures">复制失败报告</button><small>失败项操作包含当前筛选的其他页，最多1000项；成功项与主动暂停项不会重新提交。先核对远端是否已有文件，避免重复上传。</small></div>
 <section v-if="tasks.length" class="upload-task-filters" aria-label="筛选上传任务"><input v-model="taskQuery" :disabled="taskControlling" type="search" aria-label="搜索上传文件名或目标目录" placeholder="搜索上传文件名或目标目录"/><select v-model="taskKind" :disabled="taskControlling" aria-label="按网盘类型筛选上传任务"><option value="">所有网盘类型</option><option v-for="kind in taskKinds" :key="kind" :value="kind">{{cloudName(kind)}}</option></select><select v-model="taskSort" :disabled="taskControlling" aria-label="上传任务排序"><option value="newest">最新入队优先</option><option value="oldest">最早入队优先</option><option value="name">文件名升序</option><option value="attention">失败 / 暂停优先</option></select><button v-if="taskSort!=='newest'||filter!=='all'||taskKind||taskQuery" class="secondary" :disabled="taskControlling" @click="clearTaskFilters">清除筛选</button><p role="status">显示 {{visible.length}} / {{tasks.length}} 个任务 · 分类数量按全部任务统计</p></section>
 <div v-if="!visible.length" class="task-empty"><h2>{{tasks.length?'没有匹配的上传任务':'还没有上传记录'}}</h2><button v-if="!tasks.length" class="secondary" @click="tab='upload'">选择文件</button></div>
 <div v-if="visible.length" class="queue-pagination"><span>显示 {{taskPageView.first}}–{{taskPageView.last}} / {{visible.length}} 项</span><label>每页 <select v-model.number="taskPageSize" :disabled="taskControlling" aria-label="每页上传任务数"><option :value="25">25</option><option :value="50">50</option><option :value="100">100</option><option :value="200">200</option></select></label><button class="secondary" :disabled="taskControlling||taskPageView.page===1" @click="goTaskPage(1)">首页</button><button class="secondary" :disabled="taskControlling||taskPageView.page===1" @click="goTaskPage(taskPageView.page-1)">上一页</button><span>第 {{taskPageView.page}} / {{taskPageView.pages}} 页</span><button class="secondary" :disabled="taskControlling||taskPageView.page>=taskPageView.pages" @click="goTaskPage(taskPageView.page+1)">下一页</button></div><p v-if="tasks.length" class="preview-note">排序只改变显示，不改变上传调度。翻页、排序或筛选后清除选择，批量操作只处理当前页已选任务。</p><div v-if="selectableTasks.length" class="upload-task-actions"><label><input type="checkbox" :checked="chosenTasks.length===Math.min(selectableTasks.length,200)&&chosenTasks.length>0" :disabled="taskControlling" @change="selectVisibleTasks(($event.target as HTMLInputElement).checked)"/> 选择当前页可操作任务</label><span>已选 {{chosenTasks.length}} 个</span><button class="secondary" :disabled="taskControlling||!chosenStops" @click="controlChosen('stop')">停止所选（{{chosenStops}}）</button><button class="secondary" :disabled="taskControlling||!chosenRetries" @click="controlChosen('retry')">重新上传所选（{{chosenRetries}}）</button></div>
 <section v-if="taskControlling" class="upload-batch-results" role="status"><strong>{{taskBatchAction}}：已处理 {{taskBatchResults.length}} / {{taskBatchTotal}}</strong><p>当前：{{taskBatchName}}</p><progress :value="taskBatchResults.length" :max="taskBatchTotal" aria-label="上传批量操作请求进度"></progress><button class="secondary" :disabled="taskBatchStop" @click="stopTaskBatch">{{taskBatchStop?'正在结束后续处理…':'停止后续处理'}}</button><p>等待当前请求返回后停止，不撤销已提交请求，也不会停止已重新开始的上传。</p></section><details v-if="taskBatchResults.length" class="upload-batch-results" open><summary>{{taskBatchAction}}结果（{{taskBatchResults.length}} / {{taskBatchTotal}}）</summary><p>请求已提交 {{taskBatchSubmitted}} · 提交失败 {{taskBatchFailed}} · 跳过或未提交 {{taskBatchSkipped}}</p><p v-if="taskBatchResults.length>200">下方显示前200项结果，完整任务状态可在列表中查看。</p><ul><li v-for="(result,i) in taskBatchResults.slice(0,200)" :key="i" :class="{'form-error':result.failed}">{{result.name}}：{{result.message}}</li></ul></details>
 <p v-if="uploadFolderStatus" role="status" class="preview-note">{{uploadFolderStatus}}</p><p v-if="taskCopyStatus" role="status" class="preview-note">{{taskCopyStatus}}</p><article v-for="t in taskPageView.items" :key="t.id" class="task-row"><div class="task-heading"><input v-if="canTaskAction(t,'stop')||canTaskAction(t,'retry')" type="checkbox" :aria-label="'选择上传任务：'+t.file.name+'，'+cloudName(t.kind)" :checked="taskSelection.includes(t.id)" :disabled="taskControlling||(!taskSelection.includes(t.id)&&taskSelection.length>=200)" @change="toggleTask(t.id,($event.target as HTMLInputElement).checked)"/><span class="file-symbol">↥</span><div><strong>{{t.file.relativePath||t.file.name}}{{t.file.directory?'（目录）':''}}</strong><small>{{cloudName(t.kind)}} / {{t.folderDisplay||(t.folder==='-1'?'根目录':t.folder)}}</small></div><span class="status" :class="t.status">{{labels[t.status]??t.status}}</span></div><div class="task-progress"><progress :value="percent(t)" max="100" :aria-label="t.file.name+'上传进度'"/><span>{{percent(t)}}%</span></div><div v-if="uploadRecovery(t)" class="transfer-recovery"><strong>{{uploadRecovery(t)?.title}}</strong><p>{{uploadRecovery(t)?.detail}}</p><button v-if="uploadRecovery(t)?.kind==='auth'" class="secondary small" :disabled="taskControlling" @click="recoverAccount(t.accountId)">更新此网盘登录</button></div><div class="task-footer"><span>{{t.message}}<small v-if="isCloud(t.kind)&&t.status==='failed'">{{t.lastAttempt?'最近尝试：'+new Date(t.lastAttempt).toLocaleString():'这是旧版保存的结果，点击重新上传后会更新诊断'}}</small><small>{{bytes(t.sent)}} / {{bytes(t.file.size)}}</small></span><button class="secondary small" :disabled="!!openingUploadFolder" @click="openTaskFolder(t)">{{openingUploadFolder===t.id?'正在打开…':'打开本地文件夹'}}</button><button class="secondary small" :disabled="!!copyingTask" @click="copyTaskResult(t)">{{copyingTask===t.id?'正在复制…':'复制任务结果'}}</button><button v-if="t.status==='failed'||t.status==='paused'" class="secondary small" :disabled="taskControlling" @click="api.RestartUpload(t.id).then(refresh).catch(e=>error=msg(e))">从头重新上传</button><button v-if="t.status!=='success'" class="secondary small" :disabled="taskControlling" @click="control(t)">{{['running','queued'].includes(t.status)?'停止':'继续 / 重试'}}</button></div></article>
 <details class="operation-details"><summary>退出与续传</summary><p>关闭到托盘可继续传输，退出会暂停任务。夸克、123、光鸭尝试恢复已确认分片，蓝奏和 WebDAV 从头重试，百度由服务端决定分片复用。</p></details></template>
 </div></main><UploadReview v-if="uploadFlow.pending.value" :report="uploadFlow.pending.value.report" :checking="uploadFlow.checking.value" :submitting="uploadFlow.submitting.value" :error="uploadFlow.error.value" @cancel="uploadFlow.cancel" @check="uploadFlow.check" @submit="uploadFlow.submit"/><FolderPicker v-if="folderAccount" :kind="folderAccount.kind" :account-id="folderAccount.id" :account-name="folderAccount.name" :initial-folder="folderPurpose==='default'?form.folder:folders[folderAccount.id]||folderAccount.folder" :initial-breadcrumbs="folderPurpose==='upload'?uploadFolderTrails[folderAccount.id]:undefined" @close="folderAccount=null" @selected="folderChosen"/>
 <div v-if="modal" class="modal-backdrop" @click.self="!saving&&closeModal()"><section class="account-modal" @keydown.esc.prevent="!saving&&closeModal()" role="dialog" aria-modal="true" @keydown.tab="trapDialogFocus" aria-labelledby="account-title"><div class="modal-heading"><div><h2 id="account-title">{{form.id?'编辑网盘账号':'添加网盘账号'}}</h2><p>账号仅保存在这台 Windows 电脑。</p></div><button class="icon-button" aria-label="关闭" :disabled="saving" @click="closeModal">×</button></div>
 <form @submit.prevent="saveAccount"><fieldset class="account-fields" :disabled="saving||ctSmsBusy"><label>网盘类型<select v-model="form.kind" :disabled="!!form.id" @change="changeKind"><option value="lanzou">蓝奏云（实验接入）</option><option value="quark">夸克网盘（实验接入）</option><option value="pan123">123 云盘（实验接入）</option><option value="baidu">百度网盘（实验接入）</option><option value="guangya">光鸭云盘（实验接入）</option><option value="ctdav">诚通网盘（WebDAV，推荐）</option><option value="ctfile">诚通 OpenAPI（需接口权限）</option><option value="webdav">WebDAV</option></select></label><label>账号名称<input ref="accountNameInput" v-model="form.name" placeholder="例如：我的蓝奏云" required maxlength="80"/></label>
 <template v-if="['webdav','ctdav'].includes(form.kind)"><template v-if="form.kind==='ctdav'"><div class="help-box">请在诚通后台查看 WebDAV 配置，按页面显示填写服务器地址、用户名和 WebDAV 密码，无需 OpenAPI Session。</div><button type="button" class="secondary" @click="openCtWebDAV">打开诚通 WebDAV 设置</button></template><label>WebDAV 地址<input v-model="form.endpoint" placeholder="https://dav.example.com/dav" type="url" required/></label><label>用户名<input v-model="form.username" autocomplete="off" required/></label><label>应用密码<input v-model="form.secret" type="password" autocomplete="new-password" :required="!form.id" :placeholder="form.id?'留空保留原密码':'填写网盘的 WebDAV 应用密码'"/></label></template>
 <template v-else-if="form.kind==='ctfile'">
  <label>存储空间<select v-model="form.username" :disabled="!!form.id"><option value="public">公有云</option><option value="private">私有云</option></select></label>
  <div class="help-box">诚通旧短信和密码登录接口已停用。请点击下方按钮，在官方网站完成登录，进入「开放接口登录密钥管理」生成或查看 Session，然后复制到下方保存。无需获取 Cookie。</div>
  <button type="button" class="secondary" @click="openCtAuthorization">打开诚通官方密钥管理</button>
  <label>开放接口 Session<input v-model="form.secret" type="password" autocomplete="new-password" :required="!form.id" :placeholder="form.id?'留空保留原会话':'粘贴官方密钥管理页面的 Session'"/></label>
  <p class="muted">保存前会验证会话；仅在本机加密保存。另一存储空间可单独添加账号。</p>
 </template>
 <template v-else><div class="help-box"><template v-if="form.id&&!loginMode">可修改账号名称和默认上传目录；更新登录状态请勾选“重新登录此账号”，或展开高级选项导入登录信息。登录信息留空时保留原状态。</template><template v-else>点击下方登录按钮，在{{cloudName(form.kind)}}官方登录窗口完成登录。登录成功后自动返回并保存账号，无需手动获取登录信息。</template><br v-if="saving"/><strong v-if="saving">正在等待登录窗口完成…</strong></div><label v-if="form.id" class="login-toggle"><input type="checkbox" v-model="loginMode" :disabled="saving"/>重新登录此账号</label><details v-if="!saving" class="advanced-login"><summary>高级：手动导入登录信息</summary><label class="login-toggle"><input type="checkbox" :checked="!loginMode" @change="loginMode=!loginMode"/>使用手动导入</label><template v-if="!loginMode"><label>{{['pan123','guangya'].includes(form.kind)?'登录 Token':'登录 Cookie'}}<input v-model="form.secret" type="password" autocomplete="new-password" :required="!form.id" placeholder="留空保留已有登录状态"/></label><label v-if="form.kind==='guangya'">设备 ID（可选）<input v-model="form.username"/></label><label>浏览器 User-Agent（可选）<input v-model="form.userAgent"/></label></template></details></template>
 <div v-if="isCloud(form.kind)" class="default-folder"><span>默认保存目录</span><div><strong>{{form.folderDisplay||(form.folder===rootFolder(form.kind)?'根目录':`目录 ${form.folder}`)}}</strong><button v-if="form.id" type="button" class="secondary" @click="browse(form,'default')">选择目录</button></div><small v-if="!form.id">登录或保存账号后，可浏览网盘并选择其他目录。</small></div><label v-else>默认保存目录<input v-model="form.folder" placeholder="/" required/></label></fieldset><p v-if="error" class="form-error" role="alert">{{error}}</p><div class="modal-actions"><button v-if="form.id" type="button" class="danger-button" @click="removeAccount">移除账号</button><button v-if="saving&&isCloud(form.kind)&&loginMode&&form.kind!=='ctfile'" type="button" class="secondary" @click="api.CancelLanzouLogin()">取消登录</button><button v-else type="button" class="secondary" :disabled="saving" @click="closeModal">取消</button><button type="submit" class="primary" :disabled="saving||ctSmsBusy">{{saving?(isCloud(form.kind)&&loginMode?'等待登录…':'保存中…'):(isCloud(form.kind)&&loginMode?'登录'+cloudName(form.kind)+'并保存':'保存账号')}}</button></div></form></section></div>
</div>
</template>














