<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch,nextTick} from 'vue'
import {Browser} from '@wailsio/runtime'
import ShareSettingsDialog from './ShareSettingsDialog.vue'
import BatchShareRevokeDialog from './BatchShareRevokeDialog.vue'
import BatchManagedShareReadDialog from './BatchManagedShareReadDialog.vue'
import BatchShareExpiryDialog from './BatchShareExpiryDialog.vue'
import CloudShareInventory from './CloudShareInventory.vue'
import {managedShareText} from './shareText'
import {shareAccountLabel,shareWebsite} from './shareAccounts'
import {spreadsheetCell} from './spreadsheetCell'
import {trapDialogFocus} from './dialogFocus'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {Account,ManagedShare,ManagedShareLink,QuarkShareRestrictions,ShareRevocationResult} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accounts:Account[];blocked?:boolean;refreshKey?:string;reviewTarget?:{accountId:string;receiptId:string}|null}>()
const emit=defineEmits<{mutation:[active:boolean]}>()
const accounts=computed(()=>props.accounts.filter(a=>['quark','pan123','baidu','guangya'].includes(a.kind)))
const cursors=ref<Record<number,string>>({})
const selectedKind=computed(()=>accounts.value.find(a=>a.id===accountId.value)?.kind??'')
const selectedAccountName=computed(()=>{const a=accounts.value.find(a=>a.id===accountId.value);return a?accountLabel(a):'原账号已移除'})
const shareType=ref<'free'|'paid'>('free')
const paidView=computed(()=>selectedKind.value==='pan123'&&shareType.value==='paid')
const canRevoke=computed(()=>['quark','pan123','baidu','guangya'].includes(selectedKind.value))
function chooseShareType(value:string){
 if(locked.value||props.blocked||selectedKind.value!=='pan123'||(value!=='paid'&&value!=='free')||value===shareType.value)return
 shareType.value=value
 invalidate()
}
const quarkShareFilters=[
 {value:'',label:'全部状态'},{value:'valid',label:'分享有效'},
 {value:'violated',label:'分享违规'},{value:'expired',label:'分享过期'},
 {value:'failed',label:'分享失败'},{value:'private',label:'加密链接'},
]
const quarkShareFilter=ref('')
function quarkAccessLabel(access?:string){
 if(access==='public')return '免提取码'
 if(access==='protected')return '提取码保护'
 return ''
}
function chooseQuarkShareFilter(value:string){
 if(locked.value||props.blocked||selectedKind.value!=='quark'||value===quarkShareFilter.value||!quarkShareFilters.some(filter=>filter.value===value))return
 quarkShareFilter.value=value
 invalidate()
}
function fetchSharePage(id:string,target:number,cursor?:string){
 if(selectedKind.value==='pan123'&&target>1)return api.ManagedCloudSharesNext(id,cursor!)
 if(paidView.value)return api.ManagedPan123PaidShares(id)
 if(selectedKind.value==='quark'&&quarkShareFilter.value)return api.ManagedQuarkShares(id,target,quarkShareFilter.value)
 return api.ManagedCloudShares(id,target)
}
function accountLabel(a:Account){return shareAccountLabel(a,accounts.value)}
const focusReceiptId=ref(props.reviewTarget?.receiptId??'')
const accountId=ref(props.reviewTarget?.accountId??accounts.value[0]?.id??''),items=ref<ManagedShare[]>([]),page=ref(1),hasMore=ref(false),busy=ref(false),error=ref(''),message=ref(''),detail=ref<ManagedShareLink|null>(null)
const accountScope=computed(()=>JSON.stringify([props.refreshKey,props.accounts.map(a=>[a.id,a.kind,a.endpoint,a.username])]))
function resetAccountView(){
 shareType.value='free'
 quarkShareFilter.value=''
 invalidate()
}
const revokes=ref<ShareRevocationResult[]>([]),confirmTarget=ref<ManagedShare|null>(null)
const visibleRevokes=computed(()=>focusReceiptId.value?revokes.value.filter(row=>row.id===focusReceiptId.value):revokes.value)
const editTarget=ref<ManagedShare|null>(null)
const restrictionTarget=ref(''),restrictions=ref<QuarkShareRestrictions|null>(null)
const restrictionName=ref(''),restrictionError=ref(''),restrictionCopyStatus=ref('')
const restrictionQuery=ref(''),restrictionCopying=ref(false)
const restrictionDialog=ref<HTMLElement|null>(null)
let restrictionTrigger:HTMLElement|null=null
const restrictionItems=computed(()=>restrictions.value?.items??[])
const filteredRestrictions=computed(()=>{
 const term=restrictionQuery.value.trim().toLocaleLowerCase()
 return restrictionItems.value.filter(row=>[row.name,row.parent,row.reason].some(text=>text.toLocaleLowerCase().includes(term)))
})
const batchSelection=ref<string[]>([]),batchTargets=ref<ManagedShare[]|null>(null)
const batchReadTargets=ref<ManagedShare[]|null>(null)
const batchExpiryTargets=ref<ManagedShare[]|null>(null)
const batchActivate=ref(false)
type ShareSelection={page:number;share:ManagedShare}
const batchReadQueue=ref<ShareSelection[]>([])
const batchAcrossPages=ref(false)
let batchReadTrigger:HTMLElement|null=null
const batchChosen=computed(()=>items.value.filter(r=>batchSelection.value.includes(r.id)))
const canRevokeBatch=computed(()=>canRevoke.value&&batchChosen.value.length>0&&batchChosen.value.length===batchSelection.value.length&&batchChosen.value.every(row=>row.canRevoke))
function toggleBatch(id:string,on:boolean){if(locked.value||props.blocked)return;if(!on)batchSelection.value=batchSelection.value.filter(x=>x!==id);else if(batchSelection.value.length<50&&items.value.some(r=>r.id===id)&&!batchSelection.value.includes(id))batchSelection.value.push(id)}
function selectBatch(revocableOnly=false){if(locked.value||props.blocked)return;const candidates=revocableOnly?items.value.filter(row=>row.canRevoke):items.value;batchSelection.value=candidates.slice(0,50).map(r=>r.id)}
const canRevokeQueue=computed(()=>canRevoke.value&&batchReadQueue.value.length>0&&
 batchReadQueue.value.every(row=>row.share.canRevoke&&row.page<=(selectedKind.value==='baidu'?5:10)))
function prepareBatch(queued=false){
 if(locked.value||props.blocked||!(queued?canRevokeQueue.value:canRevokeBatch.value))return
 const targets=queued?batchReadQueue.value.map(row=>row.share):batchChosen.value
 batchAcrossPages.value=queued
 batchTargets.value=targets.map(row=>({...row}))
 readId.value='';detail.value=null;error.value='';message.value=''
 emit('mutation',true)
}
const canQueueReadSelection=computed(()=>batchChosen.value.length>0&&batchChosen.value.every(row=>row.selectionKey)&&
 new Set([...batchReadQueue.value.map(row=>row.share.selectionKey),...batchChosen.value.map(row=>row.selectionKey)]).size<=50)
function addToShareQueue(selections:ShareSelection[]){
 const queued=new Map(batchReadQueue.value.map(row=>[row.share.selectionKey,row]))
 for(const {page,share} of selections)queued.set(share.selectionKey,{page,share:{...share}})
 batchReadQueue.value=[...queued.values()]
}
function queueReadSelection(){
 if(locked.value||props.blocked||!canQueueReadSelection.value)return
 addToShareQueue(batchChosen.value.map(share=>({page:page.value,share})))
 batchSelection.value=[]
}
function removeQueuedShare(key:string){
 if(locked.value||props.blocked)return
 batchReadQueue.value=batchReadQueue.value.filter(row=>row.share.selectionKey!==key)
}
function prepareBatchRead(event?:Event,queued=false){
 if(disposed||locked.value||props.blocked||!accountId.value)return
 const targets=queued?batchReadQueue.value.map(row=>row.share):batchChosen.value
 if(!targets.length||(!queued&&targets.length!==batchSelection.value.length))return
 batchReadTrigger=(event?.currentTarget as HTMLElement|undefined)??null
 batchReadTargets.value=targets.map(row=>({...row}))
 readId.value='';detail.value=null;error.value='';message.value=''
 emit('mutation',true)
}
async function closeBatchRead(){batchReadTargets.value=null;emit('mutation',false);await nextTick();batchReadTrigger?.focus();batchReadTrigger=null}
const canEditBatchSettings=computed(()=>['baidu','pan123','guangya'].includes(selectedKind.value))
const canEditExpirySelection=computed(()=>batchChosen.value.length>0&&batchChosen.value.every(row=>row.canEdit))
const canEditExpiryQueue=computed(()=>batchReadQueue.value.length>0&&batchReadQueue.value.every(row=>row.share.canEdit))
const canActivateSelection=computed(()=>batchChosen.value.length>0&&batchChosen.value.length===batchSelection.value.length&&batchChosen.value.every(row=>row.canActivate))
const canActivateQueue=computed(()=>batchReadQueue.value.length>0&&batchReadQueue.value.every(row=>row.share.canActivate))
function prepareBatchExpiry(queued=false,activate=false){
 if(locked.value||props.blocked)return
 if(activate){
  if(selectedKind.value!=='baidu'||!(queued?canActivateQueue.value:canActivateSelection.value))return
 }else if(!canEditBatchSettings.value||!(queued?canEditExpiryQueue.value:canEditExpirySelection.value))return
 const shares=queued?batchReadQueue.value.map(row=>row.share):batchChosen.value
 batchActivate.value=activate
 batchExpiryTargets.value=shares.map(row=>({...row}))
 readId.value='';detail.value=null;error.value='';message.value=''
 emit('mutation',true)
}
async function closeBatchExpiry(started:boolean){
 batchExpiryTargets.value=null
 emit('mutation',false)
 if(started){
  batchReadQueue.value=[]
  await nextTick()
  await load(page.value)
 }
}
function batchResult(handle:string,result:ShareRevocationResult){revokes.value=[result,...revokes.value.filter(r=>r.id!==result.id)];if(result.state==='confirmed'){items.value=items.value.filter(r=>r.id!==handle);batchSelection.value=batchSelection.value.filter(id=>id!==handle)}}
async function closeBatch(started=true){
 batchAcrossPages.value=false
 batchTargets.value=null
 emit('mutation',false)
 if(!started)return
 batchReadQueue.value=[];batchSelection.value=[]
 await nextTick();await load(page.value)
}
function canEditShare(row:ManagedShare){return row.canEdit||selectedKind.value==='baidu'&&!!row.canActivate}
function prepareEdit(row:ManagedShare){if(locked.value||props.blocked||!canEditShare(row))return;readId.value='';detail.value=null;error.value='';editTarget.value=row;emit('mutation',true)}
async function closeEdit(attempted:boolean){
 editTarget.value=null
 emit('mutation',false)
 if(!attempted)return
 batchReadQueue.value=[]
 await nextTick()
 await load(page.value)
}
const inventory=ref<{id:string;accountName:string;scopeLabel:string;kind:string}|null>(null)
const locked=computed(()=>busy.value||!!confirmTarget.value||!!editTarget.value||!!batchTargets.value||!!batchReadTargets.value||!!batchExpiryTargets.value||!!inventory.value||!!restrictionTarget.value)
async function openWebsite(){
 const url=shareWebsite(selectedKind.value)
 if(disposed||locked.value||props.blocked||!url)return
 const stamp=generation
 try{await Browser.OpenURL(url)}
 catch{if(!disposed&&stamp===generation)error.value='无法打开网盘官网'}
}
let inventoryTrigger:HTMLElement|null=null
function openInventory(event:Event){
 if(disposed||locked.value||props.blocked||!accountId.value)return
 const scopeLabel=selectedKind.value==='pan123'?(paidView.value?'付费分享':'免费分享'):
  quarkShareFilters.find(filter=>filter.value===quarkShareFilter.value)?.label??'全部状态'
 inventoryTrigger=event.currentTarget as HTMLElement
 inventory.value={id:accountId.value,accountName:selectedAccountName.value,scopeLabel,kind:selectedKind.value}
 emit('mutation',true)
}
function readInventoryPage(page:number,cursor?:string){
 const current=inventory.value
 if(!current||disposed||props.blocked||current.id!==accountId.value)throw Error('当前分享账号已变化')
 if(current.kind==='pan123'&&page>1&&!cursor)throw Error('123 云盘下一页标识缺失')
 return fetchSharePage(current.id,page,cursor)
}
async function closeInventory(){
 inventory.value=null
 emit('mutation',false)
 await nextTick()
 inventoryTrigger?.focus()
}
const canClearRevokes=computed(()=>revokes.value.some(r=>r.state==='confirmed'||r.state==='not_sent'))
const activity=ref('read'),readId=ref(''),autoFill=ref(true)
const detailURL=computed(()=>detail.value?managedShareText(detail.value,selectedKind.value,true,autoFill.value):'')
let generation=0,reload=false,disposed=false
function invalidate(){
 inventory.value=null
 closeRestrictions();batchReadQueue.value=[];batchReadTargets.value=null;batchExpiryTargets.value=null;batchTargets.value=null;batchSelection.value=[];readId.value='';editTarget.value=null;generation++;cursors.value={};items.value=[];detail.value=null;revokes.value=[];confirmTarget.value=null;error.value='';message.value='';page.value=1;hasMore.value=false
 if(busy.value){reload=true;return}
 emit('mutation',false)
 void load(1)
}
async function finish(){if(disposed){busy.value=false;return}busy.value=false;emit('mutation',false);if(reload){reload=false;await load(1)}}
async function loadReviewRevokes(){
 if(!accounts.value.some(a=>a.id===accountId.value))return
 const stamp=generation
 busy.value=true;error.value='';activity.value='read';emit('mutation',true)
 try{
  const results=await api.ManagedCloudShareRevokes(accountId.value)
  if(!disposed&&stamp===generation)revokes.value=results??[]
 }catch(e){if(!disposed&&stamp===generation)error.value=String(e)}
 finally{await finish()}
}
function browseCloudShares(){
 if(locked.value||props.blocked)return
 focusReceiptId.value=''
 if(!accounts.value.some(a=>a.id===accountId.value)){accountId.value=accounts.value[0]?.id??'';return}
 void load(1)
}
async function load(target=1){
 if(disposed||locked.value||props.blocked||!accountId.value)return
 if(focusReceiptId.value){await loadReviewRevokes();return}
 closeRestrictions();batchSelection.value=[];readId.value=''
 if(selectedKind.value==='pan123'&&target>1&&!cursors.value[target]){
  error.value='分页信息已变化，请刷新第一页'
  return
 }
 const stamp=generation
 activity.value='read';busy.value=true;error.value='';message.value='';detail.value=null;emit('mutation',true)
 try{
  const [remote,local]=await Promise.allSettled([fetchSharePage(accountId.value,target,cursors.value[target]),api.ManagedCloudShareRevokes(accountId.value)])
  if(stamp!==generation)return
  if(local.status==='fulfilled')revokes.value=local.value??[]
  else error.value=String(local.reason)
  if(remote.status==='rejected'){
   items.value=[];hasMore.value=false;error.value=String(remote.reason)
   return
  }
  const result=remote.value
  items.value=result.items??[]
  page.value=result.page
  hasMore.value=result.hasMore
  if(target===1)cursors.value={}
  if(result.next)cursors.value[target+1]=result.next
 }catch(e){
  if(stamp===generation)error.value=String(e)
 }finally{await finish()}
}
async function read(row:ManagedShare){if(locked.value||props.blocked)return;readId.value=row.id;const stamp=generation;activity.value='read';busy.value=true;error.value='';message.value='';detail.value=null;emit('mutation',true);try{const result=await api.ReadManagedCloudShare(row.id);if(stamp===generation){if(!result?.url)throw new Error('原分享未返回可用链接，请刷新列表后重试');detail.value=result}}catch(e){if(stamp===generation)error.value=String(e)}finally{await finish()}}
async function copy(linkOnly=false){if(locked.value||props.blocked||!detail.value?.url)return;const text=managedShareText(detail.value,selectedKind.value,linkOnly,autoFill.value);if(!text)return;activity.value='copy';busy.value=true;error.value='';try{await api.CopyDownloadReport(text);message.value='已复制原分享信息，没有新建分享'}catch(e){error.value=String(e)}finally{busy.value=false}}
function closeRestrictions(){
 restrictionTarget.value='';restrictions.value=null;restrictionName.value='';restrictionError.value=''
 restrictionQuery.value='';restrictionCopyStatus.value=''
}
async function closeRestrictionDialog(){
 if(busy.value||restrictionCopying.value)return
 const trigger=restrictionTrigger
 closeRestrictions()
 restrictionTrigger=null
 emit('mutation',false)
 await nextTick()
 trigger?.focus()
}
async function openRestrictions(row:ManagedShare,event?:Event){
 if(disposed||locked.value||props.blocked||selectedKind.value!=='quark'||!row.id||!row.quarkRestricted)return
 const stamp=generation
 restrictionTrigger=(event?.currentTarget as HTMLElement|undefined)??null
 restrictionTarget.value=row.id
 restrictionName.value=row.name
 restrictions.value=null
 restrictionError.value=''
 activity.value='restriction'
 busy.value=true
 emit('mutation',true)
 void nextTick(()=>restrictionDialog.value?.focus())
 try{
  const result=await api.ReadManagedQuarkShareRestrictions(row.id)
  if(!disposed&&stamp===generation)restrictions.value=result
 }catch(e){
  if(!disposed&&stamp===generation)restrictionError.value=String(e)
 }finally{
  if(!disposed&&stamp===generation)busy.value=false
  else await finish()
 }
}
async function copyRestrictions(){
 const snapshot=restrictions.value
 if(disposed||busy.value||props.blocked||restrictionCopying.value||!restrictionTarget.value||!snapshot?.items?.length)return
 const incomplete=snapshot.items.length<snapshot.reported
 const report=[
  [selectedAccountName.value,restrictionName.value].map(spreadsheetCell).join('\t'),
  '网盘报告 '+snapshot.reported+' 项受限 · 已读取 '+snapshot.items.length+' 项'+(incomplete?' · 清单不完整':''),
  '文件名\t所在目录\t原因',
  ...snapshot.items.map(row=>[row.name,row.parent,row.reason].map(spreadsheetCell).join('\t')),
 ].join('\r\n')
 restrictionCopying.value=true;restrictionCopyStatus.value=''
 try{
  await api.CopyDownloadReport(report)
  if(!disposed&&snapshot===restrictions.value)restrictionCopyStatus.value='已复制 '+snapshot.items.length+' 项明细。'
 }catch{
  if(!disposed&&snapshot===restrictions.value)restrictionCopyStatus.value='无法写入剪贴板。'
 }finally{restrictionCopying.value=false}
}
function prepareRevoke(row:ManagedShare){if(locked.value||props.blocked||!canRevoke.value||!row.canRevoke)return;readId.value='';detail.value=null;error.value='';confirmTarget.value=row;emit('mutation',true)}
function cancelRevoke(){if(busy.value)return;confirmTarget.value=null;emit('mutation',false)}
async function revoke(){if(busy.value||props.blocked||!confirmTarget.value)return;const target=confirmTarget.value,stamp=generation;confirmTarget.value=null;batchReadQueue.value=[];activity.value='revoke';busy.value=true;detail.value=null;error.value='';message.value='';try{const result=await api.RevokeManagedCloudShare(target.id);if(stamp!==generation)return;revokes.value=[result,...revokes.value.filter(r=>r.id!==result.id)];message.value=result.message;if(result.state==='confirmed')items.value=items.value.filter(r=>r.id!==target.id);const logs=await api.ManagedCloudShareRevokes(accountId.value);if(stamp===generation)revokes.value=logs??[]}catch(e){if(stamp===generation)error.value=String(e)+'；请刷新撤销回执核对，不要反复提交'}finally{await finish()}}
async function checkRevoke(row:ShareRevocationResult){if(locked.value||props.blocked||!row.canCheck)return;const stamp=generation;activity.value='check';busy.value=true;detail.value=null;error.value='';message.value='';emit('mutation',true);try{const result=await api.CheckManagedCloudShareRevoke(row.id);if(stamp!==generation)return;message.value=result.message;const logs=await api.ManagedCloudShareRevokes(accountId.value);if(stamp===generation)revokes.value=logs??[]}catch(e){if(stamp===generation)error.value=String(e)}finally{await finish()}}
async function clearRevokes(){if(locked.value||props.blocked)return;const stamp=generation;activity.value='clear';busy.value=true;error.value='';message.value='';emit('mutation',true);try{const count=await api.ClearManagedCloudShareRevokes(accountId.value);if(stamp!==generation)return;const logs=await api.ManagedCloudShareRevokes(accountId.value);if(stamp===generation)revokes.value=logs??[];message.value='已清理 '+count+' 条本机已确认或未提交回执，未知回执保留；没有发送撤销请求'}catch(e){if(stamp===generation)error.value=String(e)}finally{await finish()}}
function revokeLabel(state:string){return ({working:'处理中',unknown:'待确认',confirmed:'结果已确认',not_sent:'未提交'} as Record<string,string>)[state]??state}
watch(accountId,resetAccountView)
watch(accountScope,()=>{const next=focusReceiptId.value||accounts.value.some(a=>a.id===accountId.value)?accountId.value:accounts.value[0]?.id??'';if(next!==accountId.value)accountId.value=next;else invalidate()})
onMounted(()=>load())
onUnmounted(()=>{disposed=true;generation++;emit('mutation',false)})
</script>
<template>
 <section class="cloud-share-list">
  <div v-if="focusReceiptId" class="share-history-actions"><strong>{{selectedAccountName}}</strong><button class="secondary" :disabled="locked||blocked" @click="browseCloudShares">打开云端分享列表</button><button class="secondary" :disabled="locked||blocked" @click="load()">刷新本机回执</button></div>
  <p v-if="focusReceiptId&&error" class="banner error" role="alert">{{error}}</p>
  <p v-if="focusReceiptId&&message" class="banner notice" role="status">{{message}}</p>
  <p v-if="focusReceiptId&&!busy&&!visibleRevokes.length" class="muted">原撤销回执已不存在或不可读取。</p>
  <template v-if="!focusReceiptId">
  <details class="operation-details"><summary>云端记录范围</summary><p>包含官网创建的分享。百度每页最多 100 条，其他网盘每页最多 50 条。</p></details>
 <div class="share-history-filters"><select v-model="accountId" :disabled="locked||blocked" aria-label="选择云端分享账号"><option v-if="!accounts.length" value="">暂无支持的网盘账号</option><option v-for="a in accounts" :key="a.id" :value="a.id">{{accountLabel(a)}}</option></select><select v-if="selectedKind==='pan123'" :value="shareType" :disabled="locked||blocked" aria-label="123 云盘分享类型" @change="chooseShareType(($event.target as HTMLSelectElement).value)"><option value="free">免费分享</option><option value="paid">付费分享</option></select><select v-if="selectedKind==='quark'" :value="quarkShareFilter" :disabled="locked||blocked" aria-label="夸克分享状态" @change="chooseQuarkShareFilter(($event.target as HTMLSelectElement).value)"><option v-for="filter in quarkShareFilters" :key="filter.value" :value="filter.value">{{filter.label}}</option></select><button class="secondary" :disabled="locked||blocked||!accountId" @click="batchReadQueue=[];load(page)">刷新云端列表</button><button class="secondary" :disabled="locked||blocked||!accountId" @click="openInventory">云端分享清单…</button><button v-if="shareWebsite(selectedKind)" class="secondary" :disabled="locked||blocked" @click="openWebsite">打开网盘官网</button></div>
  <div class="share-history-actions">
   <button class="secondary" :disabled="locked||blocked||!items.length" @click="selectBatch()">选择本页（最多50项）</button>
   <button v-if="items.some(row=>row.canRevoke)&&items.some(row=>!row.canRevoke)" class="secondary" :disabled="locked||blocked" @click="selectBatch(true)">选择本页可撤销项</button>
   <button class="secondary" :disabled="locked||blocked||!batchSelection.length" @click="batchSelection=[]">取消选择</button>
   <button class="secondary" :disabled="locked||blocked||!batchChosen.length" @click="prepareBatchRead">批量读取所选（{{batchChosen.length}}）</button>
   <button class="secondary" :disabled="locked||blocked||!canQueueReadSelection" title="跨页清单最多 50 条" @click="queueReadSelection">加入跨页分享清单</button>
   <button v-if="canRevoke" class="secondary" :disabled="locked||blocked||!canRevokeBatch" @click="prepareBatch()">批量撤销所选（{{batchChosen.length}}）</button>
   <button v-if="canEditBatchSettings" class="secondary" :disabled="locked||blocked||!canEditExpirySelection" @click="prepareBatchExpiry()">{{paidView?'批量编辑下载设置…':selectedKind==='baidu'?'批量修改有效期…':'批量编辑分享设置…'}}</button>
   <button v-if="selectedKind==='baidu'&&canActivateSelection" class="secondary" :disabled="locked||blocked" @click="prepareBatchExpiry(false,true)">批量激活原链接…</button>
  </div>
  <section v-if="batchReadQueue.length" class="share-history-card" aria-label="跨页分享清单">
   <div class="share-history-card-heading"><strong>跨页分享清单（{{batchReadQueue.length}} / 50）</strong><button class="secondary" :disabled="locked||blocked" @click="batchReadQueue=[]">清空清单</button></div>
   <ul class="recycle-batch-preview">
    <li v-for="{share,page:sourcePage} in batchReadQueue" :key="share.selectionKey">
     <div class="share-history-card-heading"><strong>{{share.name}}</strong><button class="secondary" :disabled="locked||blocked" :aria-label="'从清单移除 '+share.name" @click="removeQueuedShare(share.selectionKey)">移除</button></div>
     <p>选自第 {{sourcePage}} 页</p>
    </li>
   </ul>
   <div class="share-history-actions">
    <button class="primary" :disabled="locked||blocked" @click="prepareBatchRead($event,true)">查看整批读取</button>
    <button v-if="canRevoke" class="secondary" :disabled="locked||blocked||!canRevokeQueue" @click="prepareBatch(true)">查看整批撤销…</button>
    <button v-if="canEditBatchSettings" class="secondary" :disabled="locked||blocked||!canEditExpiryQueue" @click="prepareBatchExpiry(true)">{{paidView?'编辑清单下载设置…':selectedKind==='baidu'?'修改清单有效期…':'编辑清单分享设置…'}}</button>
    <button v-if="selectedKind==='baidu'&&canActivateQueue" class="secondary" :disabled="locked||blocked" @click="prepareBatchExpiry(true,true)">激活清单原链接…</button>
   </div>
   <p v-if="canRevoke&&!canRevokeQueue" class="muted">整批撤销要求清单内每项均可撤销，且位于前 {{selectedKind==='baidu'?5:10}} 页。</p>
  </section>
  <p v-if="error" class="banner error" role="alert">{{error}}</p><p v-if="message" class="banner notice" role="status">{{message}}</p><p v-if="busy" role="status">{{activity==='revoke'?'正在提交并核对撤销，请勿重复操作…':activity==='check'?'正在检查原撤销结果，不会重复提交…':'正在处理…'}}</p>

    <section v-if="confirmTarget" class="share-history-card" role="alertdialog" aria-label="确认撤销分享"><strong>{{paidView?'撤销付费分享':'撤销分享'}}：{{confirmTarget.name}}</strong><p class="dialog-location">{{selectedAccountName}}</p><p>原分享链接将失效，网盘文件保留。此操作不能恢复原链接；结果未知时请检查原回执，不要重复提交。</p><div class="share-history-actions"><button class="primary" :disabled="busy||blocked" @click="revoke">确认撤销分享</button><button class="secondary" :disabled="busy" @click="cancelRevoke">取消</button></div></section>
  </template>
  <section v-if="visibleRevokes.length" class="share-history-card"><strong>本机撤销回执（重启保留）</strong><button v-if="!focusReceiptId" class="secondary" :disabled="locked||blocked||!canClearRevokes" @click="clearRevokes">清理此账号的已确认回执</button><article v-for="r in visibleRevokes" :key="r.id"><p><strong>{{r.name}}</strong><span v-if="r.paid123"> · 付费分享</span> · {{revokeLabel(r.state)}}</p><p>{{r.message}}</p><button v-if="r.canCheck" class="secondary" :disabled="locked||blocked" @click="checkRevoke(r)">检查原撤销结果</button></article></section>
  <template v-if="!focusReceiptId">
  <p v-if="!items.length&&!busy&&!error" class="share-history-empty">{{!accounts.length?'请先添加夸克、123、百度或光鸭账号。':'本页没有云端分享。'}}</p>
  <article v-for="row in items" :key="row.id" class="share-history-card"><div class="share-history-card-heading"><label><input type="checkbox" :checked="batchSelection.includes(row.id)" :disabled="locked||blocked||batchSelection.length>=50&&!batchSelection.includes(row.id)" :aria-label="'选择分享：'+row.name" @change="toggleBatch(row.id,($event.target as HTMLInputElement).checked)"/> <strong>{{row.name}}</strong></label><span class="muted">{{row.status}}</span></div><p>{{row.created?'创建：'+row.created+' · ':''}}{{row.expiry}}<span v-if="quarkAccessLabel(row.quarkAccess)"> · {{quarkAccessLabel(row.quarkAccess)}}</span></p><p v-if="paidView&&(row.priceFen!=null||row.earningsFen!=null)"><span v-if="row.priceFen!=null">定价 ¥{{(row.priceFen/100).toFixed(2)}}</span><span v-if="row.priceFen!=null&&row.earningsFen!=null"> · </span><span v-if="row.earningsFen!=null">收益 ¥{{(row.earningsFen/100).toFixed(2)}}</span></p><div class="share-history-actions"><button class="secondary" :disabled="locked||blocked" @click="read(row)">{{busy&&readId===row.id&&activity==='read'?'正在读取…':'读取原分享链接'}}</button><button v-if="row.quarkRestricted" class="secondary" :disabled="locked||blocked" @click="openRestrictions(row,$event)">查看受限文件（{{row.quarkRestricted}}）</button><button v-if="row.canEdit||row.canActivate" class="secondary" :disabled="locked||blocked" @click="prepareEdit(row)">{{row.canActivate?'激活原链接…':'编辑分享设置'}}</button><button v-if="canRevoke" class="secondary" :disabled="locked||blocked||!row.canRevoke" @click="prepareRevoke(row)">撤销分享</button></div>
   <section v-if="readId===row.id" class="cloud-share-result" aria-live="polite" :aria-busy="busy">
    <p v-if="busy&&activity==='read'" role="status">正在读取这个分享的原链接和提取码…</p>
    <p v-if="error&&!busy" class="banner error" role="alert">{{error}}</p>
    <template v-if="detail"><p role="status">{{detail.message||'已读取原分享链接'}}</p><label v-if="['baidu','guangya'].includes(selectedKind)" class="share-switch"><input v-model="autoFill" type="checkbox" :disabled="busy||blocked"/>链接自动填充提取码</label><label>原分享链接<input :value="detailURL" readonly :aria-label="row.name+'的原分享链接'"/></label><p>提取码：{{detail.password||'无'}} · {{detail.expiry}}</p><div class="share-history-actions"><button class="secondary" :disabled="locked||blocked" @click="copy(false)">复制完整信息</button><button class="secondary" :disabled="locked||blocked" @click="copy(true)">只复制链接</button></div><p v-if="message" role="status">{{message}}</p></template>
   </section>
  </article>
  <div v-if="accountId" class="share-history-actions"><button class="secondary" :disabled="locked||blocked||page<=1" @click="load(page-1)">上一页</button><span>第 {{page}} 页 · 本页 {{items.length}} 条</span><button class="secondary" :disabled="locked||blocked||!hasMore" @click="load(page+1)">下一页</button></div>
  <p v-if="paidView" class="muted">付费分享可读取原链接，也可单条或批量撤销。撤销使原链接失效，网盘文件保留。结果待确认时请检查原回执。符合条件的分享可修改主题、下载权限和流量上限。</p><p v-else class="muted">撤销会让原分享链接失效，不删除网盘文件；结果未知时只检查原回执，不重复提交。核对最多10页/500项，超出范围请到官网核对。光鸭支持修改原提取码和期限；百度和符合条件的 123 分享支持修改原期限。</p>
 <BatchShareRevokeDialog v-if="batchTargets" :account-name="selectedAccountName" :targets="batchTargets" :blocked="blocked" :preflight="paidView||!!quarkShareFilter||batchAcrossPages" :paid="paidView" @result="batchResult" @close="closeBatch"/>
 <BatchManagedShareReadDialog v-if="batchReadTargets" :account-name="selectedAccountName" :kind="selectedKind" :targets="batchReadTargets" :blocked="blocked" @close="closeBatchRead"/>
 <BatchShareExpiryDialog v-if="batchExpiryTargets" :account-name="selectedAccountName" :kind="selectedKind" :targets="batchExpiryTargets" :blocked="blocked" :activate="batchActivate" :paid="paidView" @close="closeBatchExpiry"/>
 <ShareSettingsDialog v-if="editTarget" :kind="selectedKind" :handle="editTarget.id" :name="editTarget.name" :account-name="selectedAccountName" :activate="editTarget.canActivate" :original-expiry="editTarget.expiry" :paid="paidView" @close="closeEdit"/>
 <CloudShareInventory v-if="inventory" :account-name="inventory.accountName" :scope-label="inventory.scopeLabel"
  :read-page="readInventoryPage" :blocked="blocked" @close="closeInventory"/>
 <div v-if="restrictionTarget" class="modal-backdrop">
  <section ref="restrictionDialog" class="operation-dialog share-dialog" role="dialog" tabindex="-1" aria-modal="true"
   aria-labelledby="quark-restriction-title" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="closeRestrictionDialog">
   <div class="modal-heading">
    <h2 id="quark-restriction-title">受限文件</h2>
    <button class="secondary" :disabled="busy||restrictionCopying" @click="closeRestrictionDialog">关闭</button>
   </div>
   <p class="dialog-location">{{selectedAccountName}} · {{restrictionName}}</p>
   <p v-if="busy" role="status">正在读取受限文件…</p>
   <p v-else-if="restrictionError" class="banner error" role="alert">{{restrictionError}}</p>
   <template v-else-if="restrictions">
    <p>网盘报告 {{restrictions.reported}} 项受限，已读取 {{restrictionItems.length}} 项。</p>
    <p v-if="restrictionItems.length<restrictions.reported" class="muted">清单不完整，完整明细请到夸克官网查看。</p>
    <div v-if="restrictionItems.length" class="share-history-filters">
     <input v-model="restrictionQuery" type="search" placeholder="搜索文件名、目录或原因" aria-label="搜索受限文件明细"/>
     <button class="secondary" :disabled="blocked||restrictionCopying" @click="copyRestrictions">复制已读取的明细</button>
    </div>
    <p v-if="restrictionCopyStatus" role="status">{{restrictionCopyStatus}}</p>
    <p v-if="restrictionQuery.trim()&&!filteredRestrictions.length">没有符合筛选条件的文件。</p>
    <ul v-if="filteredRestrictions.length" class="recycle-batch-preview">
     <li v-for="(item,index) in filteredRestrictions" :key="index">
      <strong>{{item.name}}</strong><p>{{item.reason||'网盘未返回具体原因'}}</p><p v-if="item.parent" class="muted">所在目录 {{item.parent}}</p>
     </li>
    </ul>
   </template>
  </section>
 </div>
  </template>
 </section>
</template>
