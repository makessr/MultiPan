<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref} from 'vue'
import {trapDialogFocus} from './dialogFocus'
import {managedShareText,searchShareLinks,shareLinkSearchKey} from './shareText'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {ManagedShare,ManagedShareLink} from '../bindings/multipan/internal/transfer/models'

type Target=Pick<ManagedShare,'id'|'name'>&{sourcePath?:string}
const props=defineProps<{
 accountName:string;kind:string;targets:Target[];blocked?:boolean;canLocate?:boolean;showResultMessage?:boolean
 readLink?:(handle:string)=>Promise<ManagedShareLink>
 readLinks?:(handles:string[])=>Promise<(ManagedShareLink|null)[]|null>
}>()
const emit=defineEmits<{close:[];locate:[handle:string]}>()
type ReadState='queued'|'reading'|'ready'|'failed'
type Row={handle:string;name:string;sourcePath?:string;state:ReadState;result?:ManagedShareLink;error?:string}
const rows=ref<Row[]>(props.targets.map(row=>({handle:row.id,name:row.name,sourcePath:row.sourcePath,state:'queued'})))
const dialog=ref<HTMLElement|null>(null)
const running=ref(false),copying=ref(false),stopRequested=ref(false),autoFill=ref(true)
const message=ref(''),clipboardError=ref(''),filter=ref('')
const valid=rows.value.length>0&&rows.value.length<=50&&rows.value.every(row=>row.handle&&row.name)&&new Set(rows.value.map(row=>row.handle)).size===rows.value.length
const busy=computed(()=>running.value||copying.value)
const ready=computed(()=>rows.value.flatMap(row=>row.result?[row.result]:[]))
const filterActive=computed(()=>filter.value.trim()!=='')
const visible=computed(()=>{
 if(!filterActive.value)return rows.value
 const keys=searchShareLinks(filter.value)
 const term=filter.value.trim().toLocaleLowerCase()
 return rows.value.filter(row=>keys.size
  ?!!row.result&&keys.has(shareLinkSearchKey(row.result.url))
  :[row.name,row.sourcePath].some(text=>text?.toLocaleLowerCase().includes(term)))
})
const visibleReady=computed(()=>visible.value.flatMap(row=>row.result?[row.result]:[]))
const queued=computed(()=>rows.value.filter(row=>row.state==='queued').length)
const failed=computed(()=>rows.value.filter(row=>row.state==='failed').length)
const labels={queued:'未读取',reading:'读取中',ready:'已取得',failed:'无法读取'}
let disposed=false

function close(){
 if(!busy.value)emit('close')
}
function acceptLink(row:Row,result:ManagedShareLink|null|undefined){
 if(!result?.url||!managedShareText(result,props.kind,true,false))return false
 row.result=result
 row.state='ready'
 return true
}
async function readGroup(group:Row[]){
 for(const row of group){row.state='reading';row.error=undefined}
 try{
  const results=await (props.readLinks??api.ReadManagedQuarkShareLinks)(group.map(row=>row.handle))
  if(disposed)return
  if(results?.length===group.length){
   group.forEach((row,index)=>acceptLink(row,results[index]))
  }
 }catch{
  // Older provider responses can still be read through the original endpoint.
 }finally{
  if(!disposed)for(const row of group)if(row.state==='reading')row.state='queued'
 }
}
async function readSingle(row:Row){
 row.state='reading'
 row.error=undefined
 try{
  const result=await (props.readLink??api.ReadManagedCloudShare)(row.handle)
  if(disposed)return
  if(!acceptLink(row,result))throw Error('网盘未返回可用的原链接')
 }catch(error){
  if(disposed)return
  row.state='failed'
  row.error=String(error)
 }
}
async function run(state:'queued'|'failed'='queued'){
 if(disposed||!valid||busy.value||props.blocked)return
 const targets=rows.value.filter(row=>row.state===state)
 if(!targets.length)return
 running.value=true
 stopRequested.value=false
 message.value=''
 clipboardError.value=''
 let groupSize=1
 if(state==='queued'){
  if(props.readLinks)groupSize=5
  else if(props.kind==='quark'&&!props.readLink)groupSize=10
 }
 try{
  for(let offset=0;offset<targets.length;offset+=groupSize){
   if(disposed||props.blocked||stopRequested.value)break
   const group=targets.slice(offset,offset+groupSize)
   if(group.length>1)await readGroup(group)
   for(const row of group){
    if(disposed||props.blocked||stopRequested.value)break
    if(row.state!=='ready')await readSingle(row)
   }
  }
 }finally{
  running.value=false
 }
}
function resultLink(result:ManagedShareLink){
 return managedShareText(result,props.kind,true,autoFill.value)
}
function locate(row:Row){
 if(disposed||busy.value||props.blocked||!props.canLocate||!visible.value.includes(row)||!row.result)return
 emit('locate',row.handle)
}
async function copy(linksOnly=false){
 const selected=visibleReady.value
 if(disposed||busy.value||props.blocked||!selected.length)return
 const text=selected.map(result=>managedShareText(result,props.kind,linksOnly,autoFill.value)).join(linksOnly?'\n':'\n\n')
 copying.value=true
 message.value=''
 clipboardError.value=''
 try{
  await api.CopyDownloadReport(text)
  if(!disposed)message.value='已复制 '+selected.length+' 条'+(linksOnly?'分享链接':'完整分享信息')
 }catch(error){
  if(!disposed)clipboardError.value=String(error)
 }finally{
  copying.value=false
 }
}
onMounted(()=>dialog.value?.focus())
onUnmounted(()=>{disposed=true})
</script>

<template>
 <div class="modal-backdrop" @click.self="close">
  <section ref="dialog" tabindex="-1" class="operation-dialog share-dialog batch-managed-share-dialog" role="dialog" aria-modal="true" aria-labelledby="batch-managed-share-title" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="close">
   <div class="modal-heading">
    <h2 id="batch-managed-share-title">批量读取原分享</h2>
    <button class="secondary" :disabled="busy" @click="close">关闭</button>
   </div>
   <p>{{accountName}}</p>
   <p v-if="!valid" class="banner error" role="alert">请选择 1 至 50 条不同的分享记录。</p>
   <p class="preview-note">关闭窗口后清除本次读取结果。</p>
   <p role="status">已取得 {{ready.length}} / {{rows.length}} 条，失败 {{failed}} 条，未读取 {{queued}} 条。<template v-if="running&&stopRequested"> 正在等当前请求结束。</template></p>
   <label v-if="ready.length">筛选结果 <input v-model="filter" type="search" placeholder="项目名称、路径或原链接" :disabled="copying"/></label>
   <p v-if="filterActive&&!visible.length" role="status">本次清单暂无匹配项。</p>
   <ol v-if="visible.length" class="managed-share-read-rows">
    <li v-for="row in visible" :key="row.handle">
     <div class="share-history-card-heading"><strong>{{row.name}}</strong><span class="muted">{{labels[row.state]}}</span></div>
     <p v-if="row.sourcePath" class="muted">{{row.sourcePath}}</p>
     <template v-if="row.result">
      <label>原链接<input :value="resultLink(row.result)" :aria-label="row.name+'的原链接'" readonly/></label>
      <p>提取码 {{row.result.password||'无'}} · {{row.result.expiry}}</p>
      <p v-if="showResultMessage&&row.result.message" class="muted">{{row.result.message}}</p>
      <button v-if="canLocate" type="button" class="secondary" :disabled="busy||blocked" @click="locate(row)">定位原目录</button>
     </template>
     <p v-if="row.error" class="error">{{row.error}}</p>
    </li>
   </ol>
   <label v-if="ready.length&&['baidu','guangya'].includes(kind)" class="share-switch"><input v-model="autoFill" type="checkbox" :disabled="busy||blocked"/>复制链接时自动填充提取码</label>
   <div class="share-history-actions">
    <button v-if="running" class="secondary" :disabled="stopRequested" @click="stopRequested=true">停止后续读取</button>
    <button v-else-if="queued" class="primary" :disabled="!valid||busy||blocked" @click="run()">{{queued===rows.length?'读取所选分享':'继续未读取项'}}</button>
    <button v-if="!running&&failed" class="secondary" :disabled="busy||blocked" @click="run('failed')">重读失败项</button>
    <button v-if="visibleReady.length" class="secondary" :disabled="busy||blocked" @click="copy(true)">{{filterActive?'复制筛选出的链接':'只复制链接'}}</button>
    <button v-if="visibleReady.length" class="secondary" :disabled="busy||blocked" @click="copy()">{{filterActive?'复制筛选出的完整信息':'复制完整信息'}}</button>
   </div>
   <p v-if="message" role="status">{{message}}</p>
   <p v-if="clipboardError" class="banner error" role="alert">{{clipboardError}}</p>
  </section>
 </div>
</template>

<style scoped>
.batch-managed-share-dialog{width:min(720px,calc(100vw - 40px));max-height:calc(100vh - 40px);overflow:auto}
.managed-share-read-rows{max-height:42vh;overflow:auto;margin:16px 0;padding-left:28px}
.managed-share-read-rows li{padding:10px 8px;border-bottom:1px solid var(--border,#e4e8ee);overflow-wrap:anywhere}
.managed-share-read-rows label{display:block;margin-top:8px}
.managed-share-read-rows input{width:100%;box-sizing:border-box}
</style>
