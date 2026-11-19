<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch,nextTick} from 'vue'
import {Browser} from '@wailsio/runtime'
import AppIcon from './AppIcon.vue'
import BatchOperationReport from './BatchOperationReport.vue'
import AccountSearch,{type AccountSearchLocation,type AccountSearchSource} from './AccountSearch.vue'
import RecyclePurgeDialog from './RecyclePurgeDialog.vue'
import RecycleEmptyDialog from './RecycleEmptyDialog.vue'
import RecyclePurgeHistory from './RecyclePurgeHistory.vue'
import RecycleFolderPreview from './RecycleFolderPreview.vue'
import RestoredBatchMove from './RestoredBatchMove.vue'
import RestoredFolderMove from './RestoredFolderMove.vue'
import FolderPicker from './FolderPicker.vue'
import {trapDialogFocus} from './dialogFocus'
import {createPageSearch} from './pageSearch'
import {shareAccountLabel} from './shareAccounts'
import {spreadsheetCell} from './spreadsheetCell'
import * as api from '../bindings/multipan/desktopservice'
import type {Account,CloudFolder,GuangyaRestorePreview,LanzouRecycleRestorePreview,LanzouRecycleCoverage,LanzouRecycleCoverageRow,RecycleEntry,RecyclePurgeSelection,RecycleRestoreLocation,RecycleRestoreResult} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accounts:Account[];blocked?:boolean;refreshKey?:string;reviewTarget?:{kind:string;accountId:string;receiptId:string}|null}>()
const emit=defineEmits<{mutation:[active:boolean];'open-location':[location:RecycleRestoreLocation,move:boolean];'folder-moved':[accountId:string]}>()
const receipts=ref<RecycleRestoreResult[]>([]),confirmTarget=ref<RecycleEntry|null>(null),message=ref(''),dialog=ref<HTMLElement|null>(null)
const choosingRestoreFolder=ref(false)
const restoreDestination=ref<{path:string;trail:CloudFolder[]}|null>(null)
const restoreOrigin=ref<GuangyaRestorePreview|null>(null),restoreOriginError=ref('')
const lanzouRestorePreview=ref<LanzouRecycleRestorePreview|null>(null)
const focusedReview=ref(props.reviewTarget??null)
const visibleReceipts=computed(()=>focusedReview.value?receipts.value.filter(row=>row.id===focusedReview.value?.receiptId):receipts.value)
const restoredMoveSelection=ref<string[]>([])
const restoredMoveTargets=computed(()=>receipts.value.filter(row=>row.canMove&&restoredMoveSelection.value.includes(row.id)))
const restoredMoveBatch=ref<{accountId:string;accountName:string;kind:string;targets:{id:string;name:string}[];checkOnly:boolean}|null>(null)
const restoredFolderMove=ref<{accountId:string;accountName:string;kind:string;receiptId:string;entryId:string;parentId:string;name:string;checkOnly?:boolean}|null>(null)
let restoredFolderTrigger:HTMLElement|null=null
async function closeRestoredFolder(moved:boolean){
 const target=restoredFolderMove.value
 restoredFolderMove.value=null
 emit('mutation',false)
 if(moved&&target)emit('folder-moved',target.accountId)
 await nextTick()
 restoredFolderTrigger?.focus()
}
let restoredMoveTrigger:HTMLElement|null=null
function toggleRestoredMove(row:RecycleRestoreResult,checked:boolean){
 if(locked.value||props.blocked||!row.canMove)return
 if(!checked)restoredMoveSelection.value=restoredMoveSelection.value.filter(id=>id!==row.id)
 else if(restoredMoveSelection.value.length<20&&!restoredMoveSelection.value.includes(row.id))restoredMoveSelection.value.push(row.id)
}
function openRestoredMoveBatch(event:Event){
 showRestoredMoveBatch(restoredMoveTargets.value,event.currentTarget as HTMLElement)
}
function showRestoredMoveBatch(rows:RecycleRestoreResult[],trigger:HTMLElement|null,checkOnly=false){
 const account=activeAccount.value
 if(locked.value||props.blocked||focusedReview.value&&!checkOnly||!canMoveRestoredFiles.value||!account||
    !rows.length||rows.length>20||rows.some(row=>!row.canMove))return
 restoredMoveTrigger=trigger
 restoredMoveBatch.value={accountId:account.id,accountName:operationAccountLabel.value,kind:account.kind,
  targets:rows.map(({id,name})=>({id,name})),checkOnly}
 emit('mutation',true)
}
async function closeRestoredMoveBatch(){
 restoredMoveBatch.value=null
 restoredMoveSelection.value=[]
 emit('mutation',false)
 await nextTick()
 restoredMoveTrigger?.focus()
}
const locating=ref(false)
const activeAccount=computed(()=>accounts.value.find(a=>a.id===accountId.value))
const isLanzou=computed(()=>activeAccount.value?.kind==='lanzou')
const canMoveRestoredFiles=computed(()=>['pan123','quark','baidu','guangya'].includes(activeAccount.value?.kind??''))
const isGuangya=computed(()=>activeAccount.value?.kind==='guangya')
const canEmpty=computed(()=>['baidu','pan123','quark','guangya'].includes(activeAccount.value?.kind??''))
const canPurge=computed(()=>canEmpty.value||isLanzou.value)
const canRestore=canPurge
const openingLanzou=ref(false)
const batchConfirm=ref<RecycleEntry[]>([]),selected=ref<string[]>([]),stopRequested=ref(false),batchRunning=ref(false)
const restoreQueue=ref<RecyclePurgeSelection[]>([]),purgeQueue=ref<RecyclePurgeSelection[]>([])
type BatchRow={id:string;name:string;path:string;state:string;message:string}
const batchResults=ref<BatchRow[]>([])
const batchPhase=ref('')
const batchPlan=ref<{passed:boolean;message:string}|null>(null)
const batchReportOperation=ref('')
const selectionMode=ref<'restore'|'purge'>('restore'),purgeTarget=ref<{accountId:string;accountName:string;page:number;entries:RecycleEntry[];selections?:RecyclePurgeSelection[];autoPreview?:boolean}|null>(null),purgeHistoryVersion=ref(0),purgeHistoryBusy=ref(false)
const emptyTarget=ref<{accountId:string;accountName:string}|null>(null)
const folderPreview=ref<Pick<RecycleEntry,'id'|'name'>|null>(null)
let folderPreviewTrigger:HTMLElement|null=null
function openFolderPreview(row:Pick<RecycleEntry,'id'|'name'>,event:Event){
 if(!isLanzou.value||!row.id.startsWith('folder:')||locked.value||props.blocked||error.value)return
 folderPreviewTrigger=event.currentTarget as HTMLElement
 folderPreview.value=row
 emit('mutation',true)
}
async function closeFolderPreview(){
 folderPreview.value=null
 emit('mutation',false)
 await nextTick()
 folderPreviewTrigger?.focus()
}
function prepareEmpty(){if(!canEmpty.value||locked.value||props.blocked||!accountId.value||error.value||confirmedEmpty.value||listBrowseOnly.value)return;emptyTarget.value={accountId:accountId.value,accountName:operationAccountLabel.value};emit('mutation',true)}
async function closeEmpty(){emptyTarget.value=null;await closePurge()}
let purgeRefreshPending=false
let previewPurgeTrigger:HTMLElement|null=null
const locked=computed(()=>busy.value||purgeHistoryBusy.value||!!emptyTarget.value||!!purgeTarget.value||!!confirmTarget.value||!!folderPreview.value||!!restoredMoveBatch.value||!!restoredFolderMove.value||batchConfirm.value.length>0)
function canPurgeRow(row:RecycleEntry){
 return !listBrowseOnly.value&&canPurge.value&&['file','folder'].includes(row.kind)&&(!isLanzou.value||!!row.restoreHandle&&!!row.parentId&&!receiptFor(row))
}
const eligible=computed(()=>canRestore.value&&!listBrowseOnly.value?rows.value.filter(r=>selectionMode.value==='purge'?canPurgeRow(r):r.restoreHandle&&!receiptFor(r)):[])
function preparePurge(entries:RecycleEntry[]){if(!canPurge.value||locked.value||props.blocked||error.value||!accountId.value||!entries.length||entries.length>50||entries.some(r=>!canPurgeRow(r)))return;purgeTarget.value={accountId:accountId.value,accountName:operationAccountLabel.value,page:page.value,entries:entries.map(r=>({...r}))};emit('mutation',true)}
function canPreviewLanzouRowPurge(row:RecycleEntry){
 return isLanzou.value&&!listBrowseOnly.value&&selectionMode.value==='purge'&&!row.parentId&&!row.originalPath&&
  (row.kind==='file'||row.kind==='folder')&&row.id.startsWith(row.kind+':')&&!receiptFor(row)
}
function previewLanzouRowPurge(row:RecycleEntry,event?:Event){
 if(disposed||locked.value||props.blocked||error.value||!accountId.value||!rows.value.includes(row)||!canPreviewLanzouRowPurge(row))return
 previewPurgeTrigger=event&&event.currentTarget instanceof HTMLElement?event.currentTarget:null
 purgeTarget.value={accountId:accountId.value,accountName:operationAccountLabel.value,page:0,entries:[{...row,restoreHandle:''}],autoPreview:true}
 emit('mutation',true)
}
async function closePurge(){
 const trigger=previewPurgeTrigger
 previewPurgeTrigger=null
 purgeTarget.value=null
 emit('mutation',false)
 if(purgeRefreshPending&&!disposed){purgeRefreshPending=false;await load(page.value)}
 if(trigger){await nextTick();if(!disposed&&trigger.isConnected)trigger.focus()}
}
function clearQueues(){restoreQueue.value=[];purgeQueue.value=[]}
function purgeChanged(){clearSearch();closeCoverage();clearQueues();purgeHistoryVersion.value++;selected.value=[];purgeRefreshPending=true}
const selectedRows=computed(()=>eligible.value.filter(r=>selected.value.includes(r.id)))
const checkable=computed(()=>receipts.value.filter(r=>r.canCheck))
function restorePath(row:RecycleEntry){return isLanzou.value&&row.kind==='file'?'根目录 / '+row.name:row.originalPath}
const batchDone=computed(()=>batchResults.value.filter(r=>!['queued','working','skipped'].includes(r.state)).length)
function toggleSelected(row:RecycleEntry){if(locked.value||props.blocked||error.value||!eligible.value.some(r=>r.id===row.id))return;selected.value=selected.value.includes(row.id)?selected.value.filter(id=>id!==row.id):selected.value.length<50?[...selected.value,row.id]:selected.value}
function selectVisible(){if(locked.value||props.blocked||error.value)return;selected.value=eligible.value.slice(0,50).map(r=>r.id)}
const activeQueue=computed({
 get:()=>selectionMode.value==='restore'?restoreQueue.value:purgeQueue.value,
 set:(rows:RecyclePurgeSelection[])=>{
  if(selectionMode.value==='restore')restoreQueue.value=rows
  else purgeQueue.value=rows
 },
})
const canQueueSelection=computed(()=>selectedRows.value.length>0&&
 (selectionMode.value==='restore'||canEmpty.value&&page.value<=50)&&
 new Set([...activeQueue.value.map(row=>row.entry.id),...selectedRows.value.map(row=>row.id)]).size<=50)
function addToQueue(selections:RecyclePurgeSelection[]){
 const entries=new Map(activeQueue.value.map(row=>[row.entry.id,row]))
 for(const {page,entry} of selections)entries.set(entry.id,{page,entry:{...entry}})
 activeQueue.value=[...entries.values()]
}
function queueSelected(){
 if(locked.value||props.blocked||error.value||!canQueueSelection.value)return
 addToQueue(selectedRows.value.map(entry=>({page:page.value,entry})))
 selected.value=[]
}
function removeQueuedEntry(id:string){
 if(locked.value||props.blocked)return
 activeQueue.value=activeQueue.value.filter(row=>row.entry.id!==id)
}
function prepareQueuedPurge(){
 if(listBrowseOnly.value)return
 if(selectionMode.value!=='purge'||!canEmpty.value||locked.value||props.blocked||error.value||!accountId.value||!purgeQueue.value.length)return
 const selections=purgeQueue.value.map(row=>({page:row.page,entry:{...row.entry}}))
 purgeTarget.value={accountId:accountId.value,accountName:operationAccountLabel.value,page:page.value,entries:selections.map(row=>row.entry),selections}
 emit('mutation',true)
}
async function prepareBatch(event?:Event,queued=false){
 if(listBrowseOnly.value||!canRestore.value||selectionMode.value!=='restore'||locked.value||props.blocked||error.value)return
 const entries=queued
  ?[...restoreQueue.value].sort((a,b)=>b.page-a.page).map(row=>row.entry)
  :selectedRows.value
 if(!entries.length)return
 restoreTrigger=(event?.currentTarget as HTMLElement|undefined)??null
 batchPlan.value=null
 batchConfirm.value=entries.map(entry=>({...entry}))
 emit('mutation',true)
 await nextTick()
 dialog.value?.focus()
}
async function cancelBatch(){if(busy.value)return;batchConfirm.value=[];useOriginalRestoreDestination();choosingRestoreFolder.value=false;emit('mutation',false);await nextTick();restoreTrigger?.focus()}
function validateBatchRestore(handles:string[],destination:typeof restoreDestination.value){
 return destination?api.ValidateCloudRecycleRestoreBatchTo(handles,destination.trail):api.ValidateCloudRecycleRestoreBatch(handles)
}
async function checkBatchPlan(){
 if(disposed||busy.value||props.blocked||choosingRestoreFolder.value||!batchConfirm.value.length)return
 const stamp=generation
 batchPlan.value=null
 busy.value=true
 try{
  await validateBatchRestore(batchConfirm.value.map(entry=>entry.restoreHandle),restoreDestination.value)
  if(!disposed&&stamp===generation)batchPlan.value={passed:true,message:'本次预检通过。确认还原时会再次核对。'}
 }catch(e){
  if(!disposed&&stamp===generation)batchPlan.value={passed:false,message:'预检未通过，尚未提交还原。'+String(e)}
 }finally{
  busy.value=false
  if(reload&&!disposed){
   reload=false
   emit('mutation',false)
   await load(1)
  }
 }
}
function batchLabel(state:string){return ({queued:'未开始',working:'处理中',confirmed:'已还原',unknown:'待确认',not_sent:'未提交',skipped:'未开始',checked:'已核对'} as Record<string,string>)[state]??state}
const canContinueBatchToMove=computed(()=>canMoveRestoredFiles.value&&batchConfirm.value.length>0&&batchConfirm.value.length<=20&&
 batchConfirm.value.every(row=>row.kind==='file'&&canRestoreThenMove(row)))
async function runBatch(checkOnly=false,move=false){
 if(busy.value||props.blocked||choosingRestoreFolder.value||(!checkOnly&&!batchConfirm.value.length)||(checkOnly&&locked.value))return
 const destination=checkOnly?null:restoreDestination.value
 if(move&&(checkOnly||destination||!canContinueBatchToMove.value))return
 if(destination&&!canChooseRestoreFolder.value)return
 const targets=checkOnly
  ?checkable.value.map(r=>({id:r.id,name:r.name,path:'',restoreHandle:''}))
  :batchConfirm.value.map(r=>({id:r.id,name:r.name,path:destination?destination.path+' / '+r.name:restorePath(r),restoreHandle:r.restoreHandle}))
 if(!targets.length)return
 const stamp=generation,id=accountId.value,trigger=restoreTrigger
 const confirmedIds:string[]=[]
 batchConfirm.value=[];useOriginalRestoreDestination();stopRequested.value=false;batchRunning.value=true;busy.value=true;emit('mutation',true)
 batchReportOperation.value=checkOnly?'批量核对还原记录':'批量还原'
 batchResults.value=targets.map(r=>({id:r.id,name:r.name,path:r.path,state:'queued',message:''}))
 let preflightError='',submitted=false
 try{
  if(!checkOnly){batchPhase.value='正在检查整批原记录和还原位置，尚未提交还原…';await validateBatchRestore(targets.map(r=>r.restoreHandle),destination)}
  batchPhase.value=checkOnly?'正在只读核对原结果…':'正在逐项还原并核对…'
  for(let i=0;i<targets.length;i++){
   if(stopRequested.value||disposed||stamp!==generation||props.blocked)break
   if(!checkOnly&&!submitted){
    clearSearch()
    closeCoverage()
    clearQueues()
    selected.value=[]
    submitted=true
   }
   const row=batchResults.value[i];row.state='working'
   try{
    const handle=targets[i].restoreHandle
    const result=checkOnly?await api.CheckCloudRecycleRestore(targets[i].id)
     :destination?await api.RestoreCloudRecycleTo(handle,destination.trail):await api.RestoreCloudRecycle(handle)
    row.state=result.state;row.message=result.message
    if(move&&result.state==='confirmed')confirmedIds.push(result.id)
    // Unknown writes must never be retried or hidden by further submissions.
    if(!checkOnly&&result.state!=='confirmed'){stopRequested.value=true;break}
   }catch(e){row.state='unknown';row.message='调用中断，请检查本机还原记录：'+String(e);stopRequested.value=true;break}
  }
 }catch(e){preflightError='整批预检查未通过，没有提交本批还原：'+String(e)}finally{
  for(const row of batchResults.value)if(row.state==='queued'){
   row.state='skipped'
   row.message=checkOnly?'本次未核对':submitted?'未发送请求，可刷新列表后重新选择':'未发送请求，可调整清单后重新预检'
  }
  if(!disposed&&stamp===generation)message.value=preflightError||(checkOnly?'原还原结果核对结束；未发送新的还原请求':submitted?'批量还原结束；未确认项目请先检查原结果，未开始项目不会自动续跑':'本批未提交还原，已保留清单和所选项目。')
  if(stamp!==generation||disposed)batchResults.value=[]
  batchPhase.value='';batchRunning.value=false;busy.value=false;emit('mutation',false)
  if(!disposed&&(checkOnly||submitted||reload||stamp!==generation)){reload=false;await load(stamp===generation?page.value:1)}
 }
 if(move&&!disposed&&!stopRequested.value&&stamp===generation&&id===accountId.value&&confirmedIds.length===targets.length){
  const restored=receipts.value.filter(row=>row.canMove&&confirmedIds.includes(row.id))
  if(restored.length===targets.length)showRestoredMoveBatch(restored,trigger)
  else message.value='还原已确认，请刷新本机还原记录后选择要移动的文件。'
 }
}
const canClear=computed(()=>receipts.value.some(r=>['confirmed','not_sent'].includes(r.state)))
function receiptFor(row:RecycleEntry){return receipts.value.find(r=>r.entryId===row.id)}
function stateLabel(state:string){return ({working:'处理中',unknown:'结果待确认',confirmed:'已还原',not_sent:'未提交'} as Record<string,string>)[state]??state}
let restoreTrigger:HTMLElement|null=null
async function prepareRestore(row:RecycleEntry,event?:Event){if(!canRestore.value||locked.value||props.blocked||error.value||!row.restoreHandle||receiptFor(row))return;restoreTrigger=event?.currentTarget as HTMLElement|null;confirmTarget.value=row;restoreOrigin.value=null;lanzouRestorePreview.value=null;restoreOriginError.value='';emit('mutation',true);await nextTick();dialog.value?.focus()}
async function cancelRestore(){if(busy.value)return;confirmTarget.value=null;restoreDestination.value=null;choosingRestoreFolder.value=false;emit('mutation',false);await nextTick();restoreTrigger?.focus()}
async function checkGuangyaRestoreOrigin(){
 const target=confirmTarget.value,id=accountId.value,stamp=generation
 if(disposed||busy.value||props.blocked||!isGuangya.value||!target?.restoreHandle)return
 restoreOrigin.value=null;restoreOriginError.value='';busy.value=true
 try{
  const result=await api.PreviewGuangyaRecycleRestore(target.restoreHandle)
  if(disposed||stamp!==generation||confirmTarget.value!==target)return
  if(result.location.accountId!==id||result.location.parentId!==target.parentId)throw Error('原目录与所选回收站项目不一致')
  restoreOrigin.value=result
 }catch(e){
  if(!disposed&&stamp===generation&&confirmTarget.value===target)restoreOriginError.value=String(e)
 }finally{
  busy.value=false
  if(reload&&!disposed){reload=false;emit('mutation',false);await load(1)}
 }
}
function openGuangyaRestoreOrigin(){
 const location=restoreOrigin.value?.location
 if(!confirmTarget.value||!isGuangya.value||!location||location.accountId!==accountId.value||busy.value||props.blocked)return
 confirmTarget.value=null;restoreOrigin.value=null
 emit('mutation',false)
 emit('open-location',location,false)
}
async function checkLanzouRestore(){
 const target=confirmTarget.value,stamp=generation
 if(disposed||busy.value||props.blocked||!isLanzou.value||!target?.restoreHandle)return
 lanzouRestorePreview.value=null;restoreOriginError.value='';busy.value=true
 try{
  const result=await api.PreviewLanzouRecycleRestore(target.restoreHandle)
  if(!disposed&&stamp===generation&&confirmTarget.value===target)lanzouRestorePreview.value=result
 }catch(e){
  if(!disposed&&stamp===generation&&confirmTarget.value===target)restoreOriginError.value=String(e)
 }finally{
  busy.value=false
  if(reload&&!disposed){reload=false;emit('mutation',false);await load(1)}
 }
}
async function closeRestoreFolder(){
 choosingRestoreFolder.value=false
 await nextTick()
 dialog.value?.focus()
}
function selectRestoreFolder(id:string,path:string,trail:CloudFolder[]){
 if(!choosingRestoreFolder.value||!canChooseRestoreFolder.value||props.blocked)return
 if(!id||!path||trail.at(-1)?.id!==id||restoreFolderRows.value.some(row=>row.parentId===id||trail.some(folder=>folder.id===row.id)))return
 restoreDestination.value={path,trail}
 batchPlan.value=null
 void closeRestoreFolder()
}
function useOriginalRestoreDestination(){
 restoreDestination.value=null
 batchPlan.value=null
}
const accounts=computed(()=>props.accounts.filter(a=>['quark','pan123','baidu','lanzou','guangya'].includes(a.kind)))
const accountId=ref(props.reviewTarget?.accountId??accounts.value[0]?.id??''),items=ref<RecycleEntry[]>([]),page=ref(1),hasMore=ref(false),busy=ref(false),error=ref(''),query=ref('')
const confirmedEmpty=ref(false),listBrowseOnly=ref(false)
const diagnosticMessage=ref('')
const recycleDiagnostic=computed(()=>error.value.match(/\bRECYCLE123 data=[a-z]+ list=[a-z]+ total=[a-z]+ next=[a-z]+ len=[a-z]+(?: reason=[a-z]+)?\b/)?.[0]??'')
watch(error,()=>{diagnosticMessage.value=''})
async function copyRecycleDiagnostic(){
 const summary=recycleDiagnostic.value
 if(!summary)return
 try{
  await api.CopyDownloadReport(summary)
  if(recycleDiagnostic.value===summary)diagnosticMessage.value='诊断已复制'
 }catch{
  if(recycleDiagnostic.value===summary)diagnosticMessage.value='复制失败，可以选中诊断文字后复制'
 }
}
const accountSearchOpen=ref(false)
const accountScope=computed(()=>JSON.stringify([props.refreshKey,props.accounts.map(a=>[a.id,a.kind,a.endpoint,a.username])]))
type RecycleSearchEntry=Pick<RecycleEntry,'id'|'name'|'kind'|'originalPath'>
type RecycleSearchLocation=AccountSearchLocation<RecycleSearchEntry>
const accountSearchSources=computed<AccountSearchSource<RecycleSearchEntry>[]>(()=>accounts.value.map(account=>({
 id:account.id,label:accountLabel(account),
 reader:()=>async page=>{
  const result=await api.ListCloudRecycle(account.id,page)
  return {...result,items:result.items?.map(({id,name,kind,originalPath})=>({id,name,kind,originalPath}))??null}
 },
})))
const locatedEntryId=ref('')
let pendingLocation:RecycleSearchLocation|undefined
function openAccountSearchLocation(location:RecycleSearchLocation){
 if(!accountSearchOpen.value||disposed||locked.value||props.blocked||
    !accounts.value.some(account=>account.id===location.accountId))return
 accountSearchOpen.value=false
 pendingLocation=location
 if(accountId.value===location.accountId)invalidate()
 else accountId.value=location.accountId
}
const searchPages=ref<{page:number;count:number}[]>([])
type SearchMatch=RecyclePurgeSelection&{incomplete:boolean}
const searchMatches=ref<SearchMatch[]>([])
const searchMatchPage=ref(1)
const searchMatchPageCount=computed(()=>Math.max(1,Math.ceil(searchMatches.value.length/25)))
const visibleSearchMatches=computed(()=>searchMatches.value.slice((searchMatchPage.value-1)*25,searchMatchPage.value*25))
const searchQueueIDs=computed(()=>new Set(activeQueue.value.map(row=>row.entry.id)))
const searchSummary=ref(''),searchRunning=ref(false),searchStop=ref(false),searchCanContinue=ref(false)
let scan:ReturnType<typeof createPageSearch<RecycleEntry>>|undefined
const coverageReport=ref<LanzouRecycleCoverage|null>(null)
const coverage=computed(()=>coverageReport.value?.rows??null)
const coverageError=ref(''),coverageLoading=ref(false)
const coverageIncompleteMessages:Record<string,string>={
 timeout:'文件夹清单核对超时。',
 'folder-limit':'可见文件夹超过 10 个。',
 'uncertain-inventory':'部分目录内容、文件标识或归属无法核对。',
}
const coverageFilter=ref('all'),coveragePage=ref(1)
const coverageQuery=ref(''),coverageCopying=ref(false),coverageCopyStatus=ref('')
const coverageSelection=ref<string[]>([])
const coverageSelected=computed(()=>(coverage.value??[]).flatMap(row=>
 row.state==='candidate'&&row.entry&&coverageSelection.value.includes(row.entry.id)?[row.entry]:[]))
const coverageLabels={candidate:'可提交预检',pending:'结果待核对',history:'已有操作回执',website:'请到官网处理'} as const
function coverageLimitation(row:LanzouRecycleCoverageRow){
 switch(row.limitation){
 case 'folder-inventory': return '当前文件夹清单无法完整核对。'
 case 'folder-markers': return '网页含子目录或其他条目标记。'
 case 'folder-timeout': return '读取此文件夹时超时。'
 case 'folder-not-checked': return '本次未核对这个文件夹。'
 case 'folder-membership': return row.kind==='folder'?'无法核对文件夹内文件与回收站列表的对应关系。':'可见文件夹的内容未全部核对，无法确认此文件是否独立。'
 case 'folder-member': return `此文件列在「${row.folder?.name}」的清单中。`
 case 'name-unconfirmed': return '网页显示的名称无法确认完整。'
 default: return ''
 }
}
const coverageCounts=computed(()=>({
 candidate:coverage.value?.filter(row=>row.state==='candidate').length??0,
 pending:coverage.value?.filter(row=>row.state==='pending').length??0,
 history:coverage.value?.filter(row=>row.state==='history').length??0,
 website:coverage.value?.filter(row=>row.state==='website').length??0,
}))
const coverageFiltered=computed(()=>{
 const term=coverageQuery.value.trim().toLocaleLowerCase()
 return (coverage.value??[]).filter(row=>(coverageFilter.value==='all'||row.state===coverageFilter.value)&&row.name.toLocaleLowerCase().includes(term))
})
const coverageVisible=computed(()=>coverageFiltered.value.slice((coveragePage.value-1)*25,coveragePage.value*25))
function matches(row:Pick<RecycleEntry,'name'|'originalPath'>,term:string){return (row.name+' '+(row.originalPath??'')).toLocaleLowerCase().includes(term)}
const rows=computed(()=>items.value.filter(row=>matches(row,query.value.trim().toLocaleLowerCase())))
function accountLabel(a:Account){
 return shareAccountLabel(a,accounts.value)
}
const operationAccountLabel=computed(()=>activeAccount.value?accountLabel(activeAccount.value):'')
async function openLanzouRecycle(){
 if(!isLanzou.value||openingLanzou.value||props.blocked||locked.value)return
 const stamp=generation
 openingLanzou.value=true
 try{
  await Browser.OpenURL('https://up.woozooo.com/mydisk.php?item=recycle&action=files')
 }catch{
  if(!disposed&&stamp===generation)message.value='无法打开浏览器。请在浏览器登录蓝奏云官网，再打开回收站。'
 }finally{
  openingLanzou.value=false
 }
}
function sizeText(row:RecycleEntry){if(row.kind==='folder'||row.size==null||!Number.isSafeInteger(row.size)||row.size<0)return '—';if(row.size<1024)return row.size+' B';const units=['KB','MB','GB','TB'];let n=row.size/1024,i=0;while(n>=1024&&i<3){n/=1024;i++}return Number(n.toFixed(1))+' '+units[i]}
let generation=0,reload=false,disposed=false
function clearSearch(){scan=undefined;searchCanContinue.value=false;searchPages.value=[];searchMatches.value=[];searchMatchPage.value=1;searchSummary.value=''}
function closeCoverage(){coverageReport.value=null;coverageError.value='';coverageFilter.value='all';coverageQuery.value='';coveragePage.value=1;coverageSelection.value=[];coverageCopyStatus.value=''}
async function copyCoverage(){
 const snapshot=coverageReport.value
 if(!snapshot||coverageCopying.value||locked.value||props.blocked||disposed)return
 const rows=(snapshot.rows??[]).map(row=>[
  row.name,row.kind==='folder'?'文件夹':'文件',coverageLabels[row.state as keyof typeof coverageLabels]??row.state,String(row.page),coverageLimitation(row),
 ].map(spreadsheetCell).join('\t'))
 const incomplete=snapshot.incompleteReason
 const explanation=incomplete?coverageIncompleteMessages[incomplete]??'请到官网核对。':''
 const report=[
  spreadsheetCell(operationAccountLabel.value),
  '蓝奏回收站清理范围 · 本次网页可见 '+rows.length+' 项',
  '已核对 '+snapshot.checkedFolders+' / '+snapshot.visibleFolders+' 个可见文件夹'+(incomplete?' · 范围核对不完整。'+explanation:''),
  '名称\t类型\t处理状态\t来源页\t受限原因',...rows,
 ].join('\r\n')
 coverageCopying.value=true
 coverageCopyStatus.value=''
 try{
  await api.CopyDownloadReport(report)
  if(!disposed&&snapshot===coverageReport.value)coverageCopyStatus.value='已复制 '+rows.length+' 项预览记录。'
 }catch{
  if(!disposed&&snapshot===coverageReport.value)coverageCopyStatus.value='无法写入剪贴板。'
 }finally{coverageCopying.value=false}
}
function invalidate(){
 previewPurgeTrigger=null
 restoredMoveBatch.value=null
 restoredFolderMove.value=null
 restoredMoveSelection.value=[]
 clearSearch();closeCoverage();clearQueues();folderPreview.value=null;emptyTarget.value=null;confirmedEmpty.value=false;listBrowseOnly.value=false;purgeRefreshPending=false;purgeTarget.value=null;stopRequested.value=true;selectionMode.value='restore';selected.value=[];batchConfirm.value=[];batchPlan.value=null;if(!batchRunning.value)batchResults.value=[];confirmTarget.value=null;restoreOrigin.value=null;lanzouRestorePreview.value=null;restoreOriginError.value='';restoreDestination.value=null;choosingRestoreFolder.value=false;receipts.value=[];message.value='';generation++;items.value=[];page.value=1;hasMore.value=false;error.value='';query.value='';locatedEntryId.value=''
 const location=pendingLocation?.accountId===accountId.value?pendingLocation:undefined
 pendingLocation=undefined
 if(busy.value){reload=true;return}
 emit('mutation',false)
 void load(location?.page??1,location?.entry)
}
async function loadCoverage(){
 const id=accountId.value,stamp=generation
 if(disposed||locked.value||props.blocked||!isLanzou.value||!id)return
 closeCoverage()
 coverageLoading.value=true;busy.value=true;emit('mutation',true)
 try{
  const result=await api.PreviewLanzouRecycleCoverage(id)
  if(disposed||stamp!==generation||id!==accountId.value)return
  if(!result?.rows?.length)throw Error('网页范围未能确认，请到官网核对')
  coverageReport.value=result
 }catch(e){if(!disposed&&stamp===generation&&id===accountId.value)coverageError.value=String(e)}
 finally{
  coverageLoading.value=false;busy.value=false
  if(!disposed){
   emit('mutation',false)
   if(reload){reload=false;await load(1)}
  }
 }
}
async function openCoveragePage(row:LanzouRecycleCoverageRow){
 if(locked.value||props.blocked||!coverage.value?.includes(row))return
 closeCoverage()
 query.value=row.name
 await load(row.page)
}
function openCoverageFolder(row:LanzouRecycleCoverageRow,event:Event){
 if(row.folder&&coverage.value?.includes(row))openFolderPreview(row.folder,event)
}
function toggleCoverageSelection(row:LanzouRecycleCoverageRow){
 if(locked.value||props.blocked||!coverage.value?.includes(row)||row.state!=='candidate'||!row.entry)return
 const id=row.entry.id
 if(coverageSelection.value.includes(id)){
  coverageSelection.value=coverageSelection.value.filter(selectedId=>selectedId!==id)
 }else if(coverageSelection.value.length<50){
  coverageSelection.value.push(id)
 }
}
function prepareCoveragePurge(){
 if(locked.value||props.blocked||!isLanzou.value||!accountId.value||!coverageSelected.value.length)return
 purgeTarget.value={accountId:accountId.value,accountName:operationAccountLabel.value,page:0,entries:coverageSelected.value.map(entry=>({...entry}))}
 emit('mutation',true)
}
async function searchAcrossPages(continuing=false){
 const term=query.value.trim().toLocaleLowerCase()
 const id=accountId.value,stamp=generation,blue=isLanzou.value
 if(disposed||locked.value||props.blocked||!id||!term)return
 if(!continuing){
  clearSearch()
  scan=createPageSearch(async target=>{
   const result=await api.ListCloudRecycle(id,target)
   return {...result,incomplete:result.incomplete||result.browseOnly===true}
  },row=>matches(row,term),row=>row.id)
 }
 if(!scan||continuing&&!searchCanContinue.value)return
 selected.value=[]
 searchStop.value=false;searchRunning.value=true;busy.value=true;emit('mutation',true)
 const roundMatches:SearchMatch[]=[]
 try{
  const result=await scan(
   ()=>!disposed&&stamp===generation&&id===accountId.value&&term===query.value.trim().toLocaleLowerCase(),
   ()=>searchStop.value||!!props.blocked,
   (sourcePage,entries,incomplete)=>{
    for(const entry of entries)roundMatches.push({page:sourcePage,entry,incomplete})
   })
  if(result){
   searchPages.value=result.hits
   searchMatches.value=roundMatches
   searchMatchPage.value=1
   searchSummary.value=result.summary+(blue?'蓝奏仅搜索网页可识别的记录。':'')
   searchCanContinue.value=result.canContinue
   if(!result.canContinue)scan=undefined
  }
 }finally{
  searchRunning.value=false;busy.value=false
  if(!disposed){
   emit('mutation',false)
   if(reload){reload=false;await load(1)}
  }
 }
}
function searchQueueBlock(row:SearchMatch){
 if(row.incomplete)return '来源页不完整'
 if(searchQueueIDs.value.has(row.entry.id))return '已加入清单'
 if(selectionMode.value==='restore'){
  if(!row.entry.restoreHandle||receiptFor(row.entry))return '此项暂不可还原'
 }else{
  if(isLanzou.value)return '蓝奏永久删除请使用清理范围预览'
  if(row.page>50)return '永久删除清单限前 50 页'
  if(!canPurgeRow(row.entry))return '此项暂不可永久删除'
 }
 if(activeQueue.value.length>=50)return '清单已满'
 return ''
}
function queueSearchMatch(row:SearchMatch){
 if(disposed||locked.value||props.blocked||error.value||!canRestore.value||
    !searchMatches.value.includes(row)||searchQueueBlock(row))return
 addToQueue([row])
}
async function openSearchPage(target:number){
 if(!searchPages.value.some(hit=>hit.page===target))return
 await load(target)
}
async function loadReviewRestore(){
 if(focusedReview.value?.kind!=='restore'||!activeAccount.value)return
 const stamp=generation
 busy.value=true;error.value='';emit('mutation',true)
 try{
  const result=await api.CloudRecycleRestores(accountId.value)
  if(!disposed&&stamp===generation)receipts.value=result??[]
 }catch(e){if(!disposed&&stamp===generation)error.value=String(e)}
 finally{
  busy.value=false
  if(!disposed){
   emit('mutation',false)
   if(reload){reload=false;await load(1)}
  }
 }
}
function browseRecycle(){
 if(locked.value||props.blocked)return
 focusedReview.value=null
 if(!activeAccount.value){accountId.value=accounts.value[0]?.id??'';return}
 void load(1)
}
async function load(target=1,focus?:RecycleSearchLocation['entry']){
 if(disposed||locked.value||props.blocked||!accountId.value)return
 if(focusedReview.value){await loadReviewRestore();return}
 const stamp=generation,id=accountId.value
 closeCoverage();selected.value=[];busy.value=true;error.value='';items.value=[];receipts.value=[];hasMore.value=false;confirmedEmpty.value=false;listBrowseOnly.value=false
 if(locatedEntryId.value)message.value=''
 locatedEntryId.value=''
 try{
  const [remote,history]=await Promise.allSettled([api.ListCloudRecycle(id,target),canRestore.value?api.CloudRecycleRestores(id):Promise.resolve([])])
  if(disposed||stamp!==generation||id!==accountId.value)return
  if(history.status==='fulfilled')receipts.value=history.value??[]
  else error.value=String(history.reason)
  if(remote.status==='rejected'){
   error.value=String(remote.reason)
   return
  }
  items.value=remote.value.items??[]
  page.value=remote.value.page
  hasMore.value=remote.value.hasMore
  confirmedEmpty.value=page.value===1&&remote.value.total===0&&!remote.value.incomplete&&!hasMore.value&&!items.value.length
  listBrowseOnly.value=remote.value.browseOnly===true
  if(listBrowseOnly.value)clearQueues()
  if(remote.value.incomplete)error.value='回收站列表不完整，请刷新或到官网核对；本次操作已禁用'
  if(focus&&!error.value){
   const found=items.value.filter(row=>row.id===focus.id)
   if(page.value===target&&found.length===1&&found[0].name===focus.name&&
      found[0].kind===focus.kind&&found[0].originalPath===focus.originalPath){
    query.value=focus.name
    locatedEntryId.value=focus.id
    message.value='已定位「'+focus.name+'」。'
   }else{
    message.value='该项目已变化或不在原页，请重新查找。'
   }
  }
 }catch(e){
  if(!disposed&&stamp===generation)error.value=String(e)
 }finally{
  busy.value=false
  if(reload&&!disposed){reload=false;await load(1)}
 }
}
function canRestoreThenMove(row:RecycleEntry){
 if(row.kind==='folder')return canMoveRestoredFiles.value
 return canMoveRestoredFiles.value&&
  row.kind==='file'&&typeof row.size==='number'&&Number.isSafeInteger(row.size)&&row.size>=0
}
function canRestoreDirectly(row:RecycleEntry){return activeAccount.value?.kind==='pan123'&&(row.kind==='file'||row.kind==='folder')&&!!row.restoreHandle}
const restoreFolderRows=computed(()=>confirmTarget.value?[confirmTarget.value]:batchConfirm.value)
const canChooseRestoreFolder=computed(()=>restoreFolderRows.value.length>0&&restoreFolderRows.value.every(canRestoreDirectly))
async function restore(move=false){
 const target=confirmTarget.value,destination=restoreDestination.value
 if(busy.value||props.blocked||!target||choosingRestoreFolder.value||restoreOrigin.value?.occupied||
    (move&&(!canRestoreThenMove(target)||!!destination))||(destination&&!canRestoreDirectly(target)))return
 const stamp=generation
 const id=accountId.value
 const action=target.kind==='folder'?'folder':'file'
 let confirmed:RecycleRestoreResult|undefined
 confirmTarget.value=null;restoreDestination.value=null;clearSearch();clearQueues();busy.value=true;error.value=''
 message.value='正在提交并核对还原，请勿重复操作';emit('mutation',true)
 try{
  const result=destination?await api.RestoreCloudRecycleTo(target.restoreHandle,destination.trail):await api.RestoreCloudRecycle(target.restoreHandle)
  if(stamp===generation&&!disposed){
   message.value=result.message
   if(move&&result.state==='confirmed')confirmed=result
  }
 }catch(e){
  if(stamp===generation&&!disposed)message.value='还原调用中断，请刷新本机还原记录核对，不要反复提交：'+String(e)
 }finally{
  busy.value=false;emit('mutation',false)
  if(!disposed){reload=false;await load(stamp===generation?page.value:1)}
 }
 if(confirmed&&!disposed&&stamp===generation&&id===accountId.value){
  const receipt=receipts.value.find(row=>row.id===confirmed.id&&(action==='folder'?row.canMoveFolder:row.canMove))
  if(receipt)await locateRestore(receipt,action)
 }
}
async function checkRestore(row:RecycleRestoreResult){if(locked.value||props.blocked||!row.canCheck)return;busy.value=true;error.value='';emit('mutation',true);const stamp=generation;try{const r=await api.CheckCloudRecycleRestore(row.id);if(stamp===generation&&!disposed)message.value=r.message}catch(e){if(stamp===generation&&!disposed)message.value=String(e)}finally{busy.value=false;emit('mutation',false);if(!disposed){reload=false;await load(stamp===generation?page.value:1)}}}
async function locateRestore(row:RecycleRestoreResult,action:'open'|'file'|'folder'='open',event?:Event){
 const id=accountId.value,stamp=generation
 if(disposed||locked.value||props.blocked||!id||!receipts.value.some(r=>r.id===row.id&&r.state==='confirmed'&&
    (action==='open'||(action==='file'?r.canMove:r.canMoveFolder))))return
 const trigger=event?.currentTarget as HTMLElement|undefined
 busy.value=true;locating.value=true;message.value='';emit('mutation',true)
 let location:RecycleRestoreLocation|undefined
 try{
  const result=await api.LocateCloudRecycleRestore(id,row.id)
  if(result.accountId!==id||!result.parentId)throw Error('还原位置与所选账号不一致')
  if(action==='file'&&!result.moveFile)throw Error('还原文件暂时无法核对，请打开原目录查看。')
  if(action==='folder'&&!result.moveFolderId)throw Error('还原文件夹暂时无法核对，请打开原目录查看。')
  location=result
 }catch(e){
  if(!disposed&&stamp===generation)message.value=String(e)
 }finally{
  busy.value=false;locating.value=false
  if(!disposed)emit('mutation',false)
 }
 if(disposed)return
 if(reload){reload=false;await load(1);return}
 if(!location||stamp!==generation||id!==accountId.value||props.blocked)return
 if(action==='folder'){
  const kind=activeAccount.value?.kind
  if(!kind||!location.moveFolderId)return
  restoredFolderTrigger=trigger??null
  restoredFolderMove.value={accountId:id,accountName:operationAccountLabel.value,kind,receiptId:row.id,entryId:location.moveFolderId,
   parentId:location.parentId,name:row.name}
  emit('mutation',true)
 }else emit('open-location',location,action==='file')
}
function showRestoredFolderCheck(row:RecycleRestoreResult,event:Event){
 const id=accountId.value,kind=activeAccount.value?.kind
 if(disposed||locked.value||props.blocked||!id||!kind||
    !visibleReceipts.value.some(r=>r.id===row.id&&r.canMoveFolder))return
 restoredFolderTrigger=event.currentTarget as HTMLElement|null
 restoredFolderMove.value={accountId:id,accountName:operationAccountLabel.value,kind,receiptId:row.id,
  entryId:'',parentId:'',name:row.name,checkOnly:true}
 emit('mutation',true)
}
async function clearReceipts(){if(locked.value||props.blocked||!canClear.value)return;busy.value=true;emit('mutation',true);try{const n=await api.ClearCloudRecycleRestores(accountId.value);message.value='已清理 '+n+' 条已还原或未提交记录；待确认记录保留'}catch(e){message.value=String(e)}finally{busy.value=false;emit('mutation',false);if(!disposed)await load(page.value)}}

watch(query,()=>{selected.value=[];clearSearch()})
watch([coverageFilter,coverageQuery],()=>{coveragePage.value=1},{flush:'sync'})
watch(selectionMode,()=>{selected.value=[]})
watch(accountId,invalidate)
watch(accountScope,()=>{pendingLocation=undefined;const next=focusedReview.value||accounts.value.some(a=>a.id===accountId.value)?accountId.value:accounts.value[0]?.id??'';if(next!==accountId.value)accountId.value=next;else invalidate()})
onMounted(()=>load())
onUnmounted(()=>{disposed=true;stopRequested.value=true;generation++;reload=false;emit('mutation',false)})
</script>
<template>
 <section class="recycle-page">
  <div class="view-heading"><h1>回收站</h1></div>
  <div v-if="focusedReview" class="share-history-actions">
   <strong>{{operationAccountLabel||'原账号已移除'}}</strong>
   <button class="secondary" :disabled="locked||blocked" @click="browseRecycle">打开回收站列表</button>
   <button v-if="focusedReview.kind==='restore'" class="secondary" :disabled="locked||blocked" @click="load()">刷新本机回执</button>
  </div>
  <p v-if="focusedReview&&error" class="banner error" role="alert">{{error}}</p>
  <p v-if="focusedReview&&message" class="banner notice" role="status">{{message}}</p>
  <p v-if="focusedReview?.kind==='restore'&&!busy&&!visibleReceipts.length" class="muted">原还原回执已不存在或不可读取。</p>
  <p v-if="focusedReview?.kind==='purge'&&!canPurge" class="muted">原永久删除回执不可读取。</p>
  <AccountSearch v-if="accountSearchOpen" :sources="accountSearchSources" :scope="accountScope" :blocked="blocked||locked"
   title="跨账号查找回收站" search-label="名称或原路径" return-label="返回回收站"
   note="蓝奏仅查找网页可识别的记录。" :identity="row=>row.id" :matches="matches" :describe="row=>row.originalPath"
   @open="openAccountSearchLocation" @close="accountSearchOpen=false"/>
  <template v-else-if="!focusedReview">
  <div class="recycle-toolbar">
   <label>网盘<select v-model="accountId" :disabled="blocked||locked" aria-label="选择回收站网盘"><option v-if="!accounts.length" value="">暂无支持的网盘</option><option v-for="a in accounts" :key="a.id" :value="a.id">{{accountLabel(a)}}</option></select></label>
   <input v-model="query" type="search" :disabled="locked||blocked" aria-label="查找回收站项目" placeholder="按名称或原路径查找"/>
   <button class="secondary" :disabled="locked||blocked||!accountId" @click="clearSearch();clearQueues();load(page)"><AppIcon name="refresh"/>刷新列表</button>
   <button v-if="searchRunning" class="secondary" :disabled="searchStop" @click="searchStop=true">停止搜索</button>
   <button v-else class="secondary" :disabled="locked||blocked||!accountId||!query.trim()" @click="searchAcrossPages()">跨页搜索</button>
   <button class="secondary" :disabled="locked||blocked||!accounts.length" @click="accountSearchOpen=true">跨账号查找</button>
   <label v-if="canPurge">选择用途<select v-model="selectionMode" :disabled="locked||blocked" aria-label="回收站选择用途"><option value="restore">还原</option><option value="purge">永久删除</option></select></label>
   <button v-if="canEmpty" class="secondary" :disabled="locked||blocked||!accountId||!!error||confirmedEmpty||listBrowseOnly" @click="prepareEmpty">清空整个回收站…</button>
   <button v-else-if="isLanzou" class="secondary" :disabled="blocked||locked||openingLanzou" @click="openLanzouRecycle">在浏览器打开蓝奏云回收站</button>
   <button v-if="isLanzou" class="secondary" :disabled="blocked||locked" @click="loadCoverage">{{coverageLoading?'正在读取范围…':'预览清理范围'}}</button>
  </div>
  <p v-if="coverageError" class="banner error" role="alert">{{coverageError}}</p>
  <section v-if="coverage" class="share-history-card" aria-label="蓝奏回收站清理范围">
   <div class="share-history-card-heading"><strong>网页已识别 {{coverage.length}} 项</strong><button class="secondary" :disabled="locked" @click="closeCoverage">关闭预览</button></div>
   <p v-if="coverageReport?.incompleteReason" class="banner notice" role="status">范围核对不完整。已核对 {{coverageReport.checkedFolders}} / {{coverageReport.visibleFolders}} 个网页可见文件夹。{{coverageIncompleteMessages[coverageReport.incompleteReason]??'请到官网核对。'}}</p>
   <p>可提交预检 {{coverageCounts.candidate}} 项，待核对 {{coverageCounts.pending}} 项，已有回执 {{coverageCounts.history}} 项，需到官网处理 {{coverageCounts.website}} 项。</p>
   <p class="recycle-note">可跨页勾选可预检项目，每批最多 50 项。提交前会重新读取并核对所选项目。</p>
   <div class="share-history-actions"><button class="secondary" :disabled="locked||blocked||!coverageSelected.length" @click="prepareCoveragePurge">查看所选（{{coverageSelected.length}}）</button><button v-if="coverageSelected.length" class="secondary" :disabled="locked||blocked" @click="coverageSelection=[]">取消选择</button><button class="secondary" :disabled="locked||blocked||coverageCopying" @click="copyCoverage">复制完整预览</button></div>
   <p v-if="coverageCopyStatus" role="status">{{coverageCopyStatus}}</p>
   <div class="share-history-filters">
    <input v-model="coverageQuery" type="search" placeholder="搜索预览中的名称" aria-label="搜索清理范围预览" :disabled="locked||blocked"/>
    <select v-model="coverageFilter" aria-label="按处理状态筛选" :disabled="locked||blocked"><option value="all">全部状态</option><option v-for="(name,key) in coverageLabels" :key="key" :value="key">{{name}}</option></select>
   </div>
   <p v-if="!coverageVisible.length">{{coverageQuery.trim()||coverageFilter!=='all'?'没有符合筛选条件的项目。':'本次网页未返回项目。'}}</p>
   <ul v-else class="recycle-batch-preview">
    <li v-for="(row,index) in coverageVisible" :key="index">
     <label v-if="row.state==='candidate'&&row.entry"><input type="checkbox" :checked="coverageSelection.includes(row.entry.id)" :disabled="locked||blocked||coverageSelection.length>=50&&!coverageSelection.includes(row.entry.id)" :aria-label="'选择预检 '+row.name" @change="toggleCoverageSelection(row)"/> <strong>{{row.name}}</strong></label>
     <strong v-else>{{row.name}}</strong>
     <p>{{row.kind==='folder'?'文件夹':'文件'}} · {{coverageLabels[row.state as keyof typeof coverageLabels]}}</p>
     <p v-if="row.limitation">{{coverageLimitation(row)}}</p>
     <div class="share-history-actions">
      <button v-if="row.folder" class="secondary" :disabled="locked||blocked||!!error" @click="openCoverageFolder(row,$event)">{{row.kind==='folder'?'查看文件夹内容':'查看所属文件夹'}}</button>
      <button class="secondary" :disabled="locked||blocked" @click="openCoveragePage(row)">打开第 {{row.page}} 页</button>
     </div>
    </li>
   </ul>
   <div v-if="coverageFiltered.length>25" class="share-history-actions"><button class="secondary" :disabled="coveragePage<=1" @click="coveragePage--">上一页</button><span>预览第 {{coveragePage}} 页</span><button class="secondary" :disabled="coveragePage*25>=coverageFiltered.length" @click="coveragePage++">下一页</button></div>
  </section>
  <section v-if="searchSummary" class="share-history-card" aria-label="回收站跨页搜索结果">
   <p role="status">{{searchSummary}}</p>
   <button v-if="searchCanContinue" class="secondary" :disabled="locked||blocked" @click="searchAcrossPages(true)">继续搜索</button>
   <template v-if="searchMatches.length">
    <p>本轮命中 {{searchMatches.length}} 项，跨页清单已加入 {{activeQueue.length}} / 50 项。</p>
    <ul class="recycle-batch-preview">
     <li v-for="result in visibleSearchMatches" :key="result.entry.id">
      <div class="share-history-card-heading">
       <strong>{{result.entry.name}}</strong>
       <span v-if="searchQueueBlock(result)" class="muted">{{searchQueueBlock(result)}}</span>
       <button v-else class="secondary" :disabled="locked||blocked||!!error" @click="queueSearchMatch(result)">{{selectionMode==='restore'?'加入还原清单':'加入永久删除清单'}}</button>
      </div>
      <p>第 {{result.page}} 页 · {{result.entry.kind==='folder'?'文件夹':result.entry.kind==='file'?'文件':'类型未返回'}}<template v-if="result.entry.originalPath"> · {{result.entry.originalPath}}</template></p>
     </li>
    </ul>
    <div v-if="searchMatchPageCount>1" class="share-history-actions">
     <button class="secondary" :disabled="locked||blocked||searchMatchPage<=1" @click="searchMatchPage--">上组命中</button>
     <span>{{searchMatchPage}} / {{searchMatchPageCount}}</span>
     <button class="secondary" :disabled="locked||blocked||searchMatchPage>=searchMatchPageCount" @click="searchMatchPage++">下组命中</button>
    </div>
   </template>
   <div v-if="searchPages.length" class="share-history-actions">
    <button v-for="hit in searchPages" :key="hit.page" class="secondary" :disabled="locked||blocked" @click="openSearchPage(hit.page)">第 {{hit.page}} 页（{{hit.count}} 条）</button>
   </div>
  </section>
  <details v-if="isGuangya||canEmpty||isLanzou" class="operation-details"><summary>还原与清理范围</summary><p v-if="isGuangya" class="recycle-note">光鸭支持还原原目录明确的项目，包括根目录。原目录缺失或有同名冲突时，请在官网还原。也可永久删除所选项目或清空回收站。</p>
  <p v-else-if="canEmpty" class="recycle-note">当前支持夸克、123 和百度。每页最多 100 项，保留期限以官网为准。三家支持单项及批量还原（每次最多 50 项）。夸克和123按原目录ID核对，不猜测完整路径；缺原FID、原目录或文件大小的记录暂不能还原。批量开始前整批检查，执行前逐项再检查。永久删除需另行输入确认且无法还原；所选永久删除只操作选中项目；清空整个回收站需独立读取完整清单并再次确认。</p>
  <p v-else-if="isLanzou" class="recycle-note">可核对的文件预计还原到根目录。「预览清理范围」可选择归属明确的独立文件和内容可完整核对的文件夹。清理前请核对将一并删除的文件。文件夹还原需要本机移入记录，归属不明的项目及整站清空请到官网处理。</p></details>
  <div v-if="canRestore&&!listBrowseOnly" class="cloud-selection-bar">
   <button class="secondary" :disabled="locked||blocked||!!error||!eligible.length" @click="selectVisible">选择本页可操作项（最多50项）</button>
   <button class="secondary" :disabled="locked||blocked||!selected.length" @click="selected=[]">清除选择</button><span>本页已选 {{selectedRows.length}} 项</span>
   <template v-if="selectionMode==='restore'">
    <button class="primary" :disabled="locked||blocked||!!error||!selectedRows.length" @click="prepareBatch($event)">还原所选</button>
    <button class="secondary" :disabled="locked||blocked||!!error||!canQueueSelection" title="跨页清单最多 50 项" @click="queueSelected">加入跨页还原清单</button>
   </template>
   <template v-else-if="canPurge">
    <button class="secondary" :disabled="locked||blocked||!!error||!selectedRows.length" @click="preparePurge(selectedRows)">永久删除所选…</button>
    <button v-if="canEmpty" class="secondary" :disabled="locked||blocked||!!error||!canQueueSelection" title="前 50 页内最多 50 项" @click="queueSelected">加入跨页永久删除清单</button>
   </template>
   <button class="secondary" :disabled="locked||blocked||!checkable.length" @click="runBatch(true)">核对全部待确认（{{checkable.length}}）</button>
  </div>
  <section v-if="selectionMode==='restore'&&restoreQueue.length" class="share-history-card" aria-label="跨页还原清单">
   <div class="share-history-card-heading"><strong>跨页还原清单（{{restoreQueue.length}} / 50）</strong><button class="secondary" :disabled="locked||blocked" @click="restoreQueue=[]">清空清单</button></div>
   <ul class="recycle-batch-preview">
    <li v-for="{entry,page:sourcePage} in restoreQueue" :key="entry.id">
     <div class="share-history-card-heading"><strong>{{entry.name}}</strong><button class="secondary" :disabled="locked||blocked" :aria-label="'从清单移除 '+entry.name" @click="removeQueuedEntry(entry.id)">移除</button></div>
     <p>第 {{sourcePage}} 页 · {{restorePath(entry)}}</p>
    </li>
   </ul>
   <button class="primary" :disabled="locked||blocked||!!error" @click="prepareBatch($event,true)">查看整批还原</button>
  </section>
  <section v-if="selectionMode==='purge'&&purgeQueue.length" class="share-history-card" aria-label="跨页永久删除清单">
   <div class="share-history-card-heading"><strong>跨页永久删除清单（{{purgeQueue.length}} / 50）</strong><button class="secondary" :disabled="locked||blocked" @click="purgeQueue=[]">清空清单</button></div>
   <ul class="recycle-batch-preview">
    <li v-for="{entry,page:sourcePage} in purgeQueue" :key="entry.id">
     <div class="share-history-card-heading"><strong>{{entry.name}}</strong><button class="secondary" :disabled="locked||blocked" :aria-label="'从清单移除 '+entry.name" @click="removeQueuedEntry(entry.id)">移除</button></div>
     <p>第 {{sourcePage}} 页 · {{entry.kind==='folder'?'文件夹':'文件'}}<template v-if="entry.originalPath"> · {{entry.originalPath}}</template></p>
    </li>
   </ul>
   <button class="secondary" :disabled="locked||blocked||!!error" @click="prepareQueuedPurge">查看整批永久删除…</button>
  </section>
  <section v-if="batchResults.length" class="cloud-move-results" aria-label="批量还原结果"><div class="recycle-receipts-heading"><strong>已处理 {{batchDone}} / {{batchResults.length}} 项 · 已还原 {{batchResults.filter(r=>r.state==='confirmed').length}} · 待确认 {{batchResults.filter(r=>r.state==='unknown').length}} · 未开始 {{batchResults.filter(r=>['queued','skipped'].includes(r.state)).length}}</strong><button v-if="batchRunning" class="secondary" :disabled="stopRequested" @click="stopRequested=true">{{stopRequested?'正在停止后续':'停止后续任务'}}</button></div><p v-if="batchRunning">当前请求将完成并核对；停止后不再发送后续请求。</p><ul aria-live="polite"><li v-for="r in batchResults" :key="r.id"><span>{{r.name}}</span><small>{{batchLabel(r.state)}}</small><p v-if="r.path">{{r.path}}</p><p v-if="r.message">{{r.message}}</p></li></ul></section>
  <BatchOperationReport v-if="batchResults.length&&!batchRunning" :operation="batchReportOperation" :account-name="operationAccountLabel" :rows="batchResults" :disabled="locked||blocked"/>
  <p v-if="message" class="banner notice" role="status">{{message}}</p>
  <div v-if="error" class="banner error">
   <span role="alert">{{recycleDiagnostic?error.slice(0,error.indexOf(recycleDiagnostic)).replace(/^RuntimeError:\s*/,'').trim():error}}</span>
   <div v-if="recycleDiagnostic" class="share-history-actions">
    <code>{{recycleDiagnostic}}</code>
    <button class="secondary" @click="copyRecycleDiagnostic">复制诊断</button>
    <span v-if="diagnosticMessage" role="status">{{diagnosticMessage}}</span>
   </div>
  </div>
  <p v-if="listBrowseOnly" class="banner notice" role="status">123 云盘返回了非标准分页。本页可浏览，需在官网还原或删除这些项目。</p>
  <div class="recycle-surface" :aria-busy="busy">
   <p v-if="busy" class="recycle-empty" role="status">{{locating?'正在核对还原位置…':batchRunning?batchPhase:'正在读取回收站…'}}</p>
   <p v-else-if="error" class="recycle-empty">本次未能读取，请刷新重试或到网盘官网查看。</p>
   <p v-else-if="!accounts.length" class="recycle-empty">请先添加夸克、123、百度、蓝奏或光鸭账号。</p>
   <p v-else-if="!items.length" class="recycle-empty">{{confirmedEmpty?'回收站为空。':'本页没有回收站记录。'}}</p>
   <p v-else-if="!rows.length" class="recycle-empty">本页没有匹配的项目，请修改筛选词。</p>
   <div v-else class="recycle-table-scroll"><table class="recycle-table" :class="{'recycle-selectable':canRestore&&!listBrowseOnly}"><thead><tr><th v-if="canRestore&&!listBrowseOnly">选择</th><th>名称</th><th>类型</th><th>大小</th><th>原路径</th><th v-if="canRestore&&!listBrowseOnly" class="recycle-action">操作</th></tr></thead><tbody><tr v-for="row in rows" :key="row.id"><td v-if="canRestore&&!listBrowseOnly"><input type="checkbox" :aria-label="(selectionMode==='purge'?'选择永久删除 ':'选择还原 ')+row.name" :checked="selected.includes(row.id)" :disabled="locked||blocked||!!error||!eligible.some(r=>r.id===row.id)||(!selected.includes(row.id)&&selected.length>=50)" @change="toggleSelected(row)"/></td><td><span class="recycle-name"><AppIcon :name="row.kind==='folder'?'files':'file'"/><span>{{row.name}}</span><small v-if="row.id===locatedEntryId">查找命中</small></span></td><td>{{row.kind==='folder'?'文件夹':row.kind==='file'?'文件':'未返回'}}</td><td>{{sizeText(row)}}</td><td class="recycle-path">{{row.originalPath||'网盘未返回'}}</td><td v-if="canRestore&&!listBrowseOnly"><div class="recycle-row-actions"><button v-if="isLanzou&&row.kind==='folder'" class="secondary" :disabled="locked||blocked" @click="openFolderPreview(row,$event)">查看内容</button><span v-if="receiptFor(row)">{{stateLabel(receiptFor(row)!.state)}}</span><button v-else class="secondary" :disabled="locked||blocked||!row.restoreHandle" :title="row.restoreHandle?'还原至 '+restorePath(row):'原路径或项目类型不完整，请到官网还原'" @click="prepareRestore(row,$event)">还原</button><button v-if="canPurgeRow(row)" class="secondary" :disabled="locked||blocked" @click="preparePurge([row])">永久删除…</button><button v-else-if="canPreviewLanzouRowPurge(row)" class="secondary" :disabled="locked||blocked" @click="previewLanzouRowPurge(row,$event)">检查能否清理</button></div></td></tr></tbody></table></div>
   <footer class="recycle-footer"><span>第 {{page}} 页 · 显示 {{rows.length}} / {{items.length}} 项</span><div><button class="secondary" :disabled="locked||blocked||page<=1" @click="load(1)">首页</button><button class="secondary" :disabled="locked||blocked||page<=1" @click="load(page-1)">上一页</button><button class="secondary" :disabled="locked||blocked||!hasMore" @click="load(page+1)">下一页</button></div></footer>
  </div>
  </template>
  <section v-if="!accountSearchOpen&&visibleReceipts.length" class="recycle-receipts"><div class="recycle-receipts-heading"><h2>本机还原记录</h2><button v-if="!focusedReview" class="secondary" :disabled="locked||blocked||!canClear" @click="clearReceipts">清理已完成记录</button></div><div v-if="!focusedReview&&canMoveRestoredFiles&&visibleReceipts.some(row=>row.canMove)" class="share-history-actions"><button class="secondary" :disabled="locked||blocked||!restoredMoveTargets.length" @click="openRestoredMoveBatch">移动所选已还原文件（{{restoredMoveTargets.length}} / 20）…</button><button class="secondary" :disabled="locked||blocked||!restoredMoveTargets.length" @click="showRestoredMoveBatch(restoredMoveTargets,$event.currentTarget as HTMLElement,true)">核对所选文件去向…</button></div><p class="recycle-note">记录重启保留。待确认时只检查原结果，不重新发送还原请求；清理仅影响本机记录。</p><article v-for="r in visibleReceipts" :key="r.id" class="share-history-card"><div class="share-history-card-heading"><label v-if="!focusedReview&&canMoveRestoredFiles&&r.canMove"><input type="checkbox" :aria-label="'选择移动 '+r.name" :checked="restoredMoveSelection.includes(r.id)" :disabled="locked||blocked||restoredMoveSelection.length>=20&&!restoredMoveSelection.includes(r.id)" @change="toggleRestoredMove(r,($event.target as HTMLInputElement).checked)"/> <strong>{{r.name}}</strong></label><strong v-else>{{r.name}}</strong><span>{{stateLabel(r.state)}}</span></div><p>{{r.message}}</p><button v-if="r.state==='confirmed'" class="secondary" :disabled="locked||blocked" @click="locateRestore(r)">打开还原目录</button><button v-if="r.canMove" class="secondary" :disabled="locked||blocked" @click="locateRestore(r,'file')">移动已还原文件…</button><button v-else-if="r.canMoveFolder" class="secondary" :disabled="locked||blocked" @click="locateRestore(r,'folder',$event)">移动已还原文件夹…</button><button v-else-if="r.canCheck" class="secondary" :disabled="locked||blocked" @click="checkRestore(r)">检查原还原结果</button><button v-if="r.canMove" class="secondary" :disabled="locked||blocked" @click="showRestoredMoveBatch([r],$event.currentTarget as HTMLElement,true)">核对文件去向…</button><button v-if="r.canMoveFolder" class="secondary" :disabled="locked||blocked" @click="showRestoredFolderCheck(r,$event)">核对文件夹去向…</button></article></section>
  <RestoredBatchMove v-if="restoredMoveBatch" v-bind="restoredMoveBatch" :blocked="blocked" @close="closeRestoredMoveBatch"/>
  <RestoredFolderMove v-if="restoredFolderMove" v-bind="restoredFolderMove" :blocked="blocked" @close="closeRestoredFolder"/>
  <RecycleFolderPreview v-if="folderPreview" :account-id="accountId" :account-name="operationAccountLabel" :entry-id="folderPreview.id" :name="folderPreview.name" @close="closeFolderPreview"/>
  <RecycleEmptyDialog v-if="emptyTarget" :account-id="emptyTarget.accountId" :account-name="emptyTarget.accountName" :server-clear="isGuangya" @close="closeEmpty" @changed="purgeChanged"/>
  <RecyclePurgeDialog v-if="purgeTarget" :account-id="purgeTarget.accountId" :account-name="purgeTarget.accountName" :page="purgeTarget.page" :entries="purgeTarget.entries" :selections="purgeTarget.selections" :auto-preview="purgeTarget.autoPreview" :guangya="isGuangya" :lanzou="isLanzou" @close="closePurge" @changed="purgeChanged"/>
  <RecyclePurgeHistory v-show="!accountSearchOpen" v-if="canPurge&&accountId&&(!focusedReview||focusedReview.kind==='purge')" :account-id="accountId" :receipt-id="focusedReview?.receiptId" :server-clear="isGuangya" :lanzou="isLanzou" :refresh-key="String(props.refreshKey??'')+String(purgeHistoryVersion)" :blocked="accountSearchOpen||blocked||busy||!!emptyTarget||!!purgeTarget||!!confirmTarget||!!folderPreview||!!batchConfirm.length" @mutation="purgeHistoryBusy=$event;emit('mutation',$event)"/>
  <div v-if="batchConfirm.length&&!choosingRestoreFolder" class="modal-backdrop"><section ref="dialog" tabindex="-1" class="operation-dialog share-dialog" role="dialog" aria-modal="true" aria-labelledby="recycle-batch-title" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="cancelBatch"><div class="modal-heading"><h2 id="recycle-batch-title">还原所选 {{batchConfirm.length}} 项</h2><button class="secondary" :disabled="busy" @click="cancelBatch">取消</button></div><p>网盘：{{operationAccountLabel}}</p><p v-if="restoreDestination" class="dialog-location">全部还原至 {{restoreDestination.path}}</p><p>先检查整批的还原位置，再逐项提交和核对。还原文件夹时会一并还原其内容。遇到冲突、失败或结果待确认即停止后续任务，已还原项目不会回滚。</p><ul class="recycle-batch-preview"><li v-for="r in batchConfirm" :key="r.id"><strong>{{r.name}}</strong><p v-if="!restoreDestination">{{restorePath(r)}}</p></li></ul><p>任务未开始的部分不会在重启后自动执行。目录内容请到官网核对。</p><p v-if="!restoreDestination&&canContinueBatchToMove">整批还原确认后可再选择统一的移动目录。</p><p v-if="batchPlan" :class="batchPlan.passed?'banner notice':'banner error'" :role="batchPlan.passed?'status':'alert'">{{batchPlan.message}}</p><div class="share-history-actions"><button v-if="canChooseRestoreFolder" class="secondary" :disabled="busy||blocked" @click="choosingRestoreFolder=true">{{restoreDestination?'更改还原目录…':'选择共同还原目录…'}}</button><button v-if="restoreDestination" class="secondary" :disabled="busy||blocked" @click="useOriginalRestoreDestination">各自还原到原位置</button><button class="secondary" :disabled="busy||blocked" @click="checkBatchPlan">{{busy?'正在预检…':'只读预检'}}</button><button class="primary" :disabled="busy||blocked" @click="runBatch()">确认逐项还原</button><button v-if="!restoreDestination&&canContinueBatchToMove" class="secondary" :disabled="busy||blocked" @click="runBatch(false,true)">还原后选择移动目录…</button></div></section></div>
  <FolderPicker v-if="choosingRestoreFolder&&canChooseRestoreFolder" :account-id="accountId" :account-name="operationAccountLabel"
   initial-folder="0" kind="pan123" purpose="restore" :excluded-folder-ids="restoreFolderRows.flatMap(row=>row.parentId?[row.parentId]:[])"
   :forbidden-roots="restoreFolderRows.filter(row=>row.kind==='folder').map(row=>row.id)" @close="closeRestoreFolder" @selected="selectRestoreFolder"/>
  <div v-else-if="confirmTarget" class="modal-backdrop"><section ref="dialog" tabindex="-1" class="operation-dialog share-dialog" role="dialog" aria-modal="true" aria-labelledby="recycle-restore-title" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="cancelRestore"><div class="modal-heading"><h2 id="recycle-restore-title">还原项目</h2><button class="secondary" :disabled="busy" @click="cancelRestore">取消</button></div><p>网盘：{{operationAccountLabel}}</p><p>{{confirmTarget.name}}</p><p class="dialog-location">预计还原至 {{restoreDestination?.path??restorePath(confirmTarget)}}</p><p>将此{{confirmTarget.kind==='folder'?'文件夹及其内容':'文件'}}从当前网盘回收站还原。还原位置无法读取或已有同名项目时，程序不会提交请求。</p><p v-if="!restoreDestination&&canRestoreThenMove(confirmTarget)">选择移动目录前，项目会先还原到原位置。取消移动时，项目留在原位置。</p><p v-if="restoreOriginError" class="banner error" role="alert">{{restoreOriginError}}</p><p v-else-if="restoreOrigin" :class="restoreOrigin.occupied?'banner error':'banner notice'" role="status">{{restoreOrigin.occupied?'原目录已有同名项目。':'已核对原目录，本次读取未发现同名项目。'}}</p><template v-if="lanzouRestorePreview">
  <p v-if="lanzouRestorePreview.evidence==='root'" class="banner notice" role="status">预检通过，根目录未发现同名或同标识项目。</p>
  <p v-else-if="lanzouRestorePreview.evidence==='position'" class="banner notice" role="status">原目录可用。本次未能取得可核对的文件清单。</p>
  <details v-else open class="operation-details">
   <summary>{{lanzouRestorePreview.evidence==='local'?'移入前本机清单':'还原前回收站网页清单'}} · {{lanzouRestorePreview.files?.length??0}} 项</summary>
   <ul v-if="lanzouRestorePreview.files?.length" class="recycle-batch-preview"><li v-for="(file,index) in lanzouRestorePreview.files" :key="index">{{file}}</li></ul>
  </details>
 </template>
 <div class="share-history-actions">
  <button v-if="isLanzou" class="secondary" :disabled="busy||blocked" @click="checkLanzouRestore">{{busy?'正在预检…':'只读预检'}}</button>
  <button v-if="isGuangya" class="secondary" :disabled="busy||blocked" @click="checkGuangyaRestoreOrigin">{{busy?'正在核对…':'检查原目录'}}</button><button v-if="restoreOrigin" class="secondary" :disabled="busy||blocked" @click="openGuangyaRestoreOrigin">打开原目录</button><button class="secondary" :disabled="busy" @click="cancelRestore">暂不还原</button><button v-if="canRestoreDirectly(confirmTarget)" class="secondary" :disabled="blocked" @click="choosingRestoreFolder=true">{{restoreDestination?'更改还原目录…':'选择还原目录…'}}</button><button v-if="restoreDestination" class="secondary" @click="useOriginalRestoreDestination">使用原位置</button><button class="primary" :disabled="busy||blocked||restoreOrigin?.occupied" @click="restore()">确认还原</button><button v-if="!restoreDestination&&canRestoreThenMove(confirmTarget)" class="secondary" :disabled="busy||blocked||restoreOrigin?.occupied" @click="restore(true)">还原后选择移动目录…</button></div></section></div>
 </section>
</template>
