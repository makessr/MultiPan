<script setup lang="ts">
import {computed,nextTick,onMounted,onUnmounted,ref,watch} from 'vue'
import {trapDialogFocus} from './dialogFocus'
import {snapshotMaxFile,snapshotMaxBatch} from './relayLimits'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {Account,CloudEntry,FilePage,RelayInput,RelayJob,RelayPreflight} from '../bindings/multipan/internal/transfer/models'
import FolderPicker from './FolderPicker.vue'
const props=defineProps<{accounts:Account[];blocked?:boolean;fileCopy?:{inputs:RelayInput[];directory:boolean}|null}>()
const emit=defineEmits<{copyConsumed:[]}>()
const available=computed(()=>props.accounts.filter(a=>['webdav','ctdav','pan123','baidu','quark','guangya'].includes(a.kind)))
const sourceId=ref(''),targetId=ref(''),sourceFolder=ref('/'),targetFolder=ref('/'),sourcePath=ref('根目录'),targetPath=ref('根目录')
const targets=computed(()=>available.value.filter(a=>a.id!==sourceId.value||['quark','guangya'].includes(a.kind)))
const source=computed(()=>available.value.find(a=>a.id===sourceId.value)),target=computed(()=>targets.value.find(a=>a.id===targetId.value))
const sameAccount=computed(()=>!!source.value&&source.value.id===target.value?.id)
const sameFolder=computed(()=>sameAccount.value&&sourceFolder.value===targetFolder.value)
const canReadCloudOriginal=computed(()=>['quark','guangya'].includes(source.value?.kind??''))
const readCloudOriginal=ref(false)
const page=ref<FilePage|null>(null),selection=ref<string[]>([]),query=ref(''),busy=ref(false),error=ref(''),message=ref(''),picker=ref<'source'|'target'|''>(''),jobs=ref<RelayJob[]>([]),jobBusy=ref(''),jobPage=ref(1)
const preflight=ref<RelayPreflight|null>(null)
const formBusy=computed(()=>busy.value||!!preflight.value)
let disposed=false,generation=0,timer:ReturnType<typeof setTimeout>|undefined,polling=false
const directory=ref<CloudEntry|null>(null)
const moveTarget=ref<RelayJob|null>(null),moveText=ref(''),moveAgreed=ref(false),moveDialog=ref<HTMLElement|null>(null)
let moveTrigger:HTMLElement|null=null
function relayMovesToRecycle(kind:string|undefined){return kind==='pan123'||kind==='quark'||kind==='baidu'}
const moveToRecycle=computed(()=>relayMovesToRecycle(moveTarget.value?.sourceKind))
const moveConfirmation=computed(()=>moveToRecycle.value?'移入回收站':'删除源文件')
const moveSelection=ref<string[]>([]),moveRows=ref<RelayJob[]>([])
const selectedMoves=computed(()=>jobs.value.filter(j=>j.canMove&&moveSelection.value.includes(j.id)))
const canMoveStart=computed(()=>{
 const row=moveTarget.value
 if(disposed||props.blocked||formBusy.value||jobBusy.value||!row||!moveRows.value.length||moveText.value!==moveConfirmation.value||!moveAgreed.value)return false
 const current=jobs.value.find(j=>j.id===row.id)
 if(row.folder)return current?.moveFileIds?.join(',')===moveRows.value.map(j=>j.id).join(',')
 return moveRows.value.every(item=>jobs.value.some(j=>j.id===item.id&&j.canMove))
})
function sameMovePair(a:RelayJob,b:RelayJob){return a.sourceAccountId===b.sourceAccountId&&a.targetAccountId===b.targetAccountId}
function canSelectMove(row:RelayJob){const first=selectedMoves.value[0];return !!row.canMove&&(!first||sameMovePair(first,row))&&(selectedMoves.value.length<20||moveSelection.value.includes(row.id))}
function toggleMove(row:RelayJob,checked:boolean){
 if(checked&&!canSelectMove(row))return
 moveSelection.value=selectedMoves.value.filter(j=>j.id!==row.id).map(j=>j.id)
 if(checked)moveSelection.value.push(row.id)
}
async function prepareMove(row:RelayJob,event?:Event,rows?:RelayJob[]){
 if(disposed||props.blocked||formBusy.value||jobBusy.value||picker.value||directory.value||moveTarget.value)return
 const files=row.folder?row.moveFileIds?.map(id=>jobs.value.find(j=>j.id===id)).filter((j):j is RelayJob=>!!j):rows??[row]
 if(!files?.length||row.folder&&files.length!==row.moveFileIds?.length||!row.folder&&!files.every(j=>j.canMove&&sameMovePair(j,row)))return
 moveTrigger=event?.currentTarget as HTMLElement|null
 moveTarget.value={...row}
 moveRows.value=files.map(j=>({...j}))
 moveText.value=''
 moveAgreed.value=false
 error.value=''
 await nextTick()
 moveDialog.value?.focus()
}
async function cancelMove(){if(jobBusy.value)return;moveTarget.value=null;moveRows.value=[];moveText.value='';moveAgreed.value=false;await nextTick();moveTrigger?.focus()}
async function startMove(){
 if(!canMoveStart.value||!moveTarget.value)return
 const row=moveTarget.value,ids=moveRows.value.map(j=>j.id)
 jobBusy.value=row.id;error.value=''
 try{
  if(!row.folder&&ids.length===1)await api.MoveCloudRelay(row.id,moveText.value)
  else await api.MoveCloudRelays(ids,row.folder?row.id:'',moveText.value)
  if(!disposed){moveTarget.value=null;moveRows.value=[];moveSelection.value=moveSelection.value.filter(id=>!ids.includes(id));moveText.value='';moveAgreed.value=false;message.value='已开始逐项核对并搬运。核对失败或结果待确认时，剩余文件会保留。';clearTimeout(timer);await loadJobs()}
 }catch(e){if(!disposed)error.value=String(e)}finally{if(!disposed)jobBusy.value=''}
}
function moveAccount(row:RelayJob,end:'source'|'target'){
 const id=end==='source'?row.sourceAccountId:row.targetAccountId
 const account=props.accounts.find(a=>a.id===id)
 return account?accountLabel(account):end==='source'?historyAccount(row.sourceKind,row.sourceName):historyAccount(row.targetKind,row.targetName)
}
const supportsDirectories=computed(()=>!!source.value&&!!target.value&&!sameFolder.value&&!(sameAccount.value&&source.value.kind==='guangya'))
const directories=computed(()=>supportsDirectories.value?(page.value?.entries??[]).filter(e=>e.folder&&e.name.toLocaleLowerCase().includes(query.value.toLocaleLowerCase())):[])
const files=computed(()=>(page.value?.entries??[]).filter(e=>!e.folder&&e.size!=null&&e.size>=0&&e.size<=(readCloudOriginal.value?snapshotMaxFile:8*1024**3)).sort((a,b)=>a.name.localeCompare(b.name,'zh-CN',{numeric:true})))
const filtered=computed(()=>files.value.filter(e=>e.name.toLocaleLowerCase().includes(query.value.toLocaleLowerCase())))
const chosen=computed(()=>files.value.filter(e=>selection.value.includes(e.id)))
const total=computed(()=>chosen.value.reduce((n,e)=>n+(e.size??0),0))
const unsupportedEmptyTarget=computed(()=>['baidu','guangya','quark'].includes(target.value?.kind??''))
const blockedEmpty=computed(()=>unsupportedEmptyTarget.value&&chosen.value.some(e=>e.size===0))
const exceedsSnapshotTotal=computed(()=>readCloudOriginal.value&&total.value>snapshotMaxBatch)
const ready=computed(()=>!moveTarget.value&&!directory.value&&!blockedEmpty.value&&!exceedsSnapshotTotal.value&&!props.blocked&&!formBusy.value&&!picker.value&&!!source.value&&!!target.value&&!sameFolder.value&&chosen.value.length>0&&chosen.value.length<=20)
const canPause=computed(()=>formBusy.value||jobs.value.some(j=>['queued','downloading','ready','uploading','verifying'].includes(j.state)||!!j.phase))
const visibleJobs=computed(()=>[...jobs.value].reverse().slice((jobPage.value-1)*50,jobPage.value*50))
function accountName(a?:Account|null){if(!a)return '';const name=a.name?.trim()||'未命名账号';const same=props.accounts.filter(row=>row.kind===a.kind&&(row.name?.trim()||'未命名账号')===name);return same.length>1?name+'（账号 '+(same.findIndex(row=>row.id===a.id)+1)+'）':name}
function accountLabel(a?:Account|null){if(!a)return '账号未选';const provider=({webdav:'WebDAV',ctdav:'诚通 WebDAV',pan123:'123 云盘',baidu:'百度网盘',quark:'夸克网盘',guangya:'光鸭云盘',lanzou:'蓝奏云',ctfile:'诚通网盘',ctweb:'诚通网盘（网页版）'} as Record<string,string>)[a.kind]??'其他网盘';return provider+' · '+accountName(a)}
function historyAccount(kind:string|undefined,name:string){const provider=({webdav:'WebDAV',ctdav:'诚通 WebDAV',pan123:'123 云盘',baidu:'百度网盘',quark:'夸克网盘',guangya:'光鸭云盘','legacy-dav':'历史 DAV'} as Record<string,string>)[kind??'']??'类型未记录';return provider+' · '+(name?.trim()||'未命名账号')}
function bytes(n:number){return n>=1024*1024*1024?(n/1024/1024/1024).toFixed(2)+' GiB':n>=1024*1024?(n/1024/1024).toFixed(1)+' MiB':n>=1024?(n/1024).toFixed(1)+' KiB':n+' B'}
function label(s:string){return ({queued:'等待中',downloading:'读取源文件',ready:'本机副本完整',uploading:'上传目标',verifying:'校验中',verified:'内容已核对',unknown:'目标待核对',moving:'搬运处理中',move_unknown:'搬运删除待核对',moved:'搬运已核对',paused:'已暂停',failed:'未提交目标'} as Record<string,string>)[s]??s}
function toggle(id:string,checked:boolean){if(busy.value||props.blocked||picker.value)return;if(!checked)selection.value=selection.value.filter(x=>x!==id);else if(selection.value.length<20&&!selection.value.includes(id))selection.value.push(id)}
async function loadSource(){
 if(disposed||busy.value||props.blocked||!source.value)return
 directory.value=null
 const stamp=++generation,id=sourceId.value,folder=sourceFolder.value
 const list=readCloudOriginal.value?api.ListRelaySnapshotSourceFiles:api.ListRelaySourceFiles
 busy.value=true;page.value=null;selection.value=[];error.value=''
 try{
  const p=await list(id,folder)
  if(!disposed&&stamp===generation&&id===sourceId.value&&folder===sourceFolder.value)page.value=p
 }catch(e){if(!disposed&&stamp===generation)error.value=String(e)}
 finally{if(!disposed)busy.value=false}
}
function changeSourceReadMode(event:Event){
 if(disposed||busy.value||props.blocked||picker.value||!canReadCloudOriginal.value)return
 readCloudOriginal.value=(event.target as HTMLInputElement).checked
 void loadSource()
}
function canCopyDirectory(row:CloudEntry){return supportsDirectories.value&&(!sameAccount.value||row.id!==targetFolder.value)}
function prepareDirectory(row:CloudEntry){if(disposed||busy.value||props.blocked||picker.value||!canCopyDirectory(row)||!page.value?.entries?.some(e=>e.folder&&e.id===row.id))return;directory.value=row;error.value='';message.value=''}
async function queueDirectory(){const row=directory.value;if(disposed||!row||props.blocked||formBusy.value||!canCopyDirectory(row))return;busy.value=true;error.value='';message.value='';const stamp=++generation;const input={sourceAccountId:sourceId.value,parentId:sourceFolder.value,entryId:row.id,name:row.name,size:0,targetAccountId:targetId.value,destinationId:targetFolder.value};try{const submit=readCloudOriginal.value?api.QueueCloudRelaySnapshotDirectory:api.QueueCloudRelayDirectory;const n=await submit(input);if(!disposed&&stamp===generation){directory.value=null;message.value='已创建 '+n+' 条目录与文件记录，按预检清单逐项复制，源目录保留。';clearTimeout(timer);await loadJobs()}}catch(e){if(!disposed&&stamp===generation)error.value=String(e)}finally{if(!disposed)busy.value=false}}
function pick(which:'source'|'target'){if(busy.value||props.blocked)return;directory.value=null;if(which==='source'?!source.value:!target.value)return;picker.value=which}
async function folderChosen(id:string,path:string){const which=picker.value;picker.value='';if(which==='source'){sourceFolder.value=id;sourcePath.value=path;await loadSource()}else if(which==='target'){targetFolder.value=id;targetPath.value=path}}
async function loadJobs(){
 if(disposed||polling)return
 polling=true
 try{
  const [rows,progress]=await Promise.all([api.CloudRelayJobs(),api.CloudRelayPreflight()])
  if(!disposed){
   jobs.value=rows??[]
   preflight.value=progress
   jobPage.value=Math.min(jobPage.value,Math.max(1,Math.ceil(jobs.value.length/50)))
  }
 }catch(e){if(!disposed)error.value='读取中转记录失败：'+String(e)}
 finally{polling=false;if(!disposed)timer=setTimeout(loadJobs,1200)}
}
async function queue(){if(disposed||!ready.value)return;busy.value=true;error.value='';message.value='';const inputs=chosen.value.map(e=>({sourceAccountId:sourceId.value,parentId:sourceFolder.value,entryId:e.id,name:e.name,size:e.size??0,targetAccountId:targetId.value,destinationId:targetFolder.value}));try{const submit=readCloudOriginal.value?api.QueueCloudRelaySnapshots:api.QueueCloudRelays;const n=await submit(inputs);if(!disposed){selection.value=[];message.value='已创建 '+n+' 项中转复制。任务在本机后台处理，源文件保留。';clearTimeout(timer);await loadJobs()}}catch(e){if(!disposed)error.value=String(e)}finally{if(!disposed)busy.value=false}}
async function control(action:'resume'|'check'|'check-move'|'clear'|'pause',row?:RelayJob){if(disposed||jobBusy.value||props.blocked||busy.value&&action!=='pause')return;if(action!=='pause'&&(!row||!(action==='resume'?row.canResume:action==='check'?row.canCheck:action==='check-move'?row.canCheckMove:row.canClear)))return;jobBusy.value=row?.id??'all';error.value='';try{if(action==='pause')await api.PauseCloudRelays();else if(action==='resume')await api.ResumeCloudRelay(row!.id);else if(action==='check')await api.CheckCloudRelay(row!.id);else if(action==='check-move')await api.CheckCloudRelayMove(row!.id);else await api.ClearCloudRelay(row!.id);if(!disposed){message.value=action==='check-move'?'已开始只读核对搬运原结果，不会再次删除源文件。':action==='check'?'已开始只读核对，不会再次上传。':action==='pause'?'已请求停止及取消原件预读。已提交目标的项目保留待核对。':action==='clear'?'已清理本机记录和自有缓存，云端文件保留。':'已继续，先检查原源文件版本。';clearTimeout(timer);await loadJobs()}}catch(e){if(!disposed)error.value=String(e)}finally{if(!disposed)jobBusy.value=''}}
watch(sourceId,()=>{moveTarget.value=null;generation++;directory.value=null;page.value=null;selection.value=[];readCloudOriginal.value=false;sourceFolder.value=source.value?.kind==='guangya'?'root':['pan123','quark'].includes(source.value?.kind??'')?'0':'/';sourcePath.value='根目录';query.value=''})
watch(targetId,()=>{moveTarget.value=null;generation++;directory.value=null;targetFolder.value=target.value?.kind==='guangya'?'root':['pan123','quark'].includes(target.value?.kind??'')?'0':'/';targetPath.value='根目录'})
watch(target,account=>{if(!account)targetId.value=''})
watch(()=>JSON.stringify(props.accounts.map(a=>[a.id,a.kind,a.endpoint,a.username])),()=>{moveTarget.value=null;generation++;directory.value=null;page.value=null;selection.value=[];picker.value='';if(!source.value)sourceId.value='';if(!target.value)targetId.value=''})
watch(()=>props.blocked,value=>{if(value)moveTarget.value=null})
async function submitFileCopy(inputs:RelayInput[],directory:boolean){
 if(disposed||busy.value||props.blocked||!inputs.length)return
 busy.value=true
 try{
  const count=directory?await api.QueueCloudRelaySnapshotDirectory(inputs[0]):await api.QueueCloudRelaySnapshots(inputs)
  if(!disposed){
   message.value=`已加入 ${count} 条中转记录，源项目保留。`
   clearTimeout(timer)
   await loadJobs()
  }
 }catch(e){if(!disposed)error.value=String(e)}
 finally{if(!disposed)busy.value=false}
}
onMounted(()=>{
 void loadJobs()
 if(props.fileCopy){
  const {inputs,directory}=props.fileCopy
  emit('copyConsumed')
  void submitFileCopy(inputs,directory)
 }
})
onUnmounted(()=>{disposed=true;generation++;clearTimeout(timer)})
</script>
<template>
<section class="relay-panel">
 <div class="heading"><h1>网盘中转</h1><button class="secondary" :disabled="blocked||!!jobBusy||!canPause" @click="control('pause')">停止全部中转</button></div>

 <p v-if="error&&!moveTarget" class="banner error" role="alert">{{error}}</p><p v-if="message" class="banner notice" role="status">{{message}}</p>
 <div class="relay-form">
  <fieldset>
   <legend>来源</legend>
   <select aria-label="来源账号" v-model="sourceId" :disabled="blocked||formBusy||!!picker"><option value="">选择网盘账号</option><option v-for="a in available" :key="a.id" :value="a.id" :title="accountLabel(a)">{{accountLabel(a)}}</option></select>
   <div class="relay-path"><button class="secondary" aria-label="选择来源目录" :disabled="blocked||formBusy||!source" @click="pick('source')">选择目录</button><button class="secondary" :disabled="blocked||formBusy||!source" @click="loadSource">读取文件</button><span :title="sourcePath">{{sourcePath}}</span></div>
  </fieldset>
  <fieldset>
   <legend>目标</legend>
   <select aria-label="目标账号" v-model="targetId" :disabled="blocked||formBusy||!!picker"><option value="">选择网盘账号</option><option v-for="a in targets" :key="a.id" :value="a.id" :title="accountLabel(a)">{{accountLabel(a)}}{{a.id===sourceId?'（同账号复制）':''}}</option></select>
   <div class="relay-path"><button class="secondary" aria-label="选择目标目录" :disabled="blocked||formBusy||!target" @click="pick('target')">选择目录</button><span :title="targetPath">{{targetPath}}</span></div>
  </fieldset>
 </div>
 <p v-if="available.length<2&&!available.some(a=>a.kind==='quark')" class="recycle-note">请添加支持中转的来源和目标账号。</p>
 <p v-if="sameFolder" class="recycle-note">同账号复制请选择不同的目标目录。</p>
 <template v-if="canReadCloudOriginal">
  <label class="share-switch"><input type="checkbox" :checked="readCloudOriginal" :disabled="blocked||formBusy||!!picker" @change="changeSourceReadMode"/>读取网盘原件用于核对</label>
  <p class="recycle-note" v-if="readCloudOriginal">单文件最多 8 GiB，整批或目录合计最多 16 GiB。预读最长 6 小时，核对完整内容后入队，可用「停止全部中转」取消。</p>
 </template>
 <label v-if="page" class="relay-search">来源文件<input v-model="query" type="search" placeholder="筛选文件名" :disabled="formBusy||blocked"/></label>
 <div v-if="preflight" class="relay-preflight" aria-label="网盘原件预读">
  <p role="status">{{preflight.currentFile?`正在读取 ${preflight.completed+1} / ${preflight.files} · ${preflight.currentFile}`:preflight.files?'正在核对预读结果…':'正在检查中转目录…'}}</p>
  <template v-if="preflight.totalBytes>0">
   <progress :value="preflight.readBytes" :max="preflight.totalBytes" aria-label="原件预读字节进度"></progress>
   <small>{{bytes(preflight.readBytes)}} / {{bytes(preflight.totalBytes)}}</small>
  </template>
 </div>
 <p v-else-if="busy" role="status">正在读取或创建队列…</p>
 <p v-if="target?.kind==='guangya'||target?.kind==='quark'" class="recycle-note">上传前会核对目标目录是否有同名项目。中转期间请避免在其他客户端向同一目录写入同名文件。</p>
 <div class="relay-files" v-if="page"><label v-for="e in filtered" :key="e.id"><input type="checkbox" :checked="selection.includes(e.id)" :disabled="formBusy||blocked||(selection.length>=20||unsupportedEmptyTarget&&e.size===0)&&!selection.includes(e.id)" @change="toggle(e.id,($event.target as HTMLInputElement).checked)"/><span>{{e.name}}</span><small>{{bytes(e.size??0)}}</small></label><p v-if="!filtered.length">{{readCloudOriginal?'没有可核对且不超过 8 GiB 的文件。':['quark','guangya'].includes(source?.kind??'')?'没有本机上传指纹匹配的文件。':'没有符合条件的文件（文件夹和超过8 GiB的文件不列入）。'}}</p></div>
 <p v-if="exceedsSnapshotTotal" class="banner error" role="alert">网盘原件预读整批最多 16 GiB，请减少选择。</p>
 <p v-if="blockedEmpty" class="banner error" role="alert">所选目标暂不支持空文件。请取消选择空文件。</p><div class="relay-summary"><p v-if="chosen.length">已选 {{chosen.length}} 项 · {{bytes(total)}} · 保留源文件</p><button class="primary" :disabled="!ready" @click="queue">创建中转复制队列</button></div>
 <div v-if="supportsDirectories&&page" class="relay-files"><strong>可复制的目录</strong><div v-for="e in directories" :key="e.id" class="relay-actions"><span>{{e.name}}</span><button class="secondary" :disabled="blocked||formBusy||!!picker||!canCopyDirectory(e)" @click="prepareDirectory(e)">复制此目录…</button></div><p v-if="!directories.length">当前目录没有可选子目录。</p></div>
 <section v-if="directory" class="share-history-card" role="alertdialog" aria-label="确认复制目录"><strong>{{directory.name}} · {{accountLabel(source)}} → {{accountLabel(target)}} / {{targetPath}}</strong><p>整棵目录先预检，最多8层、20个目录（含根）及20个文件，单文件8 GiB，全部中转记录合计32 GiB。超限、读取不完整、缺少可靠版本或目标同名则整批不创建。DAV须提供强 ETag，123 和百度须提供可核验的原内容 MD5，夸克和光鸭来源的每个文件须匹配本机上传指纹，或开启原件预读建立指纹。百度、夸克和光鸭目标含空文件时整棵拒绝。两端请选择不重叠的目录。确认后按清单逐项创建目录和复制文件，源保留；之后新增的源文件不包含在清单中。</p><p>这是逐项操作，失败可能留下部分目录与文件，不自动回滚或删除。目录创建未确认时不重发，也不合并已有目录；先核对父目录，再继续子项。</p><div class="relay-actions"><button class="primary" :disabled="blocked||formBusy||!supportsDirectories" @click="queueDirectory">确认预检并创建目录队列</button><button class="secondary" :disabled="busy" @click="directory=null">取消</button></div></section>
 <details class="operation-details"><summary>中转限制与校验</summary><p>中转先下载到本机，再上传到目标网盘，默认保留源文件。每批最多 20 项，单文件最多 8 GiB，本机记录合计最多 32 GiB。百度、夸克和光鸭目标暂不接收空文件。</p>
  <p>DAV 来源需要强 ETag，123 和百度来源须能核验原始 MD5。发现目标同名时停止，并发写入仍可能产生需要核对的项目。完整回读目标并核对 SHA-256 和源版本后才确认内容，会消耗下载流量与网盘额度。</p>
  <p>缓存以明文保存在 %APPDATA%\MultiPan\relay-cache，下载时需保留至少 512 MiB 磁盘余量。退出程序后需手动继续或核对。待核对项目保留缓存，禁止重复上传和清理记录。</p></details>
 <h2>中转记录 <span v-if="jobs.length">（{{jobs.length}}）</span></h2>
 <div v-if="selectedMoves.length" class="relay-actions">
  <button class="secondary" :disabled="blocked||formBusy||!!jobBusy||!!moveTarget" @click="prepareMove(selectedMoves[0],$event,selectedMoves)">搬运所选文件（{{selectedMoves.length}}）…</button>
  <button class="secondary" :disabled="!!moveTarget" @click="moveSelection=[]">取消选择</button>
 </div>
 <p v-if="!jobs.length" class="recycle-note">暂无中转任务</p>
 <article v-for="j in visibleJobs" :key="j.id" class="share-history-card"><div class="share-history-card-heading"><label v-if="j.canMove" class="relay-move-select"><input type="checkbox" :aria-label="'选择搬运 '+j.name" :checked="moveSelection.includes(j.id)" :disabled="blocked||formBusy||!!jobBusy||!!moveTarget||!canSelectMove(j)" @change="toggleMove(j,($event.target as HTMLInputElement).checked)"/><strong>{{j.name}}</strong></label><strong v-else>{{j.name}}</strong><span>{{j.folder&&j.state==='verified'?'目录创建已核对':label(j.state)}}</span></div><p :title="historyAccount(j.sourceKind,j.sourceName)+' → '+historyAccount(j.targetKind,j.targetName)+' / '+j.destination">{{historyAccount(j.sourceKind,j.sourceName)}} → {{historyAccount(j.targetKind,j.targetName)}} / {{j.destination}} · {{bytes(j.size)}}</p><p v-if="['moving','move_unknown','moved'].includes(j.state)">来源原位置：{{j.sourcePath}}</p><p v-if="j.phase" role="status">{{j.phase}} · {{bytes(j.progress)}} / {{bytes(j.size)}}</p><p>{{j.message}}</p><p v-if="j.folder&&j.state==='verified'&&!j.canClear">子项有独立记录；清理时请先处理子项记录。</p><small>本机缓存：{{bytes(j.cacheBytes)}}<template v-if="j.sha256"> · SHA256：{{j.sha256}}</template></small><div class="relay-actions"><button v-if="j.canResume" class="secondary" :disabled="blocked||formBusy||!!jobBusy" @click="control('resume',j)">继续未提交任务</button><button v-if="j.canCheck" class="secondary" :disabled="blocked||formBusy||!!jobBusy" @click="control('check',j)">只读核对目标</button><button v-if="j.canMove" class="secondary" :disabled="blocked||formBusy||!!jobBusy||!!moveTarget" @click="prepareMove(j,$event)">{{relayMovesToRecycle(j.sourceKind)?'完成搬运并移入回收站…':'完成搬运并删除源文件…'}}</button><button v-if="j.moveFileIds?.length" class="secondary" :disabled="blocked||formBusy||!!jobBusy||!!moveTarget" @click="prepareMove(j,$event)">搬运目录内文件（{{j.moveFileIds.length}}）…</button><button v-if="j.canCheckMove" class="secondary" :disabled="blocked||formBusy||!!jobBusy" @click="control('check-move',j)">只读核对搬运结果</button><button v-if="j.canClear" class="secondary" :disabled="blocked||formBusy||!!jobBusy" @click="control('clear',j)">清理记录及本机缓存</button></div></article>
 <footer v-if="jobs.length>50" class="recycle-footer"><span>第 {{jobPage}} 页</span><button class="secondary" :disabled="jobPage<=1" @click="jobPage--">上一页</button><button class="secondary" :disabled="jobPage*50>=jobs.length" @click="jobPage++">下一页</button></footer>
 <div v-if="moveTarget" class="modal-backdrop" @click.self="cancelMove" @keydown.esc.prevent="cancelMove"><section ref="moveDialog" tabindex="-1" class="operation-dialog" role="dialog" aria-modal="true" aria-labelledby="relay-move-title" @keydown.tab="trapDialogFocus"><h2 id="relay-move-title">{{moveToRecycle?'完成搬运并移入回收站':'完成搬运并删除源文件'}}</h2><p>{{moveAccount(moveTarget,'source')}} → {{moveAccount(moveTarget,'target')}} · {{moveRows.length}} 个文件</p><p v-if="moveTarget.folder">源目录及新加入的文件会保留。</p><ul class="relay-move-files"><li v-for="row in moveRows" :key="row.id"><strong>{{row.name}} · {{bytes(row.size)}}</strong><div>来源 {{row.sourcePath}}</div><div>目标 {{row.destination}}</div></li></ul><p class="banner error">{{moveToRecycle?'重新核对三份内容后，源文件将移入来源网盘的回收站。':'DAV 源文件会按原版本条件删除，可能无法通过回收站还原。'}}</p><p>将额外完整读取目标两次、源文件一次并校验本机缓存，使用正常下载额度和合计限速，可能耗时。</p><label class="move-confirm">输入“{{moveConfirmation}}”<input v-model="moveText" autocomplete="off" aria-label="搬运删除确认文字"/></label><label class="move-confirm"><input v-model="moveAgreed" type="checkbox"/>{{moveToRecycle?'我已核对来源和目标，同意将源文件移入来源网盘的回收站':'我已核对来源和目标，理解只保留目标与本机缓存，删除可能无法还原'}}</label><p>按清单逐项处理。核对失败或结果待确认时停止，剩余源文件保留。</p><p v-if="error" class="banner error" role="alert">{{error}}</p><div class="relay-actions"><button class="secondary" :disabled="!!jobBusy" @click="cancelMove">取消并保留源文件</button><button class="primary" :disabled="!canMoveStart" @click="startMove">确认开始搬运核对</button></div></section></div>
 <FolderPicker :purpose="picker==='source'?'source':undefined" v-if="picker&&(picker==='source'?source:target)" :account-id="picker==='source'?sourceId:targetId" :account-name="accountName(picker==='source'?source:target)" :kind="(picker==='source'?source:target)!.kind" :initial-folder="picker==='source'?sourceFolder:targetFolder" @close="picker=''" @selected="folderChosen"/>
</section>
</template>
<style scoped>
.relay-move-select{display:inline-flex;align-items:center;gap:8px;min-width:0}
.relay-move-select input{width:16px;height:16px;flex:none;margin:0}
.relay-move-files{max-height:12rem;overflow:auto;overflow-wrap:anywhere;padding-inline-start:1.5rem}
.relay-move-files li+li{margin-top:.5rem}
.move-confirm{display:flex;gap:10px;margin:14px 0;align-items:center}
.move-confirm input:not([type=checkbox]){min-height:38px;font:inherit;padding:6px}
.relay-form{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px;margin:0 0 16px}
.relay-form fieldset{min-width:0;margin:0;border:1px solid var(--line);border-radius:6px;padding:12px}
.relay-form legend{font-weight:600;padding-inline:5px}
.relay-path{display:flex;align-items:center;gap:8px;flex-wrap:wrap;margin-top:10px}
.relay-path span,.relay-panel small{overflow-wrap:anywhere}
.relay-search{display:flex;align-items:center;gap:10px;margin:12px 0}
.relay-form select,.relay-search input{min-height:36px;padding:7px 10px;border:1px solid var(--line);border-radius:6px;background:#fff;color:var(--ink);font:inherit}
.relay-form select{width:100%;text-overflow:ellipsis}
.relay-files{max-height:300px;overflow:auto;margin:12px 0;border:1px solid var(--line);border-radius:6px;padding:10px}
.relay-files label{display:flex;gap:12px;padding:8px}
.relay-files span{flex:1;overflow-wrap:anywhere}
.relay-summary{display:flex;align-items:center;gap:16px;margin:16px 0}
.relay-summary button{margin-left:auto}
.relay-actions{display:flex;gap:8px;flex-wrap:wrap;margin-top:12px}
.relay-preflight progress{width:min(18rem,100%);margin-inline-end:8px;accent-color:var(--accent)}
.relay-panel h2{margin-top:24px}
@media(max-width:760px){.relay-form{grid-template-columns:1fr}}
</style>
