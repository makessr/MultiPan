<script setup lang="ts">
import {computed,nextTick,onMounted,onUnmounted,ref,watch} from 'vue'
import {Browser} from '@wailsio/runtime'
import {trapDialogFocus} from './dialogFocus'
import CloudShareList from './CloudShareList.vue'
import LanzouFolderShares from './LanzouFolderShares.vue'
import BatchManagedShareReadDialog from './BatchManagedShareReadDialog.vue'
import BatchShareExpiryDialog from './BatchShareExpiryDialog.vue'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import {shareText,shareLink,shareLinkSearchKey,searchShareLinks} from './shareText'
import {shareAccountLabel,shareProviderName as cloudName,shareWebsite} from './shareAccounts'
import type {Account,LanzouShareLocation,ShareRecord,ShareSourceLocation} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accounts:Account[];blocked?:boolean;refreshKey?:string;reviewTarget?:{kind:string;accountId:string;receiptId:string}|null}>()
const emit=defineEmits<{mutation:[active:boolean];files:[];source:[location:ShareSourceLocation,recreate:boolean]}>()
const records=ref<ShareRecord[]>([]),busy=ref(false),error=ref(''),message=ref(''),query=ref(''),kind=ref(''),status=ref(''),autoFill=ref(true)
const action=ref('')
const accountFilter=ref('')
const page=ref(1)
const pageSize=50
const batchCheck=ref<{done:number;total:number;stop:boolean}|null>(null)
const checkedReceipts=ref(new Set<string>())
const checkAccount=ref('')
const actionLabels=new Map([
 ['create','创建分享'],
 ['createPaid123','创建 123 付费分享'],
 ['get','读取原链接'],
 ['password','修改提取码'],
 ['settings','更新分享设置'],
 ['unknown','操作类型未知'],
])
function recordAction(row:ShareRecord){
 if(row.action==='create'&&row.result.kind==='pan123'&&(row.paidPriceFen123??0)>0)return 'createPaid123'
 return actionLabels.has(row.action)?row.action:'unknown'
}
const view=ref(props.reviewTarget?.kind==='share_revoke'?'cloud':'local'),cloudBusy=ref(false)
const focusReceipt=ref(props.reviewTarget??null)
const lanzouLocation=ref<LanzouShareLocation|null>(null)
const previousTarget=ref<ShareRecord|null>(null)
const previousDialog=ref<HTMLElement|null>(null)
let previousTrigger:HTMLElement|null=null
const selectedLanzou=ref<string[]>([])
const lanzouBatch=ref<{accountId:string;accountName:string;scope:string;editPassword:boolean;targets:{id:string;name:string;canEdit:boolean;canActivate:boolean;canLimitTraffic123:boolean}[]}|null>(null)
const accountScope=computed(()=>JSON.stringify([props.refreshKey,props.accounts.map(a=>[a.id,a.kind,a.endpoint,a.username])]))
const checkPageScope=computed(()=>JSON.stringify([
 accountScope.value,view.value,focusReceipt.value,page.value,query.value,kind.value,status.value,action.value,accountFilter.value,
]))
let disposed=false
function cloudMutation(value:boolean){cloudBusy.value=value;if(value)records.value=[];emit('mutation',value)}
function accountLabel(row:ShareRecord){
 const account=props.accounts.find(a=>a.id===row.accountId&&a.kind===row.result.kind)
 return account?shareAccountLabel(account,props.accounts):cloudName(row.result.kind)+' · 原账号已移除'
}
const kinds=computed(()=>[...new Set(records.value.map(r=>r.result.kind))])
function accountKey(row:ShareRecord){return JSON.stringify([row.result.kind,row.accountId])}
const availableAccounts=computed(()=>{
 const labels=new Map<string,string>()
 const counts=new Map<string,number>()
 for(const row of records.value){
  if(kind.value&&row.result.kind!==kind.value)continue
  const key=accountKey(row)
  if(labels.has(key))continue
  const label=accountLabel(row)
  labels.set(key,label)
  counts.set(label,(counts.get(label)??0)+1)
 }
 const positions=new Map<string,number>()
 for(const [key,label] of labels){
  if((counts.get(label)??0)<2)continue
  const position=(positions.get(label)??0)+1
  positions.set(label,position)
  labels.set(key,label+'（'+position+'）')
 }
 return labels
})
const availableActions=computed(()=>{
 const present=new Set(records.value.map(recordAction))
 return [...actionLabels].filter(([key])=>present.has(key))
})

const searchLinks=computed(()=>searchShareLinks(query.value))
const rows=computed(()=>{
 const term=query.value.trim().toLocaleLowerCase()
 return records.value.filter(r=>{
  if(focusReceipt.value)return r.accountId===focusReceipt.value.accountId&&r.result.id===focusReceipt.value.receiptId
  if(kind.value&&r.result.kind!==kind.value||status.value&&r.result.state!==status.value||action.value&&recordAction(r)!==action.value)return false
  if(accountFilter.value&&accountKey(r)!==accountFilter.value)return false
  if(!term)return true
  return searchLinks.value.size>0?searchLinks.value.has(shareLinkSearchKey(r.result.url)):
   (r.result.name+' '+accountLabel(r)).toLocaleLowerCase().includes(term)
 })
})
const clearableRows=computed(()=>rows.value.filter(row=>!row.keepForReplacement&&(row.result.state==='ready'||row.result.state==='failed')))
const pageCount=computed(()=>Math.max(1,Math.ceil(rows.value.length/pageSize)))
const visibleRows=computed(()=>rows.value.slice((page.value-1)*pageSize,page.value*pageSize))
function canCheck(row:ShareRecord){return row.result.canCheck&&row.result.state!=='working'}
const checkableRows=computed(()=>visibleRows.value.filter(canCheck))
const pageCheckAccounts=computed(()=>[...new Set(checkableRows.value.map(accountKey))])
const pageCheckCandidates=computed(()=>{
 const key=pageCheckAccounts.value.length===1?pageCheckAccounts.value[0]:checkAccount.value
 return checkableRows.value.filter(row=>accountKey(row)===key)
})
const checkedInAccount=computed(()=>pageCheckCandidates.value.some(row=>checkedReceipts.value.has(row.result.id)))
const pageCheckTargets=computed(()=>pageCheckCandidates.value.filter(row=>!checkedReceipts.value.has(row.result.id)).slice(0,20))
function changePage(target:number){
 if(busy.value||props.blocked||lanzouBatch.value)return
 page.value=Math.max(1,Math.min(target,pageCount.value))
}
const copyableRows=computed(()=>rows.value.filter(row=>shareLink(row.result,false)))
const selectedLanzouRows=computed(()=>rows.value.filter(row=>row.canLocate&&selectedLanzou.value.includes(row.result.id)))
function canSelectLanzou(row:ShareRecord){
 const first=selectedLanzouRows.value[0]
 return row.canLocate&&(!first||first.accountId===row.accountId)&&
  (selectedLanzou.value.length<50||selectedLanzou.value.includes(row.result.id))
}
function openLanzouBatch(editPassword=false){
 const chosen=selectedLanzouRows.value
 if(disposed||busy.value||props.blocked||lanzouBatch.value||!chosen.length)return
 const [first]=chosen
 if(chosen.some(row=>row.accountId!==first.accountId))return
 lanzouBatch.value={accountId:first.accountId,accountName:accountLabel(first),scope:accountScope.value,editPassword,
  targets:chosen.map(row=>({id:row.result.id,name:row.result.name,canEdit:true,canActivate:false,canLimitTraffic123:false}))}
 emit('mutation',true)
}
async function readLanzouReceipt(id:string){
 const batch=lanzouBatch.value
 if(disposed||!batch||accountScope.value!==batch.scope||!batch.targets.some(row=>row.id===id))throw Error('蓝奏账号或回执选择已变化')
 const result=await api.ReadLanzouShareReceiptLink(batch.accountId,id)
 if(disposed||lanzouBatch.value!==batch||accountScope.value!==batch.scope)throw Error('蓝奏账号或回执选择已变化')
 return result
}
async function readLanzouReceipts(ids:string[]){
 const batch=lanzouBatch.value
 if(disposed||!batch||accountScope.value!==batch.scope||ids.some(id=>!batch.targets.some(row=>row.id===id)))throw Error('蓝奏账号或回执选择已变化')
 const result=await api.ReadLanzouShareReceiptLinks(batch.accountId,ids)
 if(disposed||lanzouBatch.value!==batch||accountScope.value!==batch.scope)throw Error('蓝奏账号或回执选择已变化')
 return result
}
async function editLanzouReceiptPassword(id:string,original:{url:string;password:string},password:string,clear:boolean){
 const batch=lanzouBatch.value
 if(disposed||props.blocked||!batch?.editPassword||accountScope.value!==batch.scope||!batch.targets.some(row=>row.id===id))throw Error('蓝奏账号或回执选择已变化')
 return api.EditLanzouShareReceiptPassword(batch.accountId,id,original.url,original.password,password,clear)
}
const receiptPassword={
 read:readLanzouReceipt,
 readGroup:readLanzouReceipts,
 update:(id:string,original:{url:string;password:string},password:string)=>editLanzouReceiptPassword(id,original,password,false),
 clear:(id:string,original:{url:string;password:string})=>editLanzouReceiptPassword(id,original,'',true),
}
function closeLanzouBatch(){
 if(!lanzouBatch.value)return
 lanzouBatch.value=null
 emit('mutation',false)
}
function closeLanzouEdit(submitted:boolean){
 closeLanzouBatch()
 if(submitted){selectedLanzou.value=[];void refresh()}
}
function stateLabel(state:string){return ({ready:'上次已取得',working:'处理中',unknown:'待确认',failed:'不可用'} as Record<string,string>)[state]??state}
function operationTime(value:unknown){const date=new Date(String(value));return Number.isNaN(date.getTime())?'':date.toLocaleString('zh-CN',{hour12:false})}
async function read(){
 const scope=accountScope.value
 const next=await api.CloudShares()??[]
 if(disposed||scope!==accountScope.value)return
 records.value=next
 if(checkedReceipts.value.size){
  const existing=new Set(next.map(row=>row.result.id))
  for(const id of checkedReceipts.value)if(!existing.has(id))checkedReceipts.value.delete(id)
 }
}
async function refresh(){if(busy.value||props.blocked)return;busy.value=true;error.value='';try{await read()}catch(e){error.value=String(e)}finally{busy.value=false}}
async function check(row:ShareRecord){if(busy.value||props.blocked||!row.result.canCheck||row.result.state==='working')return;busy.value=true;error.value='';message.value='';emit('mutation',true);try{await api.CheckCloudShare(row.result.id);await read();message.value='已检查原操作结果，没有重新创建分享'}catch(e){error.value=String(e)}finally{busy.value=false;emit('mutation',false)}}
function canOpenPaidReview(row:ShareRecord){return recordAction(row)==='createPaid123'&&row.result.state==='unknown'}
async function openPaidReview(row:ShareRecord){
 const website=shareWebsite('pan123')
 if(disposed||busy.value||props.blocked||view.value!=='local'||!rows.value.includes(row)||!canOpenPaidReview(row)||!website)return
 const scope=accountScope.value
 error.value=''
 try{await Browser.OpenURL(website)}
 catch{if(!disposed&&scope===accountScope.value)error.value='无法打开 123 云盘官网'}
}
function stopPageCheck(){if(batchCheck.value)batchCheck.value.stop=true}
async function checkPage(restart=false){
 if(disposed||busy.value||props.blocked||lanzouBatch.value||focusReceipt.value||view.value!=='local')return
 if(restart)for(const row of pageCheckCandidates.value)checkedReceipts.value.delete(row.result.id)
 const targets=pageCheckTargets.value.map(row=>row.result.id)
 if(!targets.length)return
 const scope=checkPageScope.value
 const source=accountScope.value
 const selectedAccount=checkAccount.value
 busy.value=true;error.value='';message.value=''
 batchCheck.value={done:0,total:targets.length,stop:false}
 const run=batchCheck.value
 emit('mutation',true)
 try{
  for(const id of targets){
   if(disposed||run.stop||props.blocked||scope!==checkPageScope.value||selectedAccount!==checkAccount.value)break
   const checked=await api.CheckCloudShare(id)
   if(disposed||source!==accountScope.value)break
   if(checked?.id!==id||!['ready','failed','unknown'].includes(checked.state))throw Error('原回执未返回可用结果')
   checkedReceipts.value.add(id)
   run.done++
  }
 }catch(e){
  if(!disposed&&scope===checkPageScope.value)error.value=String(e)
 }finally{
  try{
   if(!disposed){
    await read()
    if(scope===checkPageScope.value)message.value=`已检查 ${run.done} / ${run.total} 条原回执`
   }
  }catch(e){if(!disposed)error.value=String(e)}
  finally{batchCheck.value=null;busy.value=false;emit('mutation',false)}
 }
}
async function copyText(text:string,success:string){
 if(disposed||busy.value||props.blocked||!text)return
 busy.value=true;error.value='';message.value=''
 try{await api.CopyDownloadReport(text);message.value=success}
 catch(e){error.value=String(e)}
 finally{busy.value=false}
}
async function copy(row:ShareRecord,linkOnly=false){
 await copyText(shareText(row.result,linkOnly,autoFill.value),linkOnly?'分享链接已复制':'完整分享信息已复制')
}
async function copyFiltered(linkOnly=false){
 if(view.value!=='local'||focusReceipt.value||disposed||busy.value||props.blocked)return
 const selected=copyableRows.value
 const text=selected.map(row=>{
  const value=shareText(row.result,linkOnly,autoFill.value)
  return linkOnly?value:accountLabel(row)+'\n'+value
 }).join(linkOnly?'\n':'\n\n')
 await copyText(text,linkOnly?`已复制 ${selected.length} 条分享链接`:`已复制 ${selected.length} 条完整分享信息`)
}
async function preparePreviousRevoke(row:ShareRecord,event?:Event){
 if(disposed||busy.value||props.blocked||view.value!=='local'||!row.previousShareUrl||!rows.value.includes(row))return
 previousTrigger=event?.currentTarget as HTMLElement|null
 previousTarget.value=row
 await nextTick()
 previousDialog.value?.focus()
}
async function cancelPreviousRevoke(){
 previousTarget.value=null
 await nextTick()
 previousTrigger?.focus()
}
async function submitPreviousRevoke(){
 const row=previousTarget.value
 if(disposed||busy.value||props.blocked||view.value!=='local'||!row?.previousShareUrl||!rows.value.includes(row))return
 const scope=accountScope.value
 previousTarget.value=null;busy.value=true;error.value='';message.value='';emit('mutation',true)
 try{
  await api.RevokePreviousCloudShare(row.accountId,row.result.id,row.previousShareUrl)
  if(disposed||scope!==accountScope.value)return
  await read()
 }catch(e){if(!disposed&&scope===accountScope.value)error.value=String(e)}
 finally{busy.value=false;if(!disposed)emit('mutation',false)}
}
async function checkPreviousRevoke(row:ShareRecord){
 const previous=row.previousRevocation
 if(disposed||busy.value||props.blocked||view.value!=='local'||!previous?.canCheck||!rows.value.includes(row))return
 const scope=accountScope.value
 busy.value=true;error.value='';emit('mutation',true)
 try{
  await api.CheckManagedCloudShareRevoke(previous.id)
  if(disposed||scope!==accountScope.value)return
  await read()
 }catch(e){if(!disposed&&scope===accountScope.value)error.value=String(e)}
 finally{busy.value=false;if(!disposed)emit('mutation',false)}
}
async function openSource(row:ShareRecord,recreate=false){
 if(disposed||busy.value||props.blocked||view.value!=='local'||!row.canOpenSource||recreate&&!row.canRecreate||!rows.value.includes(row))return
 const scope=accountScope.value
 const accountId=row.accountId
 busy.value=true;error.value=''
 try{
  const location=await api.LocateCreatedShareSource(accountId,row.result.id)
  if(disposed||scope!==accountScope.value||props.blocked||!rows.value.includes(row))return
  if(location?.accountId!==accountId||!location.parentId)throw Error('原目录无法核对')
  if(recreate&&(!location.recreation||location.recreation.receiptId!==row.result.id))throw Error('原项目标识无法核对，请打开原文件目录')
  emit('source',location,recreate)
 }catch(e){if(!disposed&&scope===accountScope.value)error.value=String(e)}
 finally{busy.value=false}
}
async function locateLanzou(row:ShareRecord){
 if(disposed||busy.value||props.blocked||view.value!=='local'||!row.canLocate||!rows.value.includes(row))return
 const scope=accountScope.value
 busy.value=true;error.value='';message.value='';emit('mutation',true)
 try{
  const location=await api.LocateLanzouShareReceipt(row.accountId,row.result.id)
  if(disposed||scope!==accountScope.value||props.blocked)return
  if(location.accountId!==row.accountId||!props.accounts.some(a=>a.id===location.accountId&&a.kind==='lanzou'))return
  lanzouLocation.value=location
  view.value='lanzou'
 }catch(e){if(!disposed&&scope===accountScope.value)error.value=String(e)}
 finally{busy.value=false;emit('mutation',false)}
}
async function clearConfirmed(){
 if(disposed||busy.value||props.blocked||focusReceipt.value||view.value!=='local')return
 const ids=clearableRows.value.map(row=>row.result.id)
 if(!ids.length||!window.confirm('清理当前筛选命中的 '+ids.length+' 条本机已确认记录？云端分享和待确认记录会保留。'))return
 busy.value=true;error.value='';message.value='';emit('mutation',true)
 try{
  const count=await api.ClearSelectedCloudShareHistory(ids)
  await read()
  message.value='已清理 '+count+' 条本机已确认记录'
 }catch(e){error.value=String(e)}
 finally{busy.value=false;emit('mutation',false)}
}
onMounted(()=>{if(view.value==='local')void refresh()})
watch(view,()=>{focusReceipt.value=null;records.value=[];if(view.value!=='lanzou')lanzouLocation.value=null;if(view.value==='local')void refresh()})
watch([query,kind,status,action,accountFilter,view,focusReceipt],()=>{page.value=1;selectedLanzou.value=[]})
watch(pageCount,count=>{page.value=Math.min(page.value,count)})
watch(checkPageScope,()=>{previousTarget.value=null;checkAccount.value='';stopPageCheck()})
watch(pageCheckAccounts,accounts=>{if(!accounts.includes(checkAccount.value))checkAccount.value=''})
watch([availableAccounts,batchCheck],([options,batch])=>{if(!batch&&accountFilter.value&&!options.has(accountFilter.value))accountFilter.value=''})
watch([availableActions,batchCheck],([options,batch])=>{if(!batch&&action.value&&!options.some(([key])=>key===action.value))action.value=''})
watch(accountScope,()=>{checkedReceipts.value.clear();selectedLanzou.value=[];closeLanzouBatch();if(batchCheck.value){stopPageCheck();records.value=[]}else void refresh()})
onUnmounted(()=>{disposed=true;closeLanzouBatch()})
</script>
<template>
 <section class="share-history-page">
  <div class="view-heading"><div><h1>分享记录</h1></div><button v-if="view==='local'" class="secondary" :disabled="busy||blocked" @click="refresh">刷新记录</button></div>
  <div class="share-history-actions" role="group" aria-label="分享来源"><button class="secondary" :aria-pressed="view==='local'" :disabled="busy||cloudBusy||blocked" @click="view='local'">本机操作记录</button><button class="secondary" :aria-pressed="view==='cloud'" :disabled="busy||cloudBusy||blocked" @click="view='cloud'">云端分享</button><button v-if="accounts.some(a=>a.kind==='lanzou')" class="secondary" :aria-pressed="view==='lanzou'" :disabled="busy||cloudBusy||blocked" @click="view='lanzou'">蓝奏目录分享</button></div>
  <CloudShareList v-if="view==='cloud'" :accounts="accounts" :blocked="blocked" :refresh-key="refreshKey" :review-target="focusReceipt" @mutation="cloudMutation"/>
  <LanzouFolderShares v-else-if="view==='lanzou'" :initial-location="lanzouLocation" :accounts="accounts" :blocked="blocked" :refresh-key="refreshKey" @mutation="cloudMutation"/>
  <template v-else>
  <details class="operation-details"><summary>本机记录与清理</summary><p>本机最多加密保存 1000 条。清理只移除本机已确认记录。重新分享仍在使用的原回执会保留。</p></details>
  <div v-if="focusReceipt" class="share-history-actions"><strong>原分享回执</strong><button class="secondary" :disabled="busy||blocked" @click="focusReceipt=null">显示全部分享记录</button></div>
  <div v-else class="share-history-filters">
   <input v-model="query" :disabled="!!batchCheck" type="search" placeholder="搜索文件名、网盘、账号或分享链接" aria-label="按名称、网盘、账号或链接搜索分享记录"/>
   <select v-model="kind" :disabled="!!batchCheck" aria-label="按网盘筛选分享记录"><option value="">全部网盘</option><option v-for="item in kinds" :key="item" :value="item">{{cloudName(item)}}</option></select>
   <select v-if="availableAccounts.size>1" v-model="accountFilter" :disabled="!!batchCheck" aria-label="按账号筛选分享记录"><option value="">全部账号</option><option v-for="[key,label] in availableAccounts" :key="key" :value="key">{{label}}</option></select>
   <select v-if="availableActions.length>1" v-model="action" :disabled="!!batchCheck" aria-label="按操作类型筛选分享记录"><option value="">全部操作</option><option v-for="[key,label] in availableActions" :key="key" :value="key">{{label}}</option></select>
   <select v-model="status" :disabled="!!batchCheck" aria-label="按状态筛选分享记录"><option value="">全部状态</option><option value="ready">上次已取得</option><option value="unknown">待确认</option><option value="failed">不可用</option><option value="working">处理中</option></select>
  </div>
  <label class="share-switch"><input v-model="autoFill" type="checkbox"/>复制百度、光鸭有提取码的链接时自动填充提取码</label>
  <p v-if="error" class="banner error" role="alert">{{error}}</p><p v-if="message" class="banner notice" role="status">{{message}}</p>
  <div v-if="previousTarget" class="modal-backdrop" @click.self="cancelPreviousRevoke"><section ref="previousDialog" tabindex="-1" class="operation-dialog" role="alertdialog" aria-modal="true" aria-label="确认撤销旧链接" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="cancelPreviousRevoke">
   <strong>撤销旧链接</strong><p>{{accountLabel(previousTarget)}}</p>
   <p>撤销 {{previousTarget.previousShareUrl}}</p><p>保留 {{previousTarget.result.url}}</p><p>撤销后旧链接将失效。</p>
   <div class="share-history-actions"><button class="secondary" @click="cancelPreviousRevoke">取消</button><button class="primary" :disabled="busy||blocked" @click="submitPreviousRevoke">确认撤销旧链接</button></div>
  </section></div>
  <div v-if="!focusReceipt" class="share-history-actions">
   <select v-if="pageCheckAccounts.length>1" v-model="checkAccount" :disabled="busy||blocked" aria-label="选择本页核对账号">
    <option value="">选择核对账号</option>
    <option v-for="key in pageCheckAccounts" :key="key" :value="key">{{availableAccounts.get(key)}}</option>
   </select>
   <button v-if="batchCheck" class="secondary" :disabled="batchCheck.stop" @click="stopPageCheck">{{batchCheck.stop?'正在停止…':'停止后续核对'}}</button>
   <button v-else-if="pageCheckTargets.length" class="secondary" :disabled="busy||blocked" @click="checkPage()">{{checkedInAccount?'继续核对本页':'核对本页原回执'}}（{{pageCheckTargets.length}}）</button>
   <button v-else-if="pageCheckCandidates.length" class="secondary" :disabled="busy||blocked" @click="checkPage(true)">重新核对本页</button>
   <button v-if="copyableRows.length" class="secondary" :disabled="busy||blocked" @click="copyFiltered()">复制筛选结果（{{copyableRows.length}} 条）</button>
   <button v-if="copyableRows.length" class="secondary" :disabled="busy||blocked" @click="copyFiltered(true)">仅复制筛选链接</button>
   <button class="secondary" :disabled="busy||blocked||!clearableRows.length" @click="clearConfirmed">清理筛选记录…</button>
  </div>
  <p class="muted">匹配 {{rows.length}} / {{records.length}} 条。已取得表示上次读取结果；链接可能过期或被官网修改，可先检查原结果。</p>
  <p v-if="batchCheck" role="status">正在核对原回执 · {{batchCheck.done}} / {{batchCheck.total}}</p>
  <p v-else-if="busy" role="status">正在处理…</p>
  <p v-if="focusReceipt&&!rows.length&&!busy" class="share-history-empty">原分享记录已不存在或不可读取。</p>
  <div v-else-if="!records.length&&!busy" class="share-history-empty"><p>还没有本机分享记录。</p><button class="secondary" :disabled="blocked" @click="emit('files')">去文件管理</button></div>
  <p v-else-if="!rows.length" class="share-history-empty">没有匹配的分享记录。</p>
  <div v-if="selectedLanzouRows.length" class="share-history-actions">
   <button class="secondary" :disabled="busy||blocked" @click="selectedLanzou=[]">清空勾选</button>
   <button class="secondary" :disabled="busy||blocked" @click="openLanzouBatch()">核对所选蓝奏分享（{{selectedLanzouRows.length}}）</button>
   <button class="secondary" :disabled="busy||blocked" @click="openLanzouBatch(true)">修改所选提取码…</button>
  </div>
  <nav v-if="pageCount>1" class="share-history-actions" aria-label="本机分享记录分页">
   <button class="secondary" :disabled="busy||blocked||page<=1" @click="changePage(page-1)">上一页</button>
   <span role="status">第 {{page}} / {{pageCount}} 页</span>
   <button class="secondary" :disabled="busy||blocked||page>=pageCount" @click="changePage(page+1)">下一页</button>
  </nav>
  <article v-for="row in visibleRows" :key="row.result.id" class="share-history-card">
   <div class="share-history-card-heading"><div>
    <label v-if="row.canLocate&&!focusReceipt"><input v-model="selectedLanzou" type="checkbox" :value="row.result.id"
     :disabled="busy||blocked||!canSelectLanzou(row)" :aria-label="'选择蓝奏分享 '+row.result.name"/> <strong>{{row.result.name}}</strong></label>
    <strong v-else>{{row.result.name}}</strong><small>{{accountLabel(row)}} · {{actionLabels.get(recordAction(row))}} {{operationTime(row.created)}}</small></div><span class="share-history-state" :class="row.result.state">{{stateLabel(row.result.state)}}</span></div>
   <p>{{row.result.message}}</p>
   <p v-if="row.previousRevocation">旧链接撤销 · {{row.previousRevocation.message}}</p>
   <p v-if="recordAction(row)==='createPaid123'">申请金额 {{((row.paidPriceFen123??0)/100).toFixed(2)}} 元</p>
   <template v-if="row.result.state==='ready'"><label>分享链接<input :value="shareLink(row.result,autoFill)" readonly :aria-label="row.result.name+'的分享链接'"/></label><p>{{row.result.kind==='lanzou'?'回执提取码':'提取码'}}：{{row.result.password||'无'}} · {{row.result.expiry?'当前有效期 · '+row.result.expiry:row.result.kind==='lanzou'?'沿用网盘原设置':'申请有效期：'+(row.result.days===0?(row.result.kind==='pan123'?'永久（至2099年）':'永久'):row.result.days+'天')}}</p></template>
   <div class="share-history-actions"><template v-if="row.result.state==='ready'"><button class="secondary" :disabled="busy||blocked" @click="copy(row)">复制完整信息</button><button class="secondary" :disabled="busy||blocked" @click="copy(row,true)">只复制链接</button></template><button v-if="row.result.canCheck" class="secondary" :disabled="busy||blocked||row.result.state==='working'" @click="check(row)">检查原结果</button><button v-if="canOpenPaidReview(row)" class="secondary" :disabled="busy||blocked" @click="openPaidReview(row)">到官网查看审核</button><button v-if="row.previousRevocation?.canCheck" class="secondary" :disabled="busy||blocked" @click="checkPreviousRevoke(row)">检查旧链接撤销结果</button><button v-if="row.previousShareUrl" class="secondary" :disabled="busy||blocked" @click="preparePreviousRevoke(row,$event)">撤销旧链接…</button><button v-if="row.canRecreate" class="secondary" :disabled="busy||blocked" @click="openSource(row,true)">重新分享…</button><button v-if="row.canOpenSource" class="secondary" :disabled="busy||blocked" @click="openSource(row)">打开原文件目录</button><button v-if="row.canLocate" class="secondary" :disabled="busy||blocked" @click="locateLanzou(row)">定位原目录</button></div>
  </article>
  </template>
  <BatchShareExpiryDialog v-if="lanzouBatch?.editPassword" kind="lanzou" :account-name="lanzouBatch.accountName"
   :targets="lanzouBatch.targets" :blocked="blocked" :directory-password="receiptPassword" @close="closeLanzouEdit"/>
  <BatchManagedShareReadDialog v-else-if="lanzouBatch" kind="lanzou" :account-name="lanzouBatch.accountName"
   :targets="lanzouBatch.targets" :blocked="blocked" :read-link="readLanzouReceipt" :read-links="readLanzouReceipts" show-result-message @close="closeLanzouBatch"/>
 </section>
</template>
