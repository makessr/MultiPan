<script setup lang="ts">
import {computed,nextTick,onMounted,onUnmounted,ref} from 'vue'
import BatchOperationReport from './BatchOperationReport.vue'
import {trapDialogFocus} from './dialogFocus'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {RecycleEntry,RecyclePurgePrepared,RecyclePurgeSelection} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accountId:string;accountName:string;page:number;entries:RecycleEntry[];selections?:RecyclePurgeSelection[];guangya?:boolean;lanzou?:boolean;autoPreview?:boolean}>()
const emit=defineEmits<{close:[];changed:[]}>()
const confirmation=ref(''),agreed=ref(false),busy=ref(false),started=ref(false),stop=ref(false),error=ref(''),status=ref('')
const results=ref<{name:string;state:string;message:string}[]>([])
const blueFolder=computed(()=>props.lanzou&&props.entries.some(row=>row.kind==='folder'))
const requiresPreview=computed(()=>blueFolder.value||props.lanzou&&props.page===0)
const reviewed=ref<RecyclePurgePrepared[]|null>(null)
const canStart=computed(()=>!busy.value&&!started.value&&(!requiresPreview.value||!!reviewed.value)&&agreed.value&&confirmation.value==='永久删除'&&props.entries.length>0&&props.entries.length<=50)
const dialog=ref<HTMLElement|null>(null)
let disposed=false
function label(state:string){return ({confirmed:'已受理并移除',working:'处理中',unknown:'待核对',not_sent:'未提交',skipped:'未执行'} as Record<string,string>)[state]??state}
async function prepare(){
 const entries=props.entries.map(r=>({...r}))
 const rows=props.selections
  ?await api.PrepareCloudRecyclePurgeAcrossPages(props.accountId,props.selections.map(row=>({page:row.page,entry:{...row.entry}})))
  :await api.PrepareCloudRecyclePurge(props.accountId,props.page,entries)
 if(!rows||rows.length!==entries.length||rows.some((r,i)=>!r.handle||r.name!==entries[i].name))throw Error('检查结果与所选项目不一致，未提交')
 return rows
}
async function preview(){
 if(busy.value||started.value||reviewed.value||disposed)return
 busy.value=true;error.value='';status.value='正在读取待删除内容，尚未提交…'
 try{
  const rows=await prepare()
  if(!disposed){reviewed.value=rows;status.value='请核对下方清单，再输入确认文字'}
 }catch(e){if(!disposed){error.value=String(e);status.value='预览未通过，未提交永久删除'}}
 finally{busy.value=false}
}
async function start(){
 if(!canStart.value||disposed)return
 busy.value=true;started.value=true;error.value='';status.value='正在只读检查整批回收站原记录，尚未提交…'
 let submitted=false
 let preflightPassed=false
 try{
  const prepared=reviewed.value??await prepare()
  preflightPassed=true
  for(let i=0;i<prepared.length;i++){
   if(disposed||stop.value)break
   const row=prepared[i];status.value='正在处理 '+(i+1)+' / '+prepared.length+'：'+row.name
   submitted=true
   results.value.push({name:row.name,state:'working',message:'本项结果待核对，请查看本机永久删除记录'})
   const result=await api.PurgeCloudRecycle(row.handle,'永久删除')
   if(disposed)break
   results.value[i]={name:row.name,state:result.state,message:result.message}
   if(result.state!=='confirmed')stop.value=true
  }
  if(!disposed)status.value=stop.value?'已停止后续，请先核对原结果':'本次处理结束，请核对逐项结果'
 }catch(e){if(!disposed){error.value=String(e);status.value=submitted?'处理未完成，后续不再提交。已发送项目请检查本机永久删除记录。':'整批预检未通过，未提交永久删除。'}}
 finally{
  busy.value=false
  if(!disposed){
   const current=results.value.at(-1)
   if(current?.state==='working')current.state='unknown'
   for(const row of props.entries.slice(results.value.length))results.value.push({name:row.name,state:preflightPassed?'skipped':'not_sent',message:'没有发送请求'})
   emit('changed')
  }
 }
}
onMounted(()=>{void nextTick(()=>dialog.value?.focus());if(props.autoPreview&&requiresPreview.value)void preview()})
onUnmounted(()=>{disposed=true;stop.value=true})
</script>
<template>
 <div class="modal-backdrop" @click.self="!busy&&emit('close')" @keydown.esc.prevent="!busy&&emit('close')">
  <section ref="dialog" tabindex="-1" class="operation-dialog recycle-purge-dialog" role="dialog" aria-modal="true" aria-labelledby="purge-title" @keydown.tab="trapDialogFocus">
   <h2 id="purge-title">{{autoPreview&&!reviewed?'核对回收站项目':'永久删除回收站项目'}}</h2><p>网盘：{{accountName}} · 所选 {{entries.length}} 项</p>
   <p class="banner error">永久删除后无法通过回收站还原。<template v-if="entries.some(row=>row.kind==='folder')">文件夹包含内部文件，请逐项核对。</template></p>
   <p v-if="guangya">请避免同时用其他客户端还原所选项目。光鸭按服务器上的当前状态处理删除，同时还原可能使项目重新进入回收站。</p>
   <p v-if="blueFolder">确认前请核对文件夹及其清单。处理期间请避免在其他客户端还原或移动这些项目。</p>
   <ul><li v-for="(r,i) in entries" :key="r.id"><strong>{{r.name}}{{r.kind==='folder'?'（文件夹）':''}}</strong><p><template v-if="selections?.[i]">第 {{selections[i].page}} 页 · </template>{{r.originalPath||'服务商未返回原位置'}}</p><template v-if="blueFolder&&reviewed&&r.kind==='folder'"><ul v-if="reviewed[i]?.folderFiles?.length"><li v-for="(name,j) in reviewed[i]?.folderFiles" :key="j">{{name}}</li></ul><p v-else>空文件夹</p></template></li></ul>
   <template v-if="!started&&(!requiresPreview||reviewed)"><label class="purge-confirm-text">输入“永久删除”以确认<input v-model="confirmation" type="text" aria-label="永久删除确认文字" autocomplete="off"/></label><label class="checkbox-row"><input v-model="agreed" type="checkbox"/>我已核对账号与所选项目，理解删除后无法还原</label></template>
   <p>先整批检查，再逐项提交。停止会等待当前请求返回，已执行项目无法撤销。网络中断或记录消失不能单独证明删除成功；待核对项目不自动重发。</p>
   <p v-if="status" role="status">{{status}}</p><p v-if="error" class="banner error" role="alert">{{error}}</p>
   <ul aria-label="永久删除逐项结果"><li v-for="(r,i) in results" :key="i"><strong>{{r.name}} · {{label(r.state)}}</strong><p>{{r.message}}</p></li></ul>
   <BatchOperationReport v-if="started&&!busy" operation="永久删除回收站项目" :account-name="accountName" :rows="results"/>
   <div class="modal-actions"><button class="secondary" :disabled="busy" @click="emit('close')">{{started?'关闭':'取消'}}</button><button v-if="requiresPreview&&!reviewed" class="secondary" :disabled="busy" @click="preview">{{busy?'正在核对…':error?'重新核对':'预览待删除清单'}}</button><button v-else-if="!started" class="primary" :disabled="!canStart" @click="start">确认永久删除所选项目</button><button v-if="busy&&started" class="secondary" :disabled="stop" @click="stop=true">停止后续处理</button></div>
  </section>
 </div>
</template>
<style scoped>
.purge-confirm-text{display:flex;flex-direction:column;gap:8px;margin-top:18px;font-weight:600}
.purge-confirm-text input{width:100%;min-height:38px;padding:8px 10px;border:1px solid #c9d2df;border-radius:5px;font:inherit}
.checkbox-row{display:flex;align-items:flex-start;gap:9px;line-height:1.65;margin:15px 0}
.checkbox-row input{flex:0 0 auto;margin-top:4px}
.recycle-purge-dialog ul{max-height:230px;overflow:auto;padding-left:23px}
.recycle-purge-dialog li{margin:10px 0}
.recycle-purge-dialog .primary{background:#b42318;border-color:#b42318}
</style>
