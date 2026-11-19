<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import {CopyDownloadReport} from '../bindings/multipan/desktopservice'
import type {ManagedShare,ManagedSharePage} from '../bindings/multipan/internal/transfer/models'
import {createPageSearch} from './pageSearch'
import {spreadsheetCell} from './spreadsheetCell'
import {trapDialogFocus} from './dialogFocus'

const props=defineProps<{
 accountName:string
 scopeLabel:string
 readPage:(page:number,cursor?:string)=>Promise<ManagedSharePage>
 blocked?:boolean
}>()
const emit=defineEmits<{close:[]}>()
type InventoryRow=Pick<ManagedShare,'name'|'status'|'created'|'expiry'>
const dialog=ref<HTMLElement|null>(null)
const rows=ref<InventoryRow[]>([])
const active=ref(false),copying=ref(false),stopping=ref(false)
const summary=ref(''),feedback=ref(''),page=ref(1)
const incomplete=ref(false)
const query=ref('')
const statusFilter=ref('')
const statuses=computed(()=>[...new Set(rows.value.map(row=>row.status).filter(Boolean))])
const filtered=computed(()=>{
 const term=query.value.trim().toLocaleLowerCase()
 return rows.value.filter(row=>row.name.toLocaleLowerCase().includes(term)&&(!statusFilter.value||row.status===statusFilter.value))
})
const visible=computed(()=>filtered.value.slice((page.value-1)*25,page.value*25))
const pages=computed(()=>Math.max(1,Math.ceil(filtered.value.length/25)))
watch([query,statusFilter],()=>{page.value=1},{flush:'sync'})
let disposed=false
onMounted(()=>dialog.value?.focus())
onUnmounted(()=>{disposed=true})

function close(){if(!active.value&&!copying.value)emit('close')}

async function read(){
 if(active.value||copying.value||props.blocked)return
 active.value=true;stopping.value=false;rows.value=[];summary.value='';feedback.value='';query.value='';statusFilter.value='';page.value=1;incomplete.value=false
 let next=''
 const seen=new Set<string>()
 const scan=createPageSearch(async target=>{
  if(next){
   if(seen.has(next))throw Error('分页标识重复')
   seen.add(next)
  }
  const result=await props.readPage(target,next||undefined)
  next=result.next
  return result
 },()=>true,row=>row.id?row.selectionKey:'')
 try{
  const result=await scan(()=>!disposed,()=>stopping.value||!!props.blocked,(_page,items)=>{
   rows.value.push(...items.map(({name,status,created,expiry})=>({name,status,created,expiry})))
  })
  if(result){summary.value=result.summary;incomplete.value=result.incomplete}
 }finally{active.value=false}
}

async function copy(){
 if(!summary.value||!rows.value.length||active.value||copying.value||props.blocked)return
 const values=rows.value.map(row=>[row.name,row.status,row.created,row.expiry].map(spreadsheetCell).join('\t'))
 const status=(incomplete.value?'部分清单 · ':'读取完成 · ')+rows.value.length+' 条'
 const report=[spreadsheetCell(props.accountName),spreadsheetCell(props.scopeLabel),status,'名称\t状态\t创建时间\t有效期',...values].join('\r\n')
 copying.value=true;feedback.value=''
 try{
  await CopyDownloadReport(report)
  if(!disposed)feedback.value='已复制 '+rows.value.length+' 条清单记录。'
 }catch{
  if(!disposed)feedback.value='无法写入剪贴板。'
 }finally{copying.value=false}
}
</script>

<template>
 <div class="modal-backdrop">
  <section ref="dialog" class="operation-dialog share-dialog" role="dialog" tabindex="-1" aria-modal="true"
   aria-labelledby="share-inventory-title" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="close">
   <div class="modal-heading">
    <h2 id="share-inventory-title">云端分享清单</h2>
    <button class="secondary" :disabled="active||copying" @click="close">关闭</button>
   </div>
   <p>{{accountName}} · {{scopeLabel}}</p>
   <p>从第一页读取，最多 10 页或 500 条。清单仅含名称、状态、创建时间和有效期。</p>
   <div class="share-history-actions">
    <button v-if="active" class="secondary" :disabled="stopping" @click="stopping=true">{{stopping?'正在停止…':'停止读取'}}</button>
    <button v-else class="secondary" :disabled="blocked||copying" @click="read">{{summary?'重新读取':'读取清单'}}</button>
    <button v-if="summary&&rows.length" class="primary" :disabled="active||blocked||copying" @click="copy">复制完整表格</button>
   </div>
   <p v-if="active" role="status">已读取 {{rows.length}} 条。</p>
   <p v-else-if="summary" role="status">{{summary}}</p>
   <div v-if="summary&&rows.length" class="share-history-filters">
    <input v-model="query" type="search" placeholder="搜索已读取的分享" aria-label="清单内搜索分享名称"/>
    <select v-model="statusFilter" aria-label="按分享状态筛选"><option value="">全部状态</option><option v-for="status in statuses" :key="status" :value="status">{{status}}</option></select>
   </div>
   <p v-if="query.trim()||statusFilter" role="status">显示 {{filtered.length}} / {{rows.length}} 条</p>
   <ul v-if="visible.length" class="recycle-batch-preview">
    <li v-for="(row,index) in visible" :key="index">
     <strong>{{row.name}}</strong>
     <p>{{[row.status,row.created,row.expiry].filter(Boolean).join(' · ')}}</p>
    </li>
   </ul>
   <div v-if="pages>1" class="share-history-actions">
    <button class="secondary" :disabled="page<=1" @click="page--">上一页</button>
    <span>{{page}} / {{pages}}</span>
    <button class="secondary" :disabled="page>=pages" @click="page++">下一页</button>
   </div>
   <p v-if="feedback" role="status">{{feedback}}</p>
  </section>
 </div>
</template>
