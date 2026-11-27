<script setup lang="ts">
import {trapDialogFocus} from './dialogFocus'
import {fileShortcut} from './fileShortcuts'
import {snapshotMaxFile,snapshotMaxBatch} from './relayLimits'
import {filterResults,resultCounts,type ResultFilter} from './downloadResults'
import {uploadSourceKey} from './uploadTasks'
import {BrowseHistory} from './browseHistory'
import {fileList,fileType,type FileSort,type DisplayKind} from './fileList'
import {computed,nextTick,onMounted,onUnmounted,ref,watch} from 'vue'
import {APP_VERSION} from './version'
import {DownloadMeter,remainingTime} from './downloadMeter'
import {Events} from '@wailsio/runtime'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import FolderPicker from './FolderPicker.vue'
import CopyHistory from './CopyHistory.vue'
import TrashDialog from './TrashDialog.vue'
import TrashHistory from './TrashHistory.vue'
import UploadReview from './UploadReview.vue'
import {useUploadReview} from './useUploadReview'
import AppIcon from './AppIcon.vue'
import ShareDialog from './ShareDialog.vue'
import BatchShareDialog from './BatchShareDialog.vue'
import type {Account,CloudFolder,CloudEntry,FilePage,BatchDownloadItem,BatchDownloadResult,DownloadResult,DownloadInput,File as SourceFile,RestoredFileForMove,RelayInput,MoveFileInput,CopyDestinationLocation,ShareRecreation,ShareSourceLocation} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accounts:Account[];accountId:string;blocked?:boolean;view?:'files'|'downloads';refreshKey?:number;locationRequest?:{accountId:string;id:string;breadcrumbs?:CloudFolder[];nonce:number;moveFile?:RestoredFileForMove;shareSource?:ShareSourceLocation;recreateShare?:boolean}|null}>()
const emit=defineEmits<{account:[id:string];saved:[id:string,folder:string,path:string];renamed:[id:string];queued:[];add:[];edit:[id:string,relogin:boolean];mutation:[active:boolean];downloads:[];uploads:[];viewFiles:[];shareReceipt:[kind:'share',accountId:string,receiptId:string];downloadStatus:[active:boolean,count:number];relayCopy:[inputs:RelayInput[],directory:boolean]}>()
function editCurrentAccount(relogin=false){if(!canNavigate.value||!account.value)return;closeContext();clearDrag();emit('edit',account.value.id,relogin&&canWebLogin.value)}
const canWebLogin=computed(()=>['lanzou','quark','pan123','baidu','guangya'].includes(account.value?.kind??''))
const account=computed(()=>props.accounts.find(a=>a.id===props.accountId)??props.accounts[0])
function cloudName(kind:string){return ({lanzou:'蓝奏云',quark:'夸克网盘',pan123:'123 云盘',baidu:'百度网盘',ctdav:'诚通网盘',ctfile:'诚通 OpenAPI',guangya:'光鸭云盘',webdav:'WebDAV'} as Record<string,string>)[kind]??kind}
const canShare=computed(()=>['lanzou','quark','pan123','baidu','guangya'].includes(account.value?.kind??''))
const shareTarget=ref<{accountId:string;parentId:string;kind:string;entry:CloudEntry;group?:CloudEntry[];recreation?:ShareRecreation}|null>(null)
function openShare(entry:CloudEntry,recreation?:ShareRecreation,group?:CloudEntry[]){
 const a=account.value,p=page.value
 if(!a||!p||!canShare.value||!canDrop.value)return
 closeContext();clearDrag()
 shareTarget.value={accountId:a.id,parentId:p.currentId,kind:a.kind,entry:{...entry},recreation,group}
 emit('mutation',true)
}
function closeShare(){shareTarget.value=null;emit('mutation',false)}
function openShareReceipt(id:string,batch=false){
 const target=batch?batchShareTarget.value:shareTarget.value
 if(!id||!target||props.blocked||target.accountId!==account.value?.id)return
 if(batch)closeBatchShare()
 else closeShare()
 emit('shareReceipt','share',target.accountId,id)
}
const batchShareTarget=ref<{accountId:string;parentId:string;kind:string;entries:CloudEntry[];initialSelection:CloudEntry[]}|null>(null)
function openBatchShare(useSelection=true){const a=account.value,p=page.value;if(!a||!p||!canShare.value||!canDrop.value)return;closeContext();clearDrag();batchShareTarget.value={accountId:a.id,parentId:p.currentId,kind:a.kind,entries:entries.value.map(e=>({...e})),initialSelection:useSelection?selectedShareEntries.value.map(e=>({...e})):[]};emit('mutation',true)}
function closeBatchShare(){batchShareTarget.value=null;emit('mutation',false)}
const canCreate=computed(()=>['lanzou','quark','pan123','baidu','ctdav','webdav','guangya'].includes(account.value?.kind??''))
const canMoveFolders=computed(()=>['ctdav','webdav','quark','pan123','baidu','guangya'].includes(account.value?.kind??''))
const copyStatus=ref(''),copyingReport=ref(false)
const locationBusy=ref(false),locationStatus=ref(''),locationError=ref('')
async function openSavedFolder(path:string){if(saving.value||locationBusy.value||!path)return;locationBusy.value=true;locationStatus.value='';locationError.value='';try{await api.OpenDownloadFolder(path);locationStatus.value='已请求打开保存文件夹'}catch(e){locationError.value=message(e)}finally{locationBusy.value=false}}
async function copySavedPath(path:string){if(saving.value||locationBusy.value||!path)return;locationBusy.value=true;locationStatus.value='';locationError.value='';try{await api.CopyDownloadReport(path);locationStatus.value='路径已复制'}catch(e){locationError.value=message(e)}finally{locationBusy.value=false}}
const canDownloadFolders=computed(()=>['ctdav','webdav','quark','pan123','baidu','guangya','lanzou'].includes(account.value?.kind??''))
const canDownload=computed(()=>['ctdav','webdav','pan123','guangya','quark','baidu','lanzou'].includes(account.value?.kind??''))
const root=computed(()=>account.value?.kind==='lanzou'?'-1':['quark','pan123'].includes(account.value?.kind??'')?'0':account.value?.kind==='guangya'?'root':account.value?.kind==='ctfile'?'d0':'/')
const browseHistory=new BrowseHistory(),navigationVersion=ref(0)
function accountSource(a:Account){return JSON.stringify([a.id,a.kind,a.endpoint??'',a.username??''])}
const accountScope=computed(()=>account.value?accountSource(account.value):'')
const canGoBack=computed(()=>{navigationVersion.value;return !!browseHistory.step(accountScope.value,-1)})
const canGoForward=computed(()=>{navigationVersion.value;return !!browseHistory.step(accountScope.value,1)})
function resetHistory(){browseHistory.clear(accountScope.value);navigationVersion.value++}
function clearFolderHistory(accountId:string){
 const previous=props.accounts.find(item=>item.id===accountId)
 if(!previous)return
 browseHistory.clear(accountSource(previous))
 navigationVersion.value++
}
defineExpose({clearFolderHistory})
async function goHistory(delta:-1|1){if(!canNavigate.value)return;const target=browseHistory.step(accountScope.value,delta);if(target)await load(target.location.id,target.location.breadcrumbs,target.index)}

const page=ref<FilePage|null>(null),busy=ref(false),saving=ref(false),error=ref(''),notice=ref(''),query=ref(''),sort=ref<FileSort>('asc')
const displayKind=ref<DisplayKind>('all')
const hasListFilter=computed(()=>!!query.value.trim()||displayKind.value!=='all')
function clearListFilters(){query.value='';displayKind.value='all'}
function sortState(column:'name'|'size'|'type'):'none'|'ascending'|'descending'{const active=sort.value.startsWith('size-')?'size':sort.value.startsWith('type-')?'type':'name';return active!==column?'none':sort.value==='desc'||sort.value.endsWith('-desc')?'descending':'ascending'}
function setSort(column:'name'|'size'|'type'){const direction=sortState(column)==='ascending'?'desc':'asc';sort.value=column==='name'?direction:(column+'-'+direction) as FileSort}
const createOpen=ref(false),creating=ref(false),createName=ref(''),createError=ref(''),nameInput=ref<HTMLInputElement|null>(null)
const renameTarget=ref<CloudEntry|null>(null),renameName=ref(''),renameError=ref(''),renaming=ref(false),renameInput=ref<HTMLInputElement|null>(null)
const moveTargets=ref<CloudEntry[]>([]),moving=ref(false),moveError=ref('')
let restoredMoveReceipt=''
const fileOperation=ref<'move'|'copy'>('move')
const copyHistoryVersion=ref(0),copyHistoryBusy=ref(false)
const canCopy=computed(()=>['baidu','webdav','ctdav','pan123','guangya','quark','lanzou'].includes(account.value?.kind??''))
const canTrash=computed(()=>['baidu','pan123','lanzou','quark'].includes(account.value?.kind??''))
function canTrashEntries(group:CloudEntry[]){return canTrash.value&&group.length>0&&group.length<=50}
const trashTarget=ref<{accountId:string;parentId:string;entry:CloudEntry}[]|null>(null),trashHistoryVersion=ref(0),trashHistoryBusy=ref(false)
const trashLocation=ref(''),trashAccountName=ref('')
function openTrash(group:CloudEntry[]){const a=account.value,p=page.value;if(!a||!p||!canDrop.value||!canTrashEntries(group))return;closeContext();clearDrag();trashLocation.value=currentPath.value;trashAccountName.value=cloudName(a.kind)+' / '+a.name;trashTarget.value=group.map(entry=>({accountId:a.id,parentId:p.currentId,entry:{...entry}}));emit('mutation',true)}
function closeTrash(){trashTarget.value=null;emit('mutation',false)}
async function trashChanged(){trashHistoryVersion.value++;selectedIds.value=[];selectedFolderIds.value=[];if(page.value)await load(page.value.currentId,page.value.breadcrumbs??undefined)}
function contextTrash(){const entry=contextState.value?.entry,group=contextShareGroup.value;closeContext();if(entry)openTrash(group?selectedShareEntries.value:[entry])}
const guangyaNativeCopyMaxFile=2*1024**3
function snapshotCopyAllows(entries:CloudEntry[]){
 return entries.length>0&&entries.length<=20&&entries.every(entry=>!entry.folder&&entry.size!=null&&entry.size>0&&entry.size<=snapshotMaxFile)&&
  entries.reduce((total,entry)=>total+(entry.size??0),0)<=snapshotMaxBatch
}
function copyAllows(entries:CloudEntry[]){
 if(!canCopy.value||!entries.length||entries.length>50)return false
 if(account.value?.kind==='lanzou')return entries.length<=20
 if(account.value?.kind==='quark'){
  if(entries.some(entry=>entry.folder))return entries.length===1
  return snapshotCopyAllows(entries)
 }
 if(account.value?.kind==='guangya'){
  if(snapshotCopyAllows(entries))return true
  return entries.every(entry=>entry.folder||entry.size!=null&&entry.size>=0&&entry.size<=guangyaNativeCopyMaxFile)
 }
 return true
}
const canCopySelection=computed(()=>copyAllows(selectedShareEntries.value))
function openCopy(entry:CloudEntry,group:CloudEntry[]=[entry]){if(!copyAllows(group)||!canDrop.value)return;openMove(entry,group,'','copy')}
function openSelectedCopy(){if(canCopySelection.value)openCopy(selectedShareEntries.value[0],selectedShareEntries.value)}
function contextCopy(){const entry=contextState.value?.entry,group=contextShareGroup.value;closeContext();if(entry)openCopy(entry,group?selectedShareEntries.value:[entry])}

const selectedIds=ref<string[]>([])
type MoveResult={name:string;success:boolean;message:string;skipped?:boolean}
const moveResults=ref<MoveResult[]>([]),stopMoving=ref(false)
const copyRetry=ref<{scope:string;parentId:string;revision:number;entries:CloudEntry[]}|null>(null)
const copyRetryEntries=computed(()=>{
 const retry=copyRetry.value,p=page.value
 if(!retry||!p||retry.scope!==accountScope.value||retry.parentId!==p.currentId||retry.revision!==(props.refreshKey??0))return []
 const entries=retry.entries.map(original=>p.entries?.find(entry=>
  entry.id===original.id&&entry.folder===original.folder&&entry.name===original.name&&entry.size===original.size&&entry.objectId===original.objectId))
 return entries.every((entry):entry is CloudEntry=>!!entry)?entries:[]
})
function openCopyRetry(){
 const entries=copyRetryEntries.value
 if(!canNavigate.value||!canDrop.value||!entries.length||!copyAllows(entries))return
 openMove(entries[0],entries,'','copy')
}
const currentCopy=ref<MoveFileInput|null>(null),stoppingCopy=ref(false),copyStopError=ref('')
async function stopCurrentCopy(){
 const input=currentCopy.value
 if(!input||stoppingCopy.value)return
 stopMoving.value=true
 stoppingCopy.value=true
 copyStopError.value=''
 try{
  const stopped=await api.CancelCloudCopy(input.accountId,input.entryId,input.destinationId)
  if(!stopped&&currentCopy.value===input)copyStopError.value='当前复制尚未进入可停止阶段，可稍后重试。'
 }catch(e){if(currentCopy.value===input)copyStopError.value=message(e)}
 finally{if(currentCopy.value===input)stoppingCopy.value=false}
}
const downloading=ref(false),downloadCancelling=ref(false),downloadName=ref(''),downloadError=ref(''),downloadWarning=ref(''),downloadDetails=ref(''),downloadProgress=ref({received:0,total:-1})
const downloadMeter=new DownloadMeter(),meterNow=ref(0)
let meterTimer:ReturnType<typeof setInterval>|undefined
const downloadMetrics=computed(()=>downloadMeter.read(meterNow.value))
function resetDownloadMeter(){downloadMeter.reset();meterNow.value=performance.now();downloadProgress.value={received:0,total:-1}}
function recordDownloadProgress(key:string,p:{received:number;total:number}){const now=performance.now();if(p&&downloadMeter.push(key,p.received,p.total,now)){downloadProgress.value=p;meterNow.value=now}}
function finishDownloadMeter(key:string){downloadMeter.finish(key);meterNow.value=performance.now()}
function stopMeterClock(){if(meterTimer!==undefined){clearInterval(meterTimer);meterTimer=undefined}}
watch(downloading,active=>{stopMeterClock();if(active&&!disposed){meterNow.value=performance.now();meterTimer=setInterval(()=>{meterNow.value=performance.now()},500)}})
const downloadRateLabel=computed(()=>{const m=downloadMetrics.value;if(m.phase==='complete')return '当前文件已处理完成';if(m.phase==='checking')return '内容已接收，正在校验并保存…';if(m.phase==='stalled')return '近期速度：0 B/s · 暂无新数据';return '近期速度：'+(m.bytesPerSecond===null?'等待采样':downloadBytes(Math.round(m.bytesPerSecond))+'/s')})
const downloadRemainingLabel=computed(()=>{const m=downloadMetrics.value;if(m.phase==='complete'||m.phase==='checking')return '';return '当前文件预计剩余：'+(downloadProgress.value.total<0?'大小未知':remainingTime(m.remainingSeconds))})
type BatchDownloadEvent={index:number;count:number;name:string;progress:{received:number;total:number};item?:BatchDownloadItem}
const resultFilter=ref<ResultFilter>('all'),resultQuery=ref('')
const resultFilters:{value:ResultFilter;label:string}[]=[{value:'all',label:'全部'},{value:'verified',label:'校验通过'},{value:'unverified',label:'已保存 · 未验证'},{value:'unfinished',label:'未完成'}]
function clearResultFilters(){resultFilter.value='all';resultQuery.value=''}
const singleResult=ref<DownloadResult|null>(null),downloadMessage=ref(''),downloadOrigin=ref(''),downloadKind=ref('')
function beginDownloadContext(){clearResultFilters();locationStatus.value='';locationError.value='';singleResult.value=null;downloadMessage.value='';downloadOrigin.value=currentPath.value;downloadKind.value=account.value?.kind??''}
const folderDownloading=ref(false),folderResults=ref<BatchDownloadItem[]>([]),folderDirectory=ref('')
const folderRetryId=ref(''),folderSource=ref<DownloadInput|null>(null)
const folderPending=computed(()=>folderResults.value.filter(item=>item&&item.status!=='saved').length)
const folderVisibleResults=computed(()=>folderResults.value.filter(Boolean))
const batchDownloading=ref(false),batchResults=ref<BatchDownloadItem[]>([]),batchSources=ref<DownloadInput[]>([])
const downloadItemCount=computed(()=>folderResults.value.length||batchResults.value.filter(Boolean).length||(singleResult.value?1:0))
const hasDownloadActivity=computed(()=>downloading.value||!!downloadOrigin.value||!!downloadError.value)
watch([downloading,downloadItemCount],([active,count])=>emit('downloadStatus',active,count),{immediate:true})
let selectionAnchor=''
function selectFile(event:MouseEvent,f:CloudEntry){if(!canDrop.value||(event.target as HTMLElement|null)?.closest?.('button,input,summary,details,a'))return;if(f.folder){toggleFolderSelection(f.id,!selectedFolderIds.value.includes(f.id));return};const rows=selectableEntries.value,anchor=rows.findIndex(e=>e.id===selectionAnchor),index=rows.findIndex(e=>e.id===f.id);if(event.shiftKey&&anchor>=0){const range=rows.slice(Math.min(anchor,index),Math.max(anchor,index)+1).map(e=>e.id);selectedIds.value=event.ctrlKey?[...new Set([...selectedIds.value,...range])]:range}else if(event.ctrlKey||event.metaKey)toggleSelection(f.id,!selectedIds.value.includes(f.id));else selectedIds.value=[f.id];selectionAnchor=f.id}
function goUp(){if(!canNavigate.value)return;const crumbs=page.value?.breadcrumbs??[];if(crumbs.length>1)go(crumbs[crumbs.length-2]!)}
let batchAttemptIndices:number[]=[]
const batchVisibleResults=computed(()=>batchResults.value.filter(Boolean))
const currentResults=computed(()=>folderVisibleResults.value.length?folderVisibleResults.value:batchVisibleResults.value)
const outcomeCounts=computed(()=>resultCounts(currentResults.value))
const filteredBatchResults=computed(()=>filterResults(batchVisibleResults.value,resultFilter.value,resultQuery.value))
const filteredFolderResults=computed(()=>filterResults(folderVisibleResults.value,resultFilter.value,resultQuery.value))
const shownResultCount=computed(()=>filterResults(currentResults.value,resultFilter.value,resultQuery.value).length)
const batchPendingIndices=computed(()=>batchSources.value.flatMap((_,i)=>batchResults.value[i]?.status==='saved'?[]:[i]))
function batchStatus(item:BatchDownloadItem){return item.status==='saved'?(item.result.verification==='verified'?'已保存 · 校验通过':'已保存 · 未验证'):item.status==='cancelled'?'已取消':item.status==='skipped'?'未执行':'失败'}
function downloadBytes(n:number){if(n<0)return '大小未知';if(n<1024)return n+' B';const units=['KB','MB','GB','TB'];let value=n/1024,i=0;while(value>=1024&&i<3){value/=1024;i++};return value.toFixed(1)+' '+units[i]}
async function download(entry:CloudEntry){
 const a=account.value,p=page.value;if(!a||!p||entry.folder||!canDownload.value||!canDrop.value)return
 beginDownloadContext();batchResults.value=[];batchSources.value=[];folderResults.value=[];folderDirectory.value='';folderRetryId.value='';folderSource.value=null;clearDrag();saving.value=true;downloading.value=true;downloadCancelling.value=false;downloadName.value=entry.name;downloadError.value='';downloadWarning.value='';downloadDetails.value='';copyStatus.value='';resetDownloadMeter();notice.value='';emit('mutation',true)
 try{const result=await api.DownloadCloudFile({accountId:a.id,parentId:p.currentId,entryId:entry.id,name:entry.name});if(disposed)return;if(result?.path)singleResult.value=result;if(result?.cancelled)notice.value='下载已取消，未生成正式文件。';else if(result?.path){downloadDetails.value=result.details??'';if(result.verification==='verified')notice.value=`已下载「${entry.name}」到：${result.path}。校验通过。`;else downloadWarning.value=`已保存「${entry.name}」到：${result.path}。下载完成，未验证原件完整性。`;}else downloadError.value='下载未返回保存结果，请检查本机保存位置。';downloadMessage.value=notice.value}
 catch(e){if(!disposed)downloadError.value=message(e)}
 finally{emit('mutation',false);if(!disposed){saving.value=false;downloading.value=false;downloadCancelling.value=false}}
}
async function queueFolder(entry:CloudEntry){const a=account.value,p=page.value;if(!a||!p||!canDrop.value)return;saving.value=true;try{const n=await api.QueueCloudFolder({accountId:a.id,parentId:p.currentId,entryId:entry.id,name:entry.name});notice.value='目录已处理，'+n+' 个文件已加入持久队列；空目录会保留。'}catch(e){error.value=message(e)}finally{saving.value=false}}
async function queueSelected(){if(selectedFolderIds.value.length)return;const a=account.value,p=page.value;if(!a||!p||!canDrop.value)return;const entries=[...selectedEntries.value];if(!entries.length)return;saving.value=true;try{const n=await api.QueueCloudDownloads(entries.map(f=>({accountId:a.id,parentId:p.currentId,entryId:f.id,name:f.name})));notice.value=n?'已加入 '+n+' 个下载任务，可在左侧下载队列查看。':'已取消加入队列'}catch(e){error.value=message(e)}finally{saving.value=false}}
async function downloadSelected(){if(selectedFolderIds.value.length)return;
 const a=account.value,p=page.value,targets=[...selectedEntries.value];if(!a||!p||!canDrop.value||!canDownload.value||!targets.length)return
 if(targets.length>200){downloadError.value='每批最多下载200个文件，请减少选择';return}
 beginDownloadContext();folderResults.value=[];folderDirectory.value='';folderRetryId.value='';folderSource.value=null;batchResults.value=[];batchSources.value=targets.map(entry=>({accountId:a.id,parentId:p.currentId,entryId:entry.id,name:entry.name}));await runBatchAttempt(batchSources.value.map((_,i)=>i))
}
async function retryBatch(){
 if(!canDrop.value||!canDownload.value||!batchPendingIndices.value.length)return
 const original=batchSources.value[0];if(!original||original.accountId!==account.value?.id)return
 await runBatchAttempt([...batchPendingIndices.value])
}
async function runBatchAttempt(indices:number[]){
 batchAttemptIndices=indices;const inputs=indices.map(i=>batchSources.value[i]);clearDrag();saving.value=true;downloading.value=true;batchDownloading.value=true;downloadCancelling.value=false;downloadName.value='选择保存目录…';downloadError.value='';downloadWarning.value='';downloadDetails.value='';copyStatus.value='';notice.value='';resetDownloadMeter();emit('mutation',true)
 try{const result=await api.DownloadCloudFiles(inputs);if(disposed)return;for(const [i,item] of (result.items??[]).entries()){const target=indices[i];if(target!==undefined)batchResults.value[target]=item}const saved=batchResults.value.filter(item=>item?.status==='saved').length;notice.value=result.cancelled?'本次下载已取消；已保存的文件保留。':'批量下载结束。';notice.value+=' 已保存 '+saved+' / '+batchSources.value.length+' 个文件。';downloadMessage.value=notice.value}catch(e){if(!disposed)downloadError.value=message(e)}finally{emit('mutation',false);if(!disposed){saving.value=false;downloading.value=false;batchDownloading.value=false;downloadCancelling.value=false}}
}
async function downloadFolder(entry:CloudEntry){
 const a=account.value,p=page.value;if(!a||!p||!entry.folder||!canDownloadFolders.value||!canDrop.value)return
 beginDownloadContext();clearDrag();batchResults.value=[];batchSources.value=[];folderResults.value=[];folderDirectory.value='';folderRetryId.value='';folderSource.value=null;saving.value=true;downloading.value=true;folderDownloading.value=true;downloadCancelling.value=false;downloadName.value='选择保存位置…';downloadError.value='';downloadWarning.value='';downloadDetails.value='';copyStatus.value='';notice.value='';resetDownloadMeter();emit('mutation',true)
 folderSource.value={accountId:a.id,parentId:p.currentId,entryId:entry.id,name:entry.name}
 try{const result=await api.DownloadCloudFolder(folderSource.value);if(!disposed)applyFolderResult(result)}catch(e){if(!disposed)downloadError.value=message(e)}finally{emit('mutation',false);if(!disposed){saving.value=false;downloading.value=false;folderDownloading.value=false;downloadCancelling.value=false}}
}
function applyFolderResult(result:BatchDownloadResult){
 folderResults.value=result.items??[];folderDirectory.value=result.directory??'';folderRetryId.value=result.retryId??''
 const saved=folderResults.value.filter(item=>item.status==='saved').length,unfinished=folderPending.value
 const summary=(result.cancelled?'文件夹下载已取消，已保存的内容保留。':unfinished?(saved?'文件夹下载部分完成。':'文件夹下载失败。'):'文件夹下载完成。')+' 已保存 '+saved+' 个文件，未完成 '+unfinished+' 个。'
 downloadMessage.value=summary
 if(result.cancelled||saved&&unfinished)downloadWarning.value=summary;else if(unfinished)downloadError.value=summary;else notice.value=summary
}
async function retryFolder(){
 const source=folderSource.value;if(!canDrop.value||!folderRetryId.value||!folderPending.value||!source||source.accountId!==account.value?.id)return
 clearDrag();saving.value=true;downloading.value=true;folderDownloading.value=true;downloadCancelling.value=false;downloadName.value='正在重试未完成文件…';downloadError.value='';downloadWarning.value='';downloadDetails.value='';copyStatus.value='';notice.value='';resetDownloadMeter();emit('mutation',true)
 try{const result=await api.RetryCloudFolder(source,folderRetryId.value);if(!disposed)applyFolderResult(result)}catch(e){if(!disposed)downloadError.value=message(e)}finally{emit('mutation',false);if(!disposed){saving.value=false;downloading.value=false;folderDownloading.value=false;downloadCancelling.value=false}}
}

function downloadReport(){
 const lines=['MultiPan '+APP_VERSION,'网盘：'+cloudName(downloadKind.value||account.value?.kind||''),'来源目录：'+(downloadOrigin.value||currentPath.value)];if(downloadError.value)lines.push('错误：'+downloadError.value);if(downloadWarning.value)lines.push(downloadWarning.value);if(downloadDetails.value)lines.push(downloadDetails.value);if(downloadMessage.value&&downloadMessage.value!==downloadWarning.value&&downloadMessage.value!==downloadError.value)lines.push(downloadMessage.value);if(folderDirectory.value)lines.push('保存根目录：'+folderDirectory.value);const rows=folderVisibleResults.value.length?folderVisibleResults.value:batchVisibleResults.value;for(const row of rows){lines.push('['+batchStatus(row)+'] '+row.name);if(row.message)lines.push('原因：'+row.message);if(row.result.path)lines.push('保存：'+row.result.path);if(row.result.details)lines.push('校验：'+row.result.details)};return lines.join('\n')
}
async function copyReport(){if(copyingReport.value)return;copyingReport.value=true;copyStatus.value='';try{await api.CopyDownloadReport(downloadReport());copyStatus.value='已复制结果；对外发送前可隐藏文件名和本机路径。'}catch(e){copyStatus.value=message(e)}finally{copyingReport.value=false}}
async function cancelDownload(){if(!downloading.value||downloadCancelling.value)return;downloadCancelling.value=true;try{await api.CancelDownload()}catch(e){downloadError.value=message(e);downloadCancelling.value=false}}
const localFiles=ref<SourceFile[]>([]),uploadError=ref(''),uploading=ref(false)
const canNavigate=computed(()=>!!account.value&&props.view!=='downloads'&&!busy.value&&!saving.value&&!copyHistoryBusy.value&&!trashHistoryBusy.value&&!trashTarget.value&&!props.blocked&&!createOpen.value&&!renameTarget.value&&!moveTargets.value.length&&!localFiles.value.length&&!shareTarget.value&&!batchShareTarget.value)
const dragSource=ref<{accountId:string;parentId:string;entries:CloudEntry[]}|null>(null),dragOver=ref('')
const canDrop=computed(()=>!!account.value&&!!page.value&&canCreate.value&&!busy.value&&!saving.value&&!copyHistoryBusy.value&&!trashHistoryBusy.value&&!trashTarget.value&&!error.value&&!props.blocked&&!moveTargets.value.length&&!renameTarget.value&&!createOpen.value&&!shareTarget.value&&!batchShareTarget.value)
function clearDrag(){dragSource.value=null;dragOver.value=''}
function resetDrops(clearDownloads=true){closeContext();clearDrag();localFiles.value=[];uploadError.value='';selectedFolderIds.value=[];selectedIds.value=[];selectionAnchor='';moveResults.value=[];if(clearDownloads){clearResultFilters();locationStatus.value='';locationError.value='';resetDownloadMeter();downloadError.value='';downloadWarning.value='';downloadDetails.value='';copyStatus.value='';batchResults.value=[];batchSources.value=[];folderResults.value=[];folderDirectory.value='';folderRetryId.value='';folderSource.value=null;singleResult.value=null;downloadMessage.value='';downloadOrigin.value='';downloadKind.value=''}}
function beginDrag(event:DragEvent,entry:CloudEntry){if(!canDrop.value||(entry.folder&&!canMoveFolders.value)||!event.dataTransfer){event.preventDefault();return};const group=selectedShareEntries.value.some(f=>f.id===entry.id&&f.folder===entry.folder)?selectedShareEntries.value:[entry];if(group.length>50||group.some(f=>f.folder&&!canMoveFolders.value)){event.preventDefault();return};dragSource.value={accountId:account.value!.id,parentId:page.value!.currentId,entries:[...group]};event.dataTransfer.effectAllowed='move';event.dataTransfer.setData('application/x-multipan-entry','move')}
function validDestination(id:string){const source=dragSource.value;return canDrop.value&&!!source&&source.accountId===account.value?.id&&source.parentId===page.value?.currentId&&id!==source.parentId&&!source.entries.some(f=>f.folder&&(id===f.id||id.startsWith(f.id+'/')))}
function overDestination(event:DragEvent,id:string){if(!validDestination(id))return;event.preventDefault();event.stopPropagation();if(event.dataTransfer)event.dataTransfer.dropEffect='move';dragOver.value=id}
function leaveDestination(event:DragEvent){if(event.currentTarget instanceof Node&&event.relatedTarget instanceof Node&&event.currentTarget.contains(event.relatedTarget))return;dragOver.value=''}
async function dropDestination(event:DragEvent,id:string,path:string){if(!validDestination(id))return;event.preventDefault();event.stopPropagation();const group=dragSource.value!.entries;clearDrag();restoredMoveReceipt='';moveTargets.value=group;fileOperation.value='move';await move(id,path)}
function crumbPath(index:number){return (page.value?.breadcrumbs??[]).slice(0,index+1).map(f=>f.name).join(' / ')}
type DroppedFiles={targetId:string;files:SourceFile[];directories:number;unreadable:number;duplicates:number;messages?:string[]}
function acceptLocalDrop(drop:DroppedFiles){if(drop?.targetId!=='cloud-upload-dropzone'||!canDrop.value||!Array.isArray(drop.files))return;uploadError.value='';notice.value='';const known=new Set(localFiles.value.map(uploadSourceKey));let duplicates=drop.duplicates??0;for(const f of drop.files){const key=uploadSourceKey(f);if(known.has(key)){duplicates++;continue};known.add(key);localFiles.value.push(f)};const skipped:string[]=[];if(duplicates)skipped.push(`${duplicates} 个重复项目`);if(drop.directories)skipped.push(`${drop.directories} 个未能扫描的文件夹`);if(drop.unreadable)skipped.push(`${drop.unreadable} 个无法访问的项目`);if(drop.messages?.length)skipped.push(...drop.messages);if(skipped.length)uploadError.value='已忽略：'+skipped.join('、')}
async function pickLocal(){if(!canDrop.value)return;const accountId=account.value!.id,parentId=page.value!.currentId;try{const files=await api.PickFiles();if(!disposed&&account.value?.id===accountId&&page.value?.currentId===parentId)acceptLocalDrop({targetId:'cloud-upload-dropzone',files:files??[],directories:0,unreadable:0,duplicates:0})}catch(e){if(!disposed)uploadError.value=message(e)}}
let uploadOrigin={id:'',kind:'',path:''}
const uploadFlow=useUploadReview(async(count,skipped)=>{if(disposed)return;emit('queued');if(account.value?.id!==uploadOrigin.id)return;localFiles.value=[];notice.value=`已创建 ${count} 个上传任务${skipped?'，跳过 '+skipped+' 项（未创建任务）':''}，目标：${cloudName(uploadOrigin.kind)} / ${uploadOrigin.path}。请在上传任务查看结果。`},active=>{saving.value=active;uploading.value=active;emit('mutation',active)})
async function uploadLocal(){const a=account.value,p=page.value;if(!a||!p||!canDrop.value||!localFiles.value.length)return;uploadOrigin={id:a.id,kind:a.kind,path:currentPath.value};uploadError.value='';await uploadFlow.begin(localFiles.value,[{accountId:a.id,folder:p.currentId}])}
const contextState=ref<{entry:CloudEntry|null;x:number;y:number;trigger?:HTMLElement}|null>(null),contextMenu=ref<HTMLElement|null>(null)
const contextShareGroup=computed(()=>{const state=contextState.value,e=state?.entry;return !state?.trigger&&!!e&&selectedShareEntries.value.some(f=>f.id===e.id&&f.folder===e.folder)&&selectedShareEntries.value.length>1})
const contextGroup=computed(()=>{const state=contextState.value,entry=state?.entry;return !state?.trigger&&entry&&!entry.folder&&selectedIds.value.includes(entry.id)&&selectedEntries.value.length>1})
function openEntryActions(event:MouseEvent,entry:CloudEntry){
 const trigger=event.currentTarget as HTMLButtonElement
 if(contextState.value?.trigger===trigger){closeContext();return}
 void openContext(event,entry,trigger)
}
async function openContext(event:MouseEvent,entry:CloudEntry|null=null,trigger?:HTMLElement){
 if(saving.value||busy.value||props.blocked||!page.value)return
 clearDrag()
 if(!trigger){
  if(entry?.folder&&!selectedFolderIds.value.includes(entry.id)){selectedFolderIds.value=[entry.id];selectedIds.value=[];selectionAnchor=''}
  else if(entry&&!entry.folder&&!selectedIds.value.includes(entry.id)){selectedIds.value=[entry.id];selectionAnchor=entry.id}
 }
 const anchor=trigger?.getBoundingClientRect()
 contextState.value={entry,trigger,x:anchor?.left??event.clientX,y:anchor?.bottom??event.clientY}
 const state=contextState.value
 await nextTick()
 const menu=contextMenu.value
 if(!menu||contextState.value!==state)return
 const bounds=menu.getBoundingClientRect()
 const desiredX=anchor?anchor.right-bounds.width:state.x
 const desiredY=anchor&&state.y+bounds.height>window.innerHeight?anchor.top-bounds.height:state.y
 state.x=Math.max(8,Math.min(desiredX,window.innerWidth-bounds.width-8))
 state.y=Math.max(8,Math.min(desiredY,window.innerHeight-bounds.height-8))
 await nextTick()
 if(contextState.value===state)menu.querySelector<HTMLButtonElement>('button:not([disabled])')?.focus({preventScroll:true})
}
function closeContext(){contextState.value=null}
function contextKeys(event:KeyboardEvent){if(event.key==='Tab'||event.key==='Escape'){if(event.key==='Escape')event.preventDefault();const trigger=contextState.value?.trigger;closeContext();trigger?.focus({preventScroll:true});return}if(!['ArrowDown','ArrowUp','Home','End'].includes(event.key))return;event.preventDefault();const buttons=Array.from(contextMenu.value?.querySelectorAll<HTMLButtonElement>('button:not([disabled])')??[]);if(!buttons.length)return;const index=buttons.indexOf(document.activeElement as HTMLButtonElement);const next=event.key==='Home'?0:event.key==='End'?buttons.length-1:(index+(event.key==='ArrowDown'?1:-1)+buttons.length)%buttons.length;buttons[next]?.focus()}
function contextAction(action:string){const entry=contextState.value?.entry,group=contextGroup.value,shareGroup=contextShareGroup.value;closeContext();if(action==='upload')void pickLocal();else if(action==='queue'&&entry?.folder)void queueFolder(entry);else if(action==='create')void openCreate();else if(action==='refresh')void load(page.value?.currentId??root.value,page.value?.breadcrumbs??undefined);else if(action==='all')selectAll(true);else if(entry){if(action==='open'&&entry.folder)go({id:entry.id,name:entry.name});else if(action==='share')openShare(entry);else if(action==='rename')void openRename(entry);else if(action==='download'){if(group)void downloadSelected();else if(entry.folder)void downloadFolder(entry);else void download(entry)}else if(action==='move'){if(shareGroup)openSelectedMove();else openMove(entry)}}}
function dismissContext(event:Event){
 const target=event.target as Node
 if(contextMenu.value&&!contextMenu.value.contains(target)&&!contextState.value?.trigger?.contains(target))closeContext()
}
function handleFileKey(event:KeyboardEvent){
 if(disposed||props.view==='downloads'||props.blocked||!account.value||!!shareTarget.value||createOpen.value||renameTarget.value||moveTargets.value.length||localFiles.value.length)return
 const target=event.target as HTMLElement|null,editing=!!target?.closest?.('input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="dialog"]')
 const action=fileShortcut(event,editing);if(!action)return
 event.preventDefault();if(saving.value||busy.value||batchShareTarget.value)return
 if(action==='refresh'){closeContext();void load(page.value?.currentId??root.value,page.value?.breadcrumbs??undefined);return}
 if(!canDrop.value)return
 if(action==='selectAll')selectAll(true)
 else if(action==='rename'&&selectedShareEntries.value.length===1)void openRename(selectedShareEntries.value[0]!)
 else if(action==='create'){closeContext();void openCreate()}
 else if(action==='up'){closeContext();goUp()}
 else if(action==='back'){closeContext();void goHistory(-1)}
 else if(action==='forward'){closeContext();void goHistory(1)}
 else if(action==='clear'){closeContext();selectedFolderIds.value=[];selectedIds.value=[];selectionAnchor='';clearDrag()}
 else if(action==='menu'){const entry=selectedEntries.value[0]??null;const rect=typeof document!=='undefined'?(document.querySelector('.file-workbench .cloud-row-selected')??document.querySelector('.file-workbench'))?.getBoundingClientRect():undefined;void openContext({clientX:(rect?.left??0)+24,clientY:(rect?.top??100)+24} as MouseEvent,entry)}
}
function dismissEntryMenus(event:Event){const target=event.target as Node|null;for(const menu of Array.from(document.querySelectorAll<HTMLDetailsElement>('.location-menu[open]')))if(!target||!menu.contains(target))menu.open=false}
let offFolder:(()=>void)|undefined
let offBatch:(()=>void)|undefined
let offDrop:(()=>void)|undefined,offDownload:(()=>void)|undefined
onMounted(()=>{
 if(typeof document!=='undefined'){document.addEventListener('pointerdown',dismissEntryMenus);document.addEventListener('pointerdown',dismissContext);document.addEventListener('keydown',handleFileKey)}
 offFolder=Events.On('multipan:folder-download',event=>{if(!folderDownloading.value)return;const data=event.data as BatchDownloadEvent;const key='folder:'+data.index;downloadName.value=data.index>0?data.index+' / '+data.count+' · '+data.name:data.name;if(data.item){folderResults.value[data.index-1]=data.item;finishDownloadMeter(key)}else recordDownloadProgress(key,data.progress)})
 offBatch=Events.On('multipan:batch-download',event=>{if(!batchDownloading.value)return;const data=event.data as BatchDownloadEvent;const key='batch:'+data.index;downloadName.value=data.index+' / '+data.count+' · '+data.name;const target=batchAttemptIndices[data.index-1];if(data.item&&target!==undefined){batchResults.value[target]=data.item;finishDownloadMeter(key)}else recordDownloadProgress(key,data.progress)})
 offDrop=Events.On('multipan:files-dropped',event=>acceptLocalDrop(event.data as DroppedFiles))
 offDownload=Events.On('multipan:download-progress',event=>{if(!downloading.value||batchDownloading.value||folderDownloading.value)return;recordDownloadProgress('single',event.data as {received:number;total:number})})
})
watch(()=>props.blocked,blocked=>{if(blocked)clearDrag()})
const entries=computed(()=>fileList(page.value?.entries??[],query.value,displayKind.value,sort.value))
const selectedFolderIds=ref<string[]>([])
const selectedShareEntries=computed(()=>entries.value.filter(f=>f.folder?selectedFolderIds.value.includes(f.id):selectedIds.value.includes(f.id)))
function toggleFolderSelection(id:string,checked:boolean){if(!canDrop.value)return;selectedFolderIds.value=checked?[...new Set([...selectedFolderIds.value,id])]:selectedFolderIds.value.filter(v=>v!==id)}
const selectableEntries=computed(()=>entries.value.filter(f=>!f.folder))
const selectedEntries=computed(()=>selectableEntries.value.filter(f=>selectedIds.value.includes(f.id)))
const allSelected=computed(()=>entries.value.length>0&&selectedShareEntries.value.length===entries.value.length)
function toggleSelection(id:string,checked:boolean){if(!canDrop.value)return;selectedIds.value=checked?[...new Set([...selectedIds.value,id])]:selectedIds.value.filter(value=>value!==id)}
function selectAll(checked:boolean){if(!canDrop.value)return;selectedFolderIds.value=checked?entries.value.filter(f=>f.folder).map(f=>f.id):[];selectedIds.value=checked?selectableEntries.value.map(f=>f.id):[]}
const canMoveSelection=computed(()=>selectedShareEntries.value.length>0&&selectedShareEntries.value.length<=50&&(!selectedFolderIds.value.length||canMoveFolders.value))
function openSelectedMove(){if(!canDrop.value||!canMoveSelection.value)return;openMove(selectedShareEntries.value[0],selectedShareEntries.value)}
watch([query,displayKind],()=>{selectedFolderIds.value=[];selectedIds.value=[];selectionAnchor='';clearDrag();closeContext()})
watch(sort,()=>{clearDrag();closeContext()})
const folderCount=computed(()=>(page.value?.entries??[]).filter(f=>f.folder).length)
const currentPath=computed(()=>(page.value?.breadcrumbs??[]).map(f=>f.name).join(' / '))
let request=0,disposed=false
function message(e:unknown){return e instanceof Error?e.message:String(e)}
async function load(id:string,crumbs?:CloudFolder[],historyIndex?:number){
 const a=account.value;if(!a)return
 const scope=accountSource(a)
 if(copyRetry.value&&(copyRetry.value.scope!==scope||copyRetry.value.parentId!==id||copyRetry.value.revision!==(props.refreshKey??0)))copyRetry.value=null
 resetDrops(false)
 const ticket=++request;busy.value=true;error.value='';notice.value='';query.value='';page.value=null;createOpen.value=false;createName.value='';createError.value='';renameTarget.value=null;renameError.value='';moveTargets.value=[];moveError.value=''
 try{const next=await api.ListCloudFiles(a.id,id);if(disposed||ticket!==request)return;if(crumbs&&!['baidu','ctdav','webdav'].includes(a.kind))next.breadcrumbs=crumbs;const location={id:next.currentId,breadcrumbs:next.breadcrumbs??[]};if(historyIndex===undefined||!browseHistory.commit(scope,historyIndex,location))browseHistory.record(scope,location);navigationVersion.value++;page.value=next}
 catch(e){if(!disposed&&ticket===request)error.value='无法打开目录，位置可能已变化或登录已失效：'+message(e)}
 finally{if(!disposed&&ticket===request)busy.value=false}
}
function go(folder:CloudFolder){if(!canNavigate.value)return;const crumbs=page.value?.breadcrumbs??[];const index=crumbs.findIndex(f=>f.id===folder.id);load(folder.id,index>=0?crumbs.slice(0,index+1):[...crumbs,folder])}
function openCopyDestination(accountId:string,location:CopyDestinationLocation){
 if(canNavigate.value&&account.value?.id===accountId)void load(location.folderId,location.breadcrumbs??undefined)
}
async function loadFileLocation(location:NonNullable<typeof props.locationRequest>){
 const ticket=request+1
 await load(location.id,location.breadcrumbs)
 if(disposed||ticket!==request||account.value?.id!==location.accountId||!page.value||error.value)return
 if(location.shareSource){
  selectShareSource(location.shareSource,location.recreateShare)
  return
 }
 const source=location.moveFile
 if(!source?.receiptId)return
 const matches=page.value.entries?.filter(entry=>entry.id===source.id)??[]
 if(page.value.currentId!==location.id||matches.length!==1||matches[0].folder||
    matches[0].name!==source.name||matches[0].size!==source.size||
    (source.objectId&&matches[0].objectId!==source.objectId)){
  notice.value='未能核对还原文件的原目录、名称或大小。请刷新后检查。'
  return
 }
 if(!canDrop.value||!canNavigate.value)return
 openMove(matches[0],[matches[0]],source.receiptId)
}
function selectShareSource(source:ShareSourceLocation,recreate=false){
 const a=account.value,p=page.value
 const targets=source.targets
 if(!a||!p||!canDrop.value||!canNavigate.value||source.accountId!==a.id)return
 if(!targets?.length){
  notice.value='已打开原目录。回执缺少可核对的原项目标识，请手动选择。'
  return
 }
 const matches:CloudEntry[]=[]
 if(p.currentId===source.parentId){
  for(const target of targets){
   const found=(p.entries??[]).filter(entry=>entry.id===target.id&&entry.folder===target.folder)
   if(found.length!==1||found[0].name!==target.name)break
   matches.push(found[0])
  }
 }
 const baiduIds=matches.map(entry=>entry.objectId??'').sort()
 const validBaiduIds=a.kind!=='baidu'||!!source.baiduIds?.length&&
  JSON.stringify(baiduIds)===JSON.stringify([...source.baiduIds].sort())
 if(matches.length!==targets.length||!validBaiduIds){
  notice.value='未能核对全部原项目，可能已移动、更名或更换。请检查后手动选择。'
  return
 }
 selectedIds.value=matches.filter(entry=>!entry.folder).map(entry=>entry.id)
 selectedFolderIds.value=matches.filter(entry=>entry.folder).map(entry=>entry.id)
 notice.value='已选中原分享的 '+matches.length+' 个项目。'
 if(recreate&&source.recreation)openShare(matches[0],source.recreation,matches.length>1?matches:undefined)
}
async function save(){const a=account.value,p=page.value;if(!a||!p||busy.value||saving.value||error.value)return;saving.value=true;const path=currentPath.value;try{await api.SetDefaultFolder(a.id,p.currentId,path);if(disposed)return;if(account.value?.id===a.id)notice.value='默认上传位置已设为：'+path;emit('saved',a.id,p.currentId,path)}catch(e){if(!disposed&&account.value?.id===a.id)error.value=message(e)}finally{if(!disposed)saving.value=false}}
async function openCreate(){if(!page.value||busy.value||saving.value||!canCreate.value)return;clearDrag();renameTarget.value=null;createOpen.value=true;createName.value='';createError.value='';notice.value='';await nextTick();nameInput.value?.focus()}
function closeCreate(){if(saving.value)return;createOpen.value=false;createName.value='';createError.value=''}
async function create(){
 const a=account.value,p=page.value;if(!a||!canCreate.value||!p||busy.value||saving.value||!createName.value)return
 const crumbs=p.breadcrumbs??undefined;saving.value=true;creating.value=true;createError.value=''
 try{const folder=await api.CreateCloudFolder(a.id,p.currentId,createName.value);if(disposed||account.value?.id!==a.id)return;await load(p.currentId,crumbs);if(disposed||account.value?.id!==a.id)return;notice.value=`已创建文件夹「${folder.name}」${error.value?'，请刷新列表查看。':'。'}`}
 catch(e){if(!disposed&&account.value?.id===a.id)createError.value=message(e)}
 finally{if(!disposed){saving.value=false;creating.value=false}}
}
function size(f:CloudEntry){if(f.folder)return '—';if(f.sizeText)return f.sizeText;if(f.size==null||f.size<0)return '—';if(f.size<1024)return `${f.size} B`;const units=['KB','MB','GB','TB'];let value=f.size/1024,i=0;while(value>=1024&&i<3){value/=1024;i++}return `${value.toFixed(value<10?1:0)} ${units[i]}`}
async function openRename(entry:CloudEntry){if(busy.value||saving.value||!canCreate.value)return;clearDrag();createOpen.value=false;renameTarget.value=entry;renameName.value=entry.name;renameError.value='';notice.value='';await nextTick();renameInput.value?.focus();renameInput.value?.select()}
function closeRename(){if(saving.value)return;renameTarget.value=null;renameName.value='';renameError.value=''}
async function rename(){const a=account.value,p=page.value,target=renameTarget.value;if(!a||!p||!target||saving.value||busy.value||!renameName.value||renameName.value===target.name)return;const crumbs=p.breadcrumbs??undefined;saving.value=true;renaming.value=true;renameError.value='';try{const result=await api.RenameCloudEntry({accountId:a.id,parentId:p.currentId,entryId:target.id,oldName:target.name,newName:renameName.value,folder:target.folder,parentDisplay:currentPath.value});if(disposed||account.value?.id!==a.id)return;emit('renamed',a.id);if(target.folder)resetHistory();await load(p.currentId,crumbs);if(!disposed&&account.value?.id===a.id)notice.value=`已重命名为「${result.name}」${error.value?'，请刷新列表查看。':'。'}`}catch(e){if(!disposed&&account.value?.id===a.id)renameError.value=message(e)}finally{if(!disposed){saving.value=false;renaming.value=false}}}
function openMove(entry:CloudEntry,group:CloudEntry[]=[entry],restoreReceipt='',operation:'move'|'copy'='move'){if(group.length>50||operation==='move'&&group.some(f=>f.folder&&!canMoveFolders.value)||saving.value||busy.value||!canCreate.value||props.blocked)return;clearDrag();renameTarget.value=null;createOpen.value=false;notice.value='';moveError.value='';restoredMoveReceipt=restoreReceipt;moveTargets.value=[...group];fileOperation.value=operation}
async function move(destinationId:string,destinationPath:string,destinationTrail?:CloudFolder[],directCopy=false){
 const a=account.value,p=page.value,targets=[...new Map(moveTargets.value.map(f=>[(f.folder?'d:':'f:')+f.id,f])).values()]
 const copying=fileOperation.value==='copy',verb=copying?'复制':'移动'
 if(!a||!p||!targets.length||targets.length>50||!copying&&targets.some(f=>f.folder&&!canMoveFolders.value)||saving.value||busy.value||props.blocked||destinationId===p.currentId)return
 if(copying&&!copyAllows(targets))return
 const guangyaOversize=copying&&a.kind==='guangya'&&targets.some(entry=>!entry.folder&&(entry.size??0)>guangyaNativeCopyMaxFile)
 const guangyaRelay=guangyaOversize&&directCopy
 if(guangyaOversize&&!guangyaRelay){
  notice.value='超过 2 GiB 的光鸭文件请选择「经中转复制到此目录」。'
  return
 }
 if(copying&&a.kind==='quark'||guangyaRelay){
  const directory=targets[0].folder
  if(!guangyaRelay&&directCopy||directory&&(destinationId===targets[0].id||destinationTrail?.some(folder=>folder.id===targets[0].id)))return
  copyRetry.value=null
  moveTargets.value=[]
  emit('relayCopy',targets.map(target=>({sourceAccountId:a.id,targetAccountId:a.id,parentId:p.currentId,
   destinationId,entryId:target.id,name:target.name,size:directory?0:target.size!})),directory)
  return
 }
 const crumbs=p.breadcrumbs??undefined
 const trail=destinationTrail??(()=>{const index=(crumbs??[]).findIndex(f=>f.id===destinationId);if(index>=0)return crumbs!.slice(0,index+1);const child=p.entries?.find(f=>f.folder&&f.id===destinationId);return child?[...(crumbs??[]),{id:child.id,name:child.name}]:[]})()
 const native123Copy=copying&&a.kind==='pan123'
 const nativeGuangyaCopy=copying&&a.kind==='guangya'
 const nativeLanzouCopy=copying&&a.kind==='lanzou'
 const directGuangyaCopy=directCopy&&nativeGuangyaCopy&&!guangyaRelay
 if(directCopy&&!native123Copy&&!directGuangyaCopy)return
 if(directCopy&&targets.some(target=>target.folder&&(target.id===destinationId||trail.some(folder=>folder.id===target.id))))return
 const restoreReceipt=copying?'':restoredMoveReceipt
 const retryScope={scope:accountScope.value,parentId:p.currentId,revision:props.refreshKey??0}
 if(restoreReceipt&&(targets.length!==1||targets[0].folder))return
 restoredMoveReceipt=''
 const makeEntry=(target:CloudEntry)=>({folder:target.folder,input:{accountId:a.id,parentId:p.currentId,destinationId,entryId:target.id,name:target.name,...((native123Copy||nativeGuangyaCopy||target.folder&&['quark','pan123','guangya'].includes(a.kind))?{sourceTrail:crumbs??[],destinationTrail:trail}:{})}})
 copyRetry.value=null;moveTargets.value=[];saving.value=true;moving.value=true;stopMoving.value=false;copyStopError.value='';moveError.value='';moveResults.value=[];emit('mutation',true)
 const results:MoveResult[]=[],scope=accountScope.value
 try{
  if(targets.length>1&&(copying||targets.some(t=>t.folder))){
   notice.value='正在预检查整批项目与目标目录，尚未'+verb
   const preflight=directGuangyaCopy?api.ValidateGuangyaDirectCopyBatch:directCopy?api.ValidatePan123DirectCopyBatch:native123Copy?api.ValidatePan123CopyBatch:nativeGuangyaCopy?api.ValidateGuangyaCopyBatch:nativeLanzouCopy?api.ValidateLanzouCopyBatch:api.ValidateCloudMoveBatch
   try{await preflight(targets.map(makeEntry))}
   catch(e){
    if(disposed||accountScope.value!==scope)return
    moveError.value='整批未提交：'+message(e)
    moveResults.value=targets.map(t=>({name:t.name,success:false,skipped:true,message:'预检查未通过，未发送'+verb+'请求'}))
    if(copying)copyRetry.value={...retryScope,entries:targets.map(entry=>({...entry}))}
    notice.value='预检查未通过'
    return
   }
  }
  for(const target of targets){
   if(disposed||account.value?.id!==a.id||accountScope.value!==scope)break
   if(stopMoving.value){for(const remaining of targets.slice(results.length))results.push({name:remaining.name,success:false,skipped:true,message:'已停止，未执行'});break}
   notice.value=`正在${verb} ${results.length+1} / ${targets.length}：${target.name}`
   try{const entry=makeEntry(target);if(copying)currentCopy.value=entry.input;if(directGuangyaCopy)await (target.folder?api.CopyGuangyaFolderToDirectory:api.CopyGuangyaFileToDirectory)(entry);else if(directCopy)await (target.folder?api.CopyPan123FolderToDirectory:api.CopyPan123FileToDirectory)(entry);else if(copying)await api.CopyCloudEntry(entry);else if(restoreReceipt)await api.MoveConfirmedRestoredFile(a.id,restoreReceipt,destinationId);else await (target.folder?api.MoveCloudFolder:api.MoveCloudFile)(entry.input);results.push({name:target.name,success:true,message:directGuangyaCopy?(target.folder?'已核对副本目录结构和文件 SHA-256':'已在目标目录核对副本的大小与 SHA-256'):directCopy?(target.folder?'已核对副本目录结构、文件大小与网盘 MD5':'已在目标目录核对副本的大小与网盘 MD5'):native123Copy?'已在独立子目录核对项目、大小和网盘 MD5':(nativeGuangyaCopy||nativeLanzouCopy)?'已在独立子目录核对源和副本的 SHA256':copying?'已复制（源与目标完整元数据清单已核对）':'已移动'})}
   catch(e){results.push({name:target.name,success:false,message:message(e)});if(copying||targets.some(t=>t.folder))stopMoving.value=true}
   finally{currentCopy.value=null;stoppingCopy.value=false;copyStopError.value=''}
   moveResults.value=[...results]
  }
  if(disposed||account.value?.id!==a.id||accountScope.value!==scope)return
  if(!copying&&targets.some(t=>t.folder))resetHistory()
  await load(p.currentId,crumbs)
  if(disposed||account.value?.id!==a.id||accountScope.value!==scope)return
  moveResults.value=results
  if(copying){
   const unsent=targets.filter((_,index)=>results[index]?.skipped)
   if(unsent.length)copyRetry.value={...retryScope,entries:unsent.map(entry=>({...entry}))}
  }
  if(!copying&&targets.some(t=>t.folder))emit('renamed',a.id)
  const successes=results.filter(r=>r.success).length,failures=results.filter(r=>!r.success&&!r.skipped).length,skipped=results.filter(r=>r.skipped).length
  notice.value=targets.length===1&&successes===1?`「${targets[0].name}」${verb}到：${destinationPath}${(native123Copy||nativeGuangyaCopy||nativeLanzouCopy)&&!directCopy?'下的新子目录，名称见本机复制记录':''}`:`${verb}结束：${successes} 个成功，${failures} 个未完成${skipped?`，${skipped} 个未执行`:''}。目标：${destinationPath}`
  if(failures)moveError.value='部分项目未完成或结果待确认，请查看逐项结果并刷新核对，程序未自动重试。'
 }finally{if(copying)copyHistoryVersion.value++;emit('mutation',false);if(!disposed){saving.value=false;moving.value=false}}
}
watch(()=>JSON.stringify(props.accounts.map(accountSource)),()=>{browseHistory.retain(props.accounts.map(accountSource));navigationVersion.value++},{immediate:true})
let lastLocationNonce=0
watch([accountScope,()=>props.refreshKey??0,()=>props.locationRequest?.nonce??0],([scope,revision],[previousScope,previousRevision])=>{
 if(trashTarget.value)closeTrash()
 if(shareTarget.value)closeShare()
 if(batchShareTarget.value)closeBatchShare()
 uploadFlow.cancel()
 displayKind.value='all'
 resetDrops(scope!==previousScope||revision!==previousRevision)
 request++
 page.value=null;query.value='';notice.value='';error.value=''
 createOpen.value=false;createName.value='';createError.value='';renameTarget.value=null;renameError.value='';moveTargets.value=[];moveError.value=''
 if(!account.value)return
 const location=props.locationRequest
 if(location&&location.accountId===account.value.id&&location.nonce!==lastLocationNonce){
  lastLocationNonce=location.nonce
  void loadFileLocation(location)
 }else{
  const last=browseHistory.current(scope)
  void load(last?.id??root.value,last?.breadcrumbs)
 }
},{immediate:true})
onUnmounted(()=>{if(typeof document!=='undefined'){document.removeEventListener('pointerdown',dismissEntryMenus);document.removeEventListener('pointerdown',dismissContext);document.removeEventListener('keydown',handleFileKey)};stopMeterClock();uploadFlow.cancel();disposed=true;request++;offDrop?.();offDownload?.();offBatch?.();offFolder?.();if(downloading.value)void api.CancelDownload().catch(()=>{});clearDrag();emit('mutation',false)})
</script>
<template>
<div class="cloud-files-view">
 <template v-if="view!=='downloads'">
  <div class="view-heading"><div><h1>{{account?cloudName(account.kind):'我的网盘'}}</h1></div><div v-if="account" class="heading-actions"><button class="secondary" :disabled="!canNavigate" @click="editCurrentAccount()"><AppIcon name="edit"/>编辑网盘</button><button v-if="canWebLogin" class="secondary" :disabled="!canNavigate" @click="editCurrentAccount(true)">重新登录</button></div></div>
  <section v-if="!account" class="cloud-empty"><h2>尚未添加网盘</h2><button class="primary" @click="emit('add')">添加网盘</button></section>
  <section v-else id="cloud-upload-dropzone" class="cloud-browser file-workbench" :data-file-drop-target="canDrop?'':undefined">
   <div class="file-commandbar"><div class="command-actions"><button class="primary" :disabled="!canDrop" @click="pickLocal"><AppIcon name="upload"/>上传文件</button><button class="secondary" title="新建文件夹（Ctrl+Shift+N）" :disabled="!canDrop" @click="openCreate"><AppIcon name="plus"/>新建文件夹</button><button v-if="canShare" class="secondary" :disabled="!canDrop" @click="openBatchShare()"><AppIcon name="share"/>批量分享…</button><button class="icon-button" title="刷新当前目录（F5）" aria-label="刷新当前目录" :disabled="busy||saving" @click="load(page?.currentId??root,page?.breadcrumbs??undefined)"><AppIcon name="refresh"/></button></div><label class="file-search"><AppIcon name="search"/><input v-model="query" :disabled="busy||saving||!page" aria-label="筛选当前目录的文件" placeholder="搜索当前目录"/></label></div>
   <div class="file-location"><div class="history-buttons"><button class="icon-button" aria-label="后退目录" title="后退（Alt+←）" :disabled="!canNavigate||!canGoBack" @click="goHistory(-1)"><AppIcon name="back"/></button><button class="icon-button" aria-label="前进目录" title="前进（Alt+→）" :disabled="!canNavigate||!canGoForward" @click="goHistory(1)"><AppIcon name="forward"/></button></div><button class="icon-button" title="返回上一级（Alt+↑）" aria-label="返回上一级" :disabled="!canNavigate||(page?.breadcrumbs?.length??0)<2" @click="goUp"><AppIcon name="up"/></button><nav class="folder-crumbs cloud-crumbs" aria-label="网盘当前目录"><button v-if="!page" :disabled="busy||saving" @click="load(root)">根目录</button><template v-for="(crumb,i) in page?.breadcrumbs??[]" :key="crumb.id"><AppIcon v-if="i" name="arrow"/><button :class="{'cloud-drop-hover':dragOver===crumb.id}" :disabled="busy||saving" @dragover="overDestination($event,crumb.id)" @dragleave="leaveDestination" @drop="dropDestination($event,crumb.id,crumbPath(i))" @click="go(crumb)">{{crumb.name}}</button></template></nav><details class="location-menu"><summary title="目录设置" aria-label="目录设置"><AppIcon name="settings"/></summary><div class="menu-surface"><p>默认上传目录</p><small>{{account.folderDisplay||account.folder}}</small><button :disabled="!canDrop" @click="save">将当前目录设为默认上传目录</button></div></details></div>
   <div class="file-context"><div v-if="selectedShareEntries.length" class="selection-actions"><strong>已选 {{selectedShareEntries.length}} 项<span v-if="selectedFolderIds.length">（含 {{selectedFolderIds.length}} 个文件夹）</span></strong><button v-if="canShare" class="secondary" :disabled="!canDrop" @click="openBatchShare(true)"><AppIcon name="share"/>分享…</button><button class="secondary" :disabled="!canDrop||!!selectedFolderIds.length" :title="selectedFolderIds.length?'混合选择可分享或移动；下载请单独操作文件夹':undefined" @click="downloadSelected"><AppIcon name="download"/>下载</button><button class="secondary" :disabled="!canDrop||!!selectedFolderIds.length" :title="selectedFolderIds.length?'混合选择可分享或移动；下载请单独操作文件夹':undefined" @click="queueSelected">加入下载队列</button><button class="secondary" :disabled="!canDrop||!canMoveSelection" title="最多50项；支持的网盘可混选文件和文件夹" @click="openSelectedMove"><AppIcon name="move"/>移动到…</button><button v-if="canCopy" class="secondary" :disabled="!canDrop||!canCopySelection" @click="openSelectedCopy"><AppIcon name="copy"/>复制到…</button><button v-if="canTrash" class="secondary" :disabled="!canDrop||!canTrashEntries(selectedShareEntries)" @click="openTrash(selectedShareEntries)">移入回收站…</button><button class="text-button" :disabled="!canDrop" @click="selectedIds=[];selectedFolderIds=[]">取消选择</button></div><div class="list-view-options"><div class="kind-filters" role="group" aria-label="显示项目类型"><button v-for="item in [{id:'all',label:'全部'},{id:'files',label:'文件'},{id:'folders',label:'文件夹'}]" :key="item.id" :aria-pressed="displayKind===item.id" :disabled="saving||busy" @click="displayKind=item.id as DisplayKind">{{item.label}}</button></div></div></div>
   <p v-if="error" class="banner error" role="alert">{{error}}<button :disabled="!canNavigate" @click="editCurrentAccount(canWebLogin)">{{canWebLogin?'重新登录':'修改连接'}}</button><button :disabled="busy||saving" @click="load(root)">返回根目录重试</button></p>
   <p v-if="notice&&notice!==downloadMessage" class="banner notice" role="status">{{notice}}<button v-if="notice.includes('上传任务')" class="text-button" @click="emit('uploads')">查看上传任务</button></p>
   <p v-if="moveError" class="banner error" role="alert">{{moveError}}<button :disabled="busy||saving" @click="load(page?.currentId??root,page?.breadcrumbs??undefined)">刷新当前目录</button></p>
   <button v-if="copyRetryEntries.length" class="secondary" :disabled="!canDrop||!canNavigate" @click="openCopyRetry">继续复制未提交项（{{copyRetryEntries.length}}）…</button>
   <div class="cloud-table-wrap" :aria-busy="busy" @contextmenu.prevent="openContext($event)" @scroll="closeContext"><table class="cloud-table cloud-has-download"><thead><tr><th scope="col" :aria-sort="sortState('name')"><div class="table-name-head"><input type="checkbox" aria-label="全选当前显示的项目" title="全选项目（Ctrl+A）" :checked="allSelected" :indeterminate="selectedShareEntries.length>0&&!allSelected" :disabled="!canDrop||!entries.length" @change="selectAll(($event.target as HTMLInputElement).checked)"/><button class="sort-header" :disabled="saving||busy" @click="setSort('name')">名称<span v-if="sortState('name')!=='none'">{{sortState('name')==='ascending'?'↑':'↓'}}</span></button></div></th><th scope="col" :aria-sort="sortState('type')"><button class="sort-header" :disabled="saving||busy" @click="setSort('type')">类型<span v-if="sortState('type')!=='none'">{{sortState('type')==='ascending'?'↑':'↓'}}</span></button></th><th scope="col" :aria-sort="sortState('size')"><button class="sort-header" :title="account.kind==='lanzou'?'按显示大小排序；蓝奏大小为近似值，未知大小排最后':'按大小排序；未知大小排最后'" :disabled="saving||busy" @click="setSort('size')">大小<span v-if="sortState('size')!=='none'">{{sortState('size')==='ascending'?'↑':'↓'}}</span></button></th><th scope="col">操作</th></tr></thead><tbody>
    <tr v-if="busy"><td colspan="4" class="cloud-table-state" role="status">正在读取文件…</td></tr><tr v-else-if="error"><td colspan="4" class="cloud-table-state">暂时无法读取，请刷新或重新登录网盘。</td></tr><tr v-else-if="!entries.length"><td colspan="4" class="cloud-table-state"><strong>{{hasListFilter?'没有匹配项':'文件夹为空'}}</strong><button v-if="hasListFilter" class="secondary clear-list-filter" :disabled="saving" @click="clearListFilters">清除筛选</button></td></tr>
    <tr v-for="f in busy||error?[]:entries" :key="(f.folder?'d:':'f:')+f.id" :class="{'cloud-drop-hover':f.folder&&dragOver===f.id,'cloud-row-selected':f.folder?selectedFolderIds.includes(f.id):selectedIds.includes(f.id)}" @contextmenu.stop.prevent="openContext($event,f)" @click="selectFile($event,f)" @dblclick="f.folder&&!saving&&go({id:f.id,name:f.name})" @dragover="f.folder&&overDestination($event,f.id)" @dragleave="leaveDestination" @drop="f.folder&&dropDestination($event,f.id,currentPath+' / '+f.name)"><td><div class="cloud-entry-cell"><input v-if="!f.folder" type="checkbox" :aria-label="'选择'+f.name" :checked="selectedIds.includes(f.id)" :disabled="!canDrop" @change="toggleSelection(f.id,($event.target as HTMLInputElement).checked)"/><input v-else type="checkbox" :aria-label="'选择文件夹'+f.name" :checked="selectedFolderIds.includes(f.id)" :disabled="!canDrop" @change="toggleFolderSelection(f.id,($event.target as HTMLInputElement).checked)"/><button v-if="f.folder" class="cloud-folder-link" :disabled="saving" :draggable="canDrop&&canMoveFolders" @dragstart="beginDrag($event,f)" @dragend="clearDrag" @click.stop="go({id:f.id,name:f.name})"><AppIcon name="files"/><span>{{f.name}}</span></button><span v-else class="cloud-file-name" :draggable="canDrop" @dragstart="beginDrag($event,f)" @dragend="clearDrag"><AppIcon name="file"/><span>{{f.name}}</span></span></div></td><td :title="fileType(f)">{{fileType(f)}}</td><td>{{size(f)}}</td><td><div class="entry-actions"><button v-if="f.folder?canDownloadFolders:canDownload" class="entry-download" :aria-label="'下载'+f.name" :disabled="saving||busy" @click="f.folder?downloadFolder(f):download(f)"><AppIcon name="download"/><span>下载</span></button><button v-if="f.folder||canShare||canCreate||canTrash||copyAllows([f])" class="icon-button entry-more" :disabled="!canNavigate" aria-haspopup="menu" :aria-expanded="!!contextState?.trigger&&contextState.entry===f" :aria-label="'更多操作：'+f.name" title="更多操作" @click.stop="openEntryActions($event,f)"><AppIcon name="more"/></button></div></td></tr>
   </tbody></table></div>
   <div class="cloud-summary"><span v-if="page&&!busy&&!error">{{folderCount}} 个文件夹，{{(page.entries?.length??0)-folderCount}} 个文件<span v-if="hasListFilter">，显示 {{entries.length}} 项</span></span><details class="file-help"><summary>快捷键</summary><p>Ctrl 多选，Shift 连选，Ctrl+A 全选，F2 重命名，Ctrl+Shift+N 新建文件夹，F5 刷新，Alt+↑ 上一级，Alt+←/→ 后退/前进，Shift+F10 打开操作菜单，Esc 取消选择。</p></details></div>
  </section>
  <div v-if="hasDownloadActivity" class="activity-dock" :class="{attention:!!downloadError}"><AppIcon name="download"/><div><strong>{{downloading?downloadName:downloadError?'下载有未完成项':downloadMessage||'下载结果已更新'}}</strong><small v-if="downloading">{{downloadRateLabel}} {{downloadRemainingLabel}}</small><small v-else>{{downloadItemCount}} 项结果，点击查看保存位置与校验详情</small><progress v-if="downloading&&downloadProgress.total>0" :max="downloadProgress.total" :value="downloadProgress.received"></progress></div><button class="secondary" @click="emit('downloads')">查看下载</button><button v-if="downloading" class="text-button" :disabled="downloadCancelling" @click="cancelDownload">{{downloadCancelling?'正在取消…':'取消'}}</button></div>
  <div v-if="moving" class="activity-dock"><AppIcon name="move"/><strong>正在按顺序{{fileOperation==='copy'?'复制':'移动'}}项目</strong><button v-if="currentCopy" class="secondary" :disabled="stoppingCopy" @click="stopCurrentCopy">停止当前及后续复制</button><button v-else class="secondary" :disabled="stopMoving" @click="stopMoving=true">停止后续{{fileOperation==='copy'?'复制':'移动'}}</button><span v-if="copyStopError" role="status">{{copyStopError}}</span></div>
  <details v-if="moveResults.length" class="move-history"><summary>最近文件操作结果（{{moveResults.length}}）</summary>  <section v-if="moveResults.length" class="cloud-move-results" aria-label="文件操作逐项结果"><strong>文件操作结果</strong><ul><li v-for="(result,i) in moveResults" :key="i" :class="{'move-result-error':!result.success&&!result.skipped}"><span>{{result.name}}</span><small>{{result.success?result.message:result.skipped?'未执行':'未完成 / 待核对'}}</small><p v-if="!result.success">{{result.message}}</p></li></ul></section>
</details>
 </template>
 <section v-else class="downloads-view">
  <div class="view-heading"><div><h1>下载结果 <span v-if="downloadItemCount" class="count-label">{{downloadItemCount}}</span></h1></div><button class="secondary" @click="emit('viewFiles')"><AppIcon name="files"/>返回文件</button></div>
  <p class="view-description">{{downloadOrigin?cloudName(downloadKind)+' / '+downloadOrigin:'从网盘文件中选择文件或文件夹开始下载。'}}</p>
  <div v-if="!hasDownloadActivity" class="task-empty"><AppIcon name="download"/><h2>还没有下载</h2><p>在文件列表中点击下载，完成后可在这里查看结果。</p><button class="primary" @click="emit('viewFiles')">浏览网盘文件</button></div>
    <section v-if="downloading" class="cloud-create cloud-download-progress" role="status"><strong>正在下载：{{downloadName}}</strong><p>{{downloadBytes(downloadProgress.received)}} / {{downloadBytes(downloadProgress.total)}} · 已有文件不会覆盖。</p><p>{{downloadRateLabel}}<span v-if="downloadRemainingLabel"> · {{downloadRemainingLabel}}</span></p><progress v-if="downloadProgress.total>0" aria-label="下载进度" :max="downloadProgress.total" :value="downloadProgress.received"></progress><div class="modal-actions"><button class="secondary" :disabled="downloadCancelling" @click="cancelDownload">{{downloadCancelling?'正在取消…':'取消下载'}}</button></div></section>

  <p v-if="downloadError" class="banner error" role="alert">{{downloadError}}</p><p v-else-if="downloadWarning" class="banner download-warning" role="status">{{downloadWarning}}</p><p v-else-if="downloadMessage" class="banner notice" role="status">{{downloadMessage}}</p>
  <section v-if="currentResults.length" class="download-result-filters" aria-label="筛选下载结果">
   <div class="result-filter-buttons" role="group" aria-label="下载结果状态"><button v-for="filter in resultFilters" :key="filter.value" class="secondary" :class="{active:resultFilter===filter.value}" :aria-pressed="resultFilter===filter.value" @click="resultFilter=filter.value">{{filter.label}} <span>{{outcomeCounts[filter.value]}}</span></button></div>
   <input v-model="resultQuery" type="search" aria-label="搜索下载结果文件名" placeholder="搜索下载结果中的文件名或相对路径"/>
   <div class="result-filter-summary"><span role="status">显示 {{shownResultCount}} / {{currentResults.length}} 项{{downloading?'（结果持续更新）':''}}</span><button v-if="resultFilter!=='all'||resultQuery" class="secondary" @click="clearResultFilters">清除筛选</button></div>
   <p>未验证的文件已保存；筛选不改变重试范围，复制下载结果始终包含全部项目。</p>
   <p v-if="!shownResultCount" class="result-no-match">当前筛选没有匹配的下载结果。</p>
  </section>
  <section v-if="singleResult" class="single-download-result"><AppIcon name="file"/><div><strong>{{singleResult.verification==='verified'?'已保存，校验通过':'已保存，完整性未验证'}}</strong><p>{{singleResult.path}}</p><div class="saved-location-actions"><button class="secondary" :disabled="saving||locationBusy" @click="openSavedFolder(singleResult.path)">打开保存文件夹</button><button class="secondary" :disabled="saving||locationBusy" @click="copySavedPath(singleResult.path)">复制路径</button></div><details v-if="downloadDetails"><summary>校验详情</summary><p>{{downloadDetails}}</p></details></div></section>
    <section v-if="batchSources.length" class="cloud-move-results" aria-label="批量下载逐项结果"><strong>下载结果</strong><button v-if="batchPendingIndices.length" class="secondary" :disabled="!canDrop" @click="retryBatch">重试未完成（{{batchPendingIndices.length}}）…</button><p v-if="batchPendingIndices.length">只重试未保存的文件；选择保存目录后重新下载，已有文件不会覆盖。</p><ul><li v-for="(item,i) in filteredBatchResults" :key="i" :class="{'move-result-error':item.status==='failed','download-result-neutral':item.status!=='failed'&&(item.status!=='saved'||item.result.verification!=='verified')}"><span>{{item.name}}</span><small>{{batchStatus(item)}}</small><p v-if="item.message">{{item.message}}</p><p v-if="item.result.path">{{item.result.path}}</p><template v-if="item.status==='saved'&&item.result.path"><div class="saved-location-actions"><button class="secondary" :disabled="saving||locationBusy" @click="openSavedFolder(item.result.path)">打开保存文件夹</button><button class="secondary" :disabled="saving||locationBusy" @click="copySavedPath(item.result.path)">复制路径</button></div></template><details v-if="item.result.details"><summary>校验详情</summary>{{item.result.details}}</details></li></ul></section>
  <section v-if="folderDirectory||folderVisibleResults.length" class="cloud-move-results" aria-label="文件夹下载结果"><strong>文件夹下载结果</strong><button v-if="folderRetryId&&folderPending" class="secondary" :disabled="!canDrop" @click="retryFolder">重试未完成（{{folderPending}}）</button><p v-if="folderRetryId&&folderPending">继续保存到原文件夹，只重试未保存项；已保存文件不会重新下载，同名项目不会覆盖。切换网盘、开始新下载或退出后不再保留此结果。</p><p v-if="folderDirectory">保存位置：{{folderDirectory}}</p><template v-if="folderDirectory"><div class="saved-location-actions"><button class="secondary" :disabled="saving||locationBusy" @click="openSavedFolder(folderDirectory)">打开保存文件夹</button><button class="secondary" :disabled="saving||locationBusy" @click="copySavedPath(folderDirectory)">复制路径</button></div></template><ul><li v-for="(item,i) in filteredFolderResults" :key="i" :class="{'move-result-error':item.status==='failed','download-result-neutral':item.status!=='failed'&&(item.status!=='saved'||item.result.verification!=='verified')}"><span>{{item.name}}</span><small>{{batchStatus(item)}}</small><p v-if="item.message">{{item.message}}</p><p v-if="item.result.path">{{item.result.path}}</p><template v-if="item.status==='saved'&&item.result.path"><div class="saved-location-actions"><button class="secondary" :disabled="saving||locationBusy" @click="openSavedFolder(item.result.path)">打开保存文件夹</button><button class="secondary" :disabled="saving||locationBusy" @click="copySavedPath(item.result.path)">复制路径</button></div></template><details v-if="item.result.details"><summary>校验详情</summary>{{item.result.details}}</details></li></ul></section>
  <p v-if="locationStatus" role="status">{{locationStatus}}</p><p v-if="locationError" class="error" role="alert">{{locationError}}</p>
  <div v-if="downloadError||downloadDetails||downloadWarning||batchVisibleResults.length||folderDirectory" class="download-details"><button class="secondary" :disabled="saving||copyingReport" @click="copyReport">{{copyingReport?'正在复制…':'复制下载结果'}}</button><span role="status"> {{copyStatus}}</span></div>

  <p v-if="hasDownloadActivity" class="view-footnote">这里保留当前网盘最近一次下载。切换页面或浏览目录不会清空；切换网盘、开始新下载或退出后更新。</p>
 </section>

 <div v-if="contextState" ref="contextMenu" class="file-context-menu" role="menu" :aria-label="contextState.entry?'文件操作':'当前目录操作'" :style="{left:contextState.x+'px',top:contextState.y+'px'}" @keydown="contextKeys" @contextmenu.prevent>
  <template v-if="contextState.entry">
   <span class="context-caption">{{contextShareGroup?'已选 '+selectedShareEntries.length+' 项':contextState.entry.name}}</span>
   <button v-if="contextState.entry.folder" role="menuitem" @click="contextAction('open')"><AppIcon name="files"/>打开文件夹</button>
   <button v-if="contextState.entry.folder&&canDownloadFolders&&!contextShareGroup" role="menuitem" @click="contextAction('queue')">文件夹加入下载队列</button><button v-if="contextState.entry.folder?canDownloadFolders:canDownload" role="menuitem" :disabled="contextShareGroup&&!!selectedFolderIds.length" @click="contextAction('download')"><AppIcon name="download"/>{{contextGroup?'下载所选（'+selectedEntries.length+'）':contextState.entry.folder?'下载文件夹':'下载'}}</button>
   <button v-if="canShare" role="menuitem" :disabled="!canDrop" @click="contextShareGroup?openBatchShare():contextAction('share')"><AppIcon name="share"/>分享链接</button><button v-if="canCreate&&!contextShareGroup" role="menuitem" @click="contextAction('rename')"><AppIcon name="edit"/>重命名<kbd>F2</kbd></button>
   <button v-if="canCopy" role="menuitem" :disabled="!canDrop||(contextShareGroup?!canCopySelection:!copyAllows([contextState.entry]))" @click="contextCopy"><AppIcon name="copy"/>{{contextShareGroup?'复制所选（'+selectedShareEntries.length+'）':'复制到…'}}</button><button v-if="canCreate" role="menuitem" :disabled="contextShareGroup?!canMoveSelection:contextState.entry.folder&&!canMoveFolders" :title="contextState.entry.folder&&!canMoveFolders?'此网盘暂不支持文件夹移动':undefined" @click="contextAction('move')"><AppIcon name="move"/>{{contextShareGroup?'移动所选（'+selectedShareEntries.length+'）':'移动到…'}}</button>
   <button v-if="canTrash" role="menuitem" :disabled="!canDrop||!canTrashEntries(contextShareGroup?selectedShareEntries:contextState?.entry?[contextState.entry]:[])" @click="contextTrash">移入回收站…</button>
   <button v-if="canCreate" role="menuitem" :disabled="!canDrop" title="在当前浏览的目录中新建文件夹" @click="contextAction('create')"><AppIcon name="plus"/>新建文件夹</button>
  </template>
  <template v-else><span class="context-caption">当前目录</span><button role="menuitem" :disabled="!canDrop" @click="contextAction('upload')"><AppIcon name="upload"/>上传文件</button><button role="menuitem" :disabled="!canDrop" @click="contextAction('create')"><AppIcon name="plus"/>新建文件夹</button><button role="menuitem" @click="contextAction('refresh')"><AppIcon name="refresh"/>刷新<kbd>F5</kbd></button><button role="menuitem" :disabled="!canDrop||!selectableEntries.length" @click="contextAction('all')"><AppIcon name="check"/>全选文件<kbd>Ctrl+A</kbd></button></template>
 </div>
 <BatchShareDialog v-if="batchShareTarget" v-bind="batchShareTarget" @receipt="openShareReceipt($event,true)" @close="closeBatchShare"/>
 <ShareDialog v-if="shareTarget" :account-id="shareTarget.accountId" :parent-id="shareTarget.parentId" :kind="shareTarget.kind" :entry="shareTarget.entry" :recreation="shareTarget.recreation" :group="shareTarget.group" :account-name="shareTarget.recreation?account?.name:undefined" :source-path="shareTarget.recreation?currentPath:undefined" show-receipt-link @receipt="openShareReceipt($event)" @close="closeShare"/>
 <div v-if="createOpen" class="modal-backdrop" @click.self="closeCreate"><section class="operation-dialog" role="dialog" aria-modal="true" @keydown.tab="trapDialogFocus" aria-labelledby="create-title"><div class="modal-heading"><h2 id="create-title">新建文件夹</h2><button class="icon-button" :disabled="saving" aria-label="关闭" @click="closeCreate"><AppIcon name="close"/></button></div><p class="dialog-location">{{cloudName(account?.kind??'')}} / {{currentPath}}</p>  <form v-if="createOpen" class="cloud-create" @submit.prevent="create" @keydown.esc.prevent="closeCreate"><label for="new-folder-name">新文件夹名称</label><div class="cloud-create-fields"><input id="new-folder-name" ref="nameInput" v-model="createName" required maxlength="50" :disabled="saving" autocomplete="off" placeholder="例如：资料备份" aria-describedby="folder-name-help"/><button type="submit" class="primary" :disabled="saving||!createName">{{creating?'正在创建…':'创建'}}</button><button type="button" class="secondary" :disabled="saving" @click="closeCreate">取消</button></div><p id="folder-name-help">将在当前目录创建。最多 50 个字符。<template v-if="account.kind==='lanzou'">空格和英文括号请用下划线代替。</template></p><p v-if="createError" class="form-error" role="alert">{{createError}}</p></form>
</section></div>
 <div v-if="renameTarget" class="modal-backdrop" @click.self="closeRename"><section class="operation-dialog" role="dialog" aria-modal="true" @keydown.tab="trapDialogFocus" aria-labelledby="rename-title"><div class="modal-heading"><h2 id="rename-title">重命名</h2><button class="icon-button" :disabled="saving" aria-label="关闭" @click="closeRename"><AppIcon name="close"/></button></div>  <form v-if="renameTarget" class="cloud-create" @submit.prevent="rename" @keydown.esc.prevent="closeRename"><label for="rename-entry-name">重命名{{renameTarget.folder?'文件夹':'文件'}}：{{renameTarget.name}}</label><div class="cloud-create-fields"><input id="rename-entry-name" ref="renameInput" v-model="renameName" required maxlength="50" :disabled="saving" autocomplete="off" aria-label="新名称"/><button type="submit" class="primary" :disabled="saving||!renameName||renameName===renameTarget.name">{{renaming?'正在重命名…':'保存名称'}}</button><button type="button" class="secondary" :disabled="saving" @click="closeRename">取消</button></div><p>最多 50 个字符，已有同名项目时不修改。<template v-if="account.kind==='lanzou'">名称不能包含空格和英文括号；文件需保留原扩展名，能否改名以账号权限为准。</template></p><p v-if="renameError" class="form-error" role="alert">{{renameError}}</p></form>
</section></div>
 <div v-if="localFiles.length&&!uploadFlow.pending.value" @keydown.esc.prevent="!uploading&&(localFiles=[])" class="modal-backdrop" @click.self="!uploading&&(localFiles=[])"><section class="operation-dialog" role="dialog" aria-modal="true" @keydown.tab="trapDialogFocus" aria-labelledby="upload-current-title"><div class="modal-heading"><h2 id="upload-current-title">上传文件</h2><button class="icon-button" :disabled="uploading" aria-label="关闭上传确认" @click="localFiles=[]"><AppIcon name="close"/></button></div>  <section v-if="localFiles.length" class="cloud-create cloud-upload-review" aria-label="确认上传到当前目录"><strong>上传到：{{cloudName(account.kind)}} / {{currentPath}}</strong><p>共 {{localFiles.length}} 个本地项目，点击开始上传后创建任务。不会改变默认上传位置。</p><ul><li v-for="f in localFiles" :key="uploadSourceKey(f)">{{f.relativePath||f.name}}{{f.directory?'（目录）':''}}</li></ul><div class="modal-actions"><button class="secondary" :disabled="saving" @click="localFiles=[];uploadError=''">取消</button><button class="primary" :disabled="!canDrop" @click="uploadLocal">{{uploading?'正在创建任务…':'开始上传到此目录'}}</button></div></section>
<p v-if="uploadError" class="banner error" role="alert">{{uploadError}}</p></section></div>
  <UploadReview v-if="uploadFlow.pending.value" :report="uploadFlow.pending.value.report" :checking="uploadFlow.checking.value" :submitting="uploadFlow.submitting.value" :error="uploadFlow.error.value" @cancel="uploadFlow.cancel" @check="uploadFlow.check" @submit="uploadFlow.submit"/>
  <TrashDialog v-if="trashTarget&&account" :inputs="trashTarget" :account-name="trashAccountName" :location="trashLocation" :browser-restore="account.kind==='lanzou'" @close="closeTrash" @changed="trashChanged"/>
  <details v-if="account&&(canTrash||canCopy)" class="operation-details file-histories">
   <summary>文件操作记录</summary>
   <TrashHistory v-if="canTrash&&account" :account-id="account.id" :refresh-key="accountScope+String(props.refreshKey??'')+String(trashHistoryVersion)" :blocked="saving||busy||!!trashTarget||props.blocked" @mutation="trashHistoryBusy=$event;emit('mutation',$event)"/>
   <CopyHistory v-if="canCopy&&account&&account.kind!=='quark'" :account-id="account.id" :refresh-key="accountScope+String(props.refreshKey??'')+String(copyHistoryVersion)" :blocked="saving||busy||props.blocked" @open="openCopyDestination" @mutation="copyHistoryBusy=$event;emit('mutation',$event)"/>
  </details>
  <FolderPicker v-if="moveTargets.length&&account&&page" :account-id="account.id" :account-name="cloudName(account.kind)" :kind="account.kind" :initial-folder="page.currentId" :initial-breadcrumbs="page.breadcrumbs??[]" :purpose="fileOperation" :source-names="moveTargets.map(f=>f.name)" :forbidden-roots="moveTargets.filter(f=>f.folder).map(f=>f.id)" :source-name="moveTargets.length===1?moveTargets[0].name:moveTargets.length+' 个项目（含 '+moveTargets.filter(f=>f.folder).length+' 个文件夹）'" :direct-copy="fileOperation==='copy'&&(account.kind==='pan123'||account.kind==='guangya')" :snapshot-copy="fileOperation==='copy'&&account.kind==='guangya'&&moveTargets.some(target=>!target.folder&&(target.size??0)>guangyaNativeCopyMaxFile)" :snapshot-only="fileOperation==='copy'&&account.kind==='guangya'&&moveTargets.some(target=>!target.folder&&(target.size??0)>guangyaNativeCopyMaxFile)" @close="moveTargets=[]" @selected="move"/>

</div>
</template>
