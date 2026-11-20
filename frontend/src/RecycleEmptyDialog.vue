<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import BatchOperationReport from './BatchOperationReport.vue'
import {trapDialogFocus} from './dialogFocus'
import {spreadsheetCell} from './spreadsheetCell'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {RecycleEmptyPreview} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accountId:string;accountName:string;serverClear?:boolean}>()
const emit=defineEmits<{close:[];changed:[]}>()
const preview=ref<RecycleEmptyPreview|null>(null),busy=ref(false),started=ref(false),stop=ref(false)
const agreed=ref(false),confirmation=ref(''),error=ref(''),status=ref('')
const copying=ref(false),copyStatus=ref('')
const results=ref<{name:string;state:string;message:string}[]>([])
const query=ref(''),page=ref(1)
const rowsPerPage=25
const filteredItems=computed(()=>{
 const items=preview.value?.items??[]
 const needle=query.value.trim().toLocaleLowerCase()
 return needle?items.filter(row=>[row.name,row.originalPath??''].some(text=>text.toLocaleLowerCase().includes(needle))):items
})
const pageCount=computed(()=>Math.max(1,Math.ceil(filteredItems.value.length/rowsPerPage)))
const visibleItems=computed(()=>filteredItems.value.slice((page.value-1)*rowsPerPage,page.value*rowsPerPage))
watch(query,()=>{page.value=1},{flush:'sync'})
function stepPage(offset:number){
 page.value=Math.max(1,Math.min(pageCount.value,page.value+offset))
}
let disposed=false
const canStart=computed(()=>!busy.value&&!copying.value&&!started.value&&!!preview.value?.token&&preview.value.total>0&&preview.value.total<=1000&&agreed.value&&confirmation.value==='清空回收站')
function label(state:string){return ({confirmed:'已受理并移除',working:'处理中',unknown:'待核对',not_sent:'未提交',skipped:'未执行'} as Record<string,string>)[state]??state}
function validPreview(p:RecycleEmptyPreview){return p&&!!p.token&&Array.isArray(p.items)&&Number.isInteger(p.total)&&p.total>=0&&p.total<=1000&&p.total===p.items.length}
async function load(){
 if(busy.value||copying.value||started.value||disposed)return
 busy.value=true;preview.value=null;query.value='';page.value=1;copyStatus.value='';agreed.value=false;confirmation.value='';error.value='';status.value='正在只读获取整个回收站清单，尚未提交…'
 try{const p=await api.PreviewCloudRecycleEmpty(props.accountId);if(disposed)return;if(!validPreview(p))throw Error('完整清单格式未确认，未创建清空计划');preview.value=p;status.value=p.total?'已读取完整清单，确认前会再次核对；预览10分钟内有效':'已核对回收站为空（本次读取时）'}
 catch(e){if(!disposed){error.value=String(e);status.value='清单未完整确认，没有发送删除请求'}}finally{busy.value=false}
}
async function copyPreview(){
 const snapshot=preview.value
 if(!snapshot?.items?.length||busy.value||copying.value||started.value||disposed)return
 const rows=snapshot.items.map(row=>[
  row.name,row.kind==='folder'?'文件夹':row.kind==='file'?'文件':'未知',
  row.kind==='file'&&typeof row.size==='number'&&Number.isSafeInteger(row.size)&&row.size>=0?String(row.size):'',row.originalPath||'',
 ].map(spreadsheetCell).join('\t'))
 const report=['回收站清空预览 · '+rows.length+' 项','名称\t类型\t大小（字节）\t原位置',...rows].join('\r\n')
 copying.value=true
 copyStatus.value=''
 try{
  await api.CopyDownloadReport(report)
  if(!disposed)copyStatus.value='已复制 '+rows.length+' 项预览记录'
 }catch(e){
  if(!disposed)copyStatus.value='复制预览失败：'+String(e)
 }finally{copying.value=false}
}
async function start(){
 if(!canStart.value||disposed)return
 const p=preview.value!
 busy.value=true;started.value=true;stop.value=false;error.value='';status.value='正在重新读取完整清单，尚未提交…'
 const targets=props.serverClear?[{name:'清空整个回收站'}]:p.items
 let preflightPassed=false
 try{
  const prepared=await api.PrepareCloudRecycleEmpty(p.token,confirmation.value)
  if(props.serverClear){
   if(!Array.isArray(prepared)||prepared.length!==1||!prepared[0]?.handle||prepared[0].name!=='清空整个回收站')throw Error('清空计划格式未确认，未提交')
   if(disposed)return
   preflightPassed=true
   status.value='正在提交光鸭清空任务并核对结果…'
   results.value.push({name:prepared[0].name,state:'working',message:'服务端任务结果待核对'})
   const result=await api.PurgeCloudRecycle(prepared[0].handle,'清空回收站')
   if(!disposed){
    results.value[0]={name:prepared[0].name,state:result.state,message:''}
    status.value=result.message
   }
   return
  }
  if(!Array.isArray(prepared)||prepared.length!==p.total||prepared.some((r,i)=>!r.handle||r.name!==p.items?.[i]?.name))throw Error('检查结果与预览不一致，未提交')
  preflightPassed=true
  for(let i=0;i<prepared.length;i++){
   if(disposed||stop.value)break
   const r=prepared[i];status.value='正在处理 '+(i+1)+' / '+prepared.length+'：'+r.name
   results.value.push({name:r.name,state:'working',message:'本项结果待核对，请查看本机永久删除记录'})
   const result=await api.PurgeCloudRecycle(r.handle,'永久删除')
   if(disposed)break
   results.value[i]={name:r.name,state:result.state,message:result.message}
   if(result.state!=='confirmed')stop.value=true
  }
  if(disposed)return
  if(stop.value){status.value='已停止后续；请检查永久删除记录，不会自动重试';return}
  status.value='正在只读检查回收站是否为空…'
  const final=await api.PreviewCloudRecycleEmpty(props.accountId)
  if(disposed)return
  if(!validPreview(final))throw Error('最终清单未完整确认，请刷新或到官网核对')
  status.value=final.total===0?'已清空回收站（最终读取时），逐项受理与移除均已核对':'预览内项目已处理，回收站仍有 '+final.total+' 项；没有追加删除，请重新预览'
 }catch(e){if(!disposed){error.value=String(e);status.value='清空未完成；后续不再提交，已发送项目请检查本机永久删除记录'}}
 finally{
  busy.value=false
  if(!disposed){
   const current=results.value.at(-1)
   if(current?.state==='working')current.state='unknown'
   for(const row of (targets??[]).slice(results.value.length))results.value.push({name:row.name,state:preflightPassed?'skipped':'not_sent',message:'没有发送请求'})
   emit('changed')
  }
 }
}
onMounted(load)
onUnmounted(()=>{disposed=true;stop.value=true})
</script>
<template>
 <div class="modal-backdrop" @click.self="!busy&&emit('close')" @keydown.esc.prevent="!busy&&emit('close')">
  <section class="operation-dialog recycle-empty-dialog" role="dialog" aria-modal="true" aria-labelledby="empty-title" @keydown.tab="trapDialogFocus">
   <h2 id="empty-title">清空整个回收站</h2><p>网盘：{{accountName}}</p>
   <p class="banner error">以下文件和文件夹将永久删除，无法通过回收站还原。文件夹包含内部全部内容。</p>
   <p>范围是当前账号的整个回收站，包含其他页和被筛选隐藏的项目。预览最多1000项，读取不完整或清单变化时不提交。</p>
   <p v-if="serverClear" class="banner error">光鸭按服务端执行时的整个回收站清空。提交后、任务完成前移入回收站的项目也可能被清除。提交后无法停止任务。</p>
   <p v-else>预览不是原子快照，执行期间新增项目不会追加删除。</p>
   <div v-if="preview" class="share-history-actions">
    <span>完整清单 {{preview.total}} 项（文件夹内部项目不单独计数）</span>
    <button v-if="preview.total&&!started" class="secondary" :disabled="busy||copying" @click="copyPreview">复制完整表格</button>
   </div>
   <p v-if="copyStatus" role="status">{{copyStatus}}</p>
   <template v-if="preview?.total">
    <label class="preview-filter">查找项目 <input v-model="query" type="search" placeholder="名称或原位置"/></label>
    <p v-if="query.trim()" role="status">匹配 {{filteredItems.length}} / {{preview.total}} 项。清空范围仍是整个回收站。</p>
    <p v-if="!visibleItems.length">没有匹配项目。</p>
    <ul v-else :key="page" aria-label="清空回收站预览项目"><li v-for="r in visibleItems" :key="r.id"><strong>{{r.name}}{{r.kind==='folder'?'（文件夹及内部内容）':''}}</strong><p>{{r.originalPath||'服务商未返回原位置'}}</p></li></ul>
    <nav v-if="pageCount>1" class="share-history-actions" aria-label="清空预览分页">
     <button class="secondary" :disabled="page<=1" @click="stepPage(-1)">上一页</button>
     <span role="status">第 {{page}} / {{pageCount}} 页</span>
     <button class="secondary" :disabled="page>=pageCount" @click="stepPage(1)">下一页</button>
    </nav>
   </template>
   <template v-if="preview&&preview.total&&!started"><label class="confirm-text">输入“清空回收站”以确认<input v-model="confirmation" aria-label="清空回收站确认文字" autocomplete="off"/></label><label class="checkbox-row"><input v-model="agreed" type="checkbox"/>{{serverClear?'我确认清空光鸭执行时的整个回收站，理解永久删除后无法还原':'我已核对账号和完整清单，理解永久删除后无法还原'}}</label></template>
   <p v-if="!serverClear">逐项处理，遇到失败或结果待核对即停止。手动停止等待当前请求返回，已完成项目无法撤销；重启不会自动续跑。</p>
   <p v-if="status" role="status">{{status}}</p><p v-if="error" class="banner error" role="alert">{{error}}</p>
   <ul aria-label="清空回收站逐项结果"><li v-for="(r,i) in results" :key="i"><strong>{{r.name}} · {{label(r.state)}}</strong><p>{{r.message}}</p></li></ul>
   <BatchOperationReport v-if="started&&!busy" :operation="serverClear?'清空回收站服务端任务':'清空回收站逐项结果'" :account-name="accountName" :rows="results"/>
   <div class="modal-actions"><button class="secondary" :disabled="busy" @click="emit('close')">{{started?'关闭':'取消'}}</button><button v-if="!started" class="secondary" :disabled="busy||copying" @click="load">重新读取完整清单</button><button v-if="!started" class="primary" :disabled="!canStart" @click="start">确认清空整个回收站</button><button v-if="busy&&started&&!serverClear" class="secondary" :disabled="stop" @click="stop=true">停止后续处理</button></div>
  </section>
 </div>
</template>
<style scoped>
.recycle-empty-dialog ul{max-height:230px;overflow:auto;padding-left:23px}.recycle-empty-dialog li{margin:10px 0}.preview-filter{display:flex;align-items:center;gap:8px}.preview-filter input{min-width:0;padding:7px 9px;font:inherit}.confirm-text{display:flex;flex-direction:column;gap:8px;font-weight:600}.confirm-text input{padding:8px;font:inherit}.checkbox-row{display:flex;gap:9px;margin:15px 0;align-items:flex-start}.recycle-empty-dialog .primary{background:#b42318;border-color:#b42318}
</style>
