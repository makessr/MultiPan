<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref} from 'vue'
import BatchOperationReport from './BatchOperationReport.vue'
import {trapDialogFocus} from './dialogFocus'
import * as api from '../bindings/multipan/desktopservice'
import type {ManagedShare,ShareRevocationResult} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accountName:string;targets:ManagedShare[];blocked?:boolean;preflight?:boolean;paid?:boolean}>()
const emit=defineEmits<{close:[started:boolean];result:[handle:string,result:ShareRevocationResult]}>()
const targets=props.targets.map(t=>({...t}))
type Row={handle:string;name:string;state:string;message:string;receipt?:ShareRevocationResult}
const rows=ref<Row[]>(targets.map(t=>({handle:t.id,name:t.name,state:'queued',message:'尚未提交'})))
const running=ref(false),started=ref(false),stopRequested=ref(false),ack=ref(false),confirmText=ref(''),checking=ref(''),error=ref('')
const preflighting=ref(false),resuming=ref(false)
const ackControl=ref<HTMLInputElement|null>(null)
let disposed=false
let attempted=false
const valid=computed(()=>targets.length>0&&targets.length<=50&&new Set(targets.map(t=>t.id)).size===targets.length&&targets.every(t=>t.id&&t.name&&t.canRevoke))
const canStart=computed(()=>valid.value&&!started.value&&!running.value&&!props.blocked&&ack.value&&confirmText.value==='撤销分享')
const confirmed=computed(()=>rows.value.filter(r=>r.state==='confirmed').length)
const remaining=computed(()=>rows.value.filter(r=>r.state==='skipped'))
const hasUnknown=computed(()=>rows.value.some(r=>r.state==='unknown'))
const canResume=computed(()=>valid.value&&started.value&&!running.value&&!checking.value&&!props.blocked&&!hasUnknown.value&&remaining.value.length>0)
function label(state:string){return ({queued:'未开始',working:'正在处理',confirmed:'已确认撤销',unknown:'待核对',not_sent:'未提交',skipped:'已停止后续'} as Record<string,string>)[state]??'待核对'}
function stop(){stopRequested.value=true}
function close(){if(!running.value&&!checking.value)emit('close',attempted)}
function prepareResume(){
 if(disposed||!canResume.value)return
 for(const row of remaining.value){row.state='queued';row.message='尚未提交'}
 resuming.value=true;started.value=false;ack.value=false;confirmText.value='';error.value=''
}
async function run(){
 if(disposed||!canStart.value)return
 const pending=rows.value.filter(row=>row.state==='queued')
 if(!pending.length)return
 attempted=true;running.value=true;started.value=true;stopRequested.value=false;error.value=''
 try{
  if(props.preflight||resuming.value){
   preflighting.value=true
   try{
    const handles=pending.map(row=>row.handle)
    if(resuming.value)await api.ValidateManagedCloudShareRevokeResume(handles)
    else await api.ValidateManagedCloudShareRevokeBatch(handles)
   }catch(e){
    if(!disposed)error.value='整批预检未通过，未提交撤销。'+String(e)
    return
   }finally{preflighting.value=false}
  }
  for(const row of pending){
   if(disposed||stopRequested.value||props.blocked)break
   row.state='working';row.message='正在检查原分享、提交并核对；请勿重复操作'
   try{
    const result=await api.RevokeManagedCloudShare(row.handle)
    if(disposed)return
    if(!result||!['confirmed','unknown','not_sent'].includes(result.state))throw new Error('unconfirmed response')
    row.state=result.state;row.message=result.message;row.receipt=result
    emit('result',row.handle,result)
    if(result.state!=='confirmed'){stopRequested.value=true;break}
   }catch{
    if(disposed)return
    row.state='unknown';row.message='本项结果未能确认，请刷新本机撤销回执或到官网核对；不会重复提交'
    stopRequested.value=true;break
   }
  }
 }finally{
  if(!disposed){for(const row of pending)if(row.state==='queued'){row.state='skipped';row.message='尚未提交'}running.value=false}
 }
}
async function check(row:Row){
 if(disposed||running.value||checking.value||props.blocked||!rows.value.includes(row)||row.state!=='unknown'||!row.receipt?.canCheck||!row.receipt.id)return
 const receiptId=row.receipt.id
 checking.value=row.handle;error.value=''
 try{const result=await api.CheckManagedCloudShareRevoke(receiptId);if(disposed)return;if(result?.id!==receiptId||!['confirmed','unknown','not_sent'].includes(result.state))throw Error('invalid result');row.receipt=result;row.state=result.state;row.message=result.message;emit('result',row.handle,result)}
 catch{if(!disposed)error.value='只读核对未能完成，保留原状态；请稍后刷新撤销回执，不会重新提交撤销'}
 finally{if(!disposed)checking.value=''}
}
onMounted(()=>ackControl.value?.focus())
onUnmounted(()=>{disposed=true;stopRequested.value=true})
</script>
<template>
<div class="modal-backdrop"><section class="account-modal batch-revoke-dialog" role="dialog" aria-modal="true" aria-labelledby="batch-revoke-title" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="close">
 <div class="modal-heading"><div><h2 id="batch-revoke-title">{{paid?'批量撤销付费分享':'批量撤销分享'}}</h2><p>{{accountName}} · {{targets.length}} 项</p></div><button class="icon-button" aria-label="关闭批量撤销" :disabled="running||!!checking" @click="close">×</button></div>
 <p class="preview-note">所选原链接将失效，不能恢复原链接，网盘文件保留。每次最多50项；逐项重新检查、提交和核对，首项失败或待核对会停止后续，不回滚已撤销分享。未开始项目不跨重启恢复。</p>
 <p v-if="!valid" class="banner error" role="alert">所选项目无效、重复、超过50项或不允许撤销；本批不会提交，请关闭后重新选择。</p>
 <p v-if="error" class="banner error" role="alert">{{error}}</p>
 <ol class="batch-revoke-rows"><li v-for="row in rows" :key="row.handle"><div><strong>{{row.name}}</strong><span>{{label(row.state)}}</span></div><p>{{row.message}}</p><button v-if="row.receipt?.canCheck" class="secondary" :disabled="running||!!checking||blocked" @click="check(row)">{{checking===row.handle?'正在只读核对…':'检查原撤销结果'}}</button></li></ol>
 <fieldset v-if="!started" :disabled="blocked"><label class="revoke-ack"><input ref="ackControl" v-model="ack" type="checkbox"/>{{resuming?'我确认撤销标为「未开始」的分享，理解原链接失效且不能恢复。':'我已确认所选分享，理解原链接失效且不能恢复。'}}</label><label>输入“撤销分享”确认<input v-model="confirmText" autocomplete="off" placeholder="撤销分享"/></label></fieldset>
 <p v-if="started" role="status">已确认撤销 {{confirmed}} / {{targets.length}} 项。{{preflighting?'正在只读预检本轮项目。':running?(stopRequested?'正在等待当前项目结束，随后停止。':'正在逐项处理。'):hasUnknown?'请先核对待确认的原回执。':'本轮已结束。'}}</p>
 <BatchOperationReport v-if="started&&!running" operation="批量撤销分享" :account-name="accountName" :rows="rows" :disabled="!!checking||blocked"/>
 <div class="modal-actions"><button v-if="running" class="secondary" :disabled="stopRequested" @click="stop">停止后续项目</button><button v-else class="secondary" :disabled="!!checking" @click="close">{{started?'关闭结果':'取消'}}</button><button v-if="canResume" class="secondary" @click="prepareResume">继续未提交项目（{{remaining.length}}）…</button><button v-if="!started" class="primary" :disabled="!canStart" @click="run">{{resuming?'确认撤销剩余分享':'确认撤销所选分享'}}</button></div>
</section></div>
</template>
<style scoped>
.batch-revoke-dialog{width:min(720px,calc(100vw - 40px));max-height:calc(100vh - 40px);overflow:auto}.batch-revoke-rows{max-height:40vh;overflow:auto;margin:16px 0;padding-left:28px}.batch-revoke-rows li{padding:10px 8px;border-bottom:1px solid var(--border,#e4e8ee);overflow-wrap:anywhere}.batch-revoke-rows li>div{display:flex;justify-content:space-between;gap:16px}.batch-revoke-rows p{font-size:13px}.batch-revoke-dialog fieldset{display:grid;gap:12px;margin:16px 0}.batch-revoke-dialog fieldset>label{display:flex;flex-direction:column;gap:8px}.batch-revoke-dialog fieldset>.revoke-ack{flex-direction:row;align-items:center}.revoke-ack input{width:auto}
</style>
