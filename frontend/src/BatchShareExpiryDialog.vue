<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref} from 'vue'
import BatchOperationReport from './BatchOperationReport.vue'
import {trapDialogFocus} from './dialogFocus'
import {pan123PermissionOptions,pan123TrafficLimit} from './pan123ShareTraffic'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {ManagedShare,ManagedShareLink,ShareResult} from '../bindings/multipan/internal/transfer/models'
type Target=Pick<ManagedShare,'id'|'name'|'canEdit'|'canActivate'|'canLimitTraffic123'>&{expiry?:string;sourcePath?:string}
type OriginalAccess=Pick<ManagedShareLink,'url'|'password'>
type DirectoryPassword={
 read:(id:string)=>Promise<ManagedShareLink>
 readGroup?:(ids:string[])=>Promise<(ManagedShareLink|null)[]|null>
 update:(id:string,original:OriginalAccess,password:string)=>Promise<ShareResult>
 clear:(id:string,original:OriginalAccess)=>Promise<ShareResult>
}
const props=defineProps<{accountName:string;kind:string;targets:Target[];blocked?:boolean;directoryPassword?:DirectoryPassword;activate?:boolean;paid?:boolean}>()
const emit=defineEmits<{close:[submitted:boolean]}>()
type Row={share:Target;state:string;message:string;receiptId?:string;originalURL?:string}
const rows=ref<Row[]>(props.targets.map(share=>({share:{...share},state:'queued',message:''})))
const dialog=ref<HTMLElement|null>(null)
const days=ref(-1),agreed=ref(false),busy=ref(false),started=ref(false),stopping=ref(false),preflight=ref(false),error=ref('')
const checking=ref('')
const checkedLinks=ref(0),attemptSize=ref(0),attemptSubmitted=ref(false),retrying=ref(false)
const access=ref<'set'|'clear'|''>(props.kind==='lanzou'?'set':'')
const password=ref('')
const edit123=ref<'expiry'|'permissions'|'limit'>(props.paid?'permissions':'expiry')
const permission=ref(0),limitMode=ref(''),limitAmount=ref(''),limitUnit=ref(2**30)
const changeLimitWithPermission=ref(false)
const canLimit123=computed(()=>props.kind==='pan123'&&rows.value.every(row=>row.share.canLimitTraffic123))
const limit123=computed(()=>pan123TrafficLimit(limitMode.value,limitAmount.value,limitUnit.value))
const editCombined123=computed(()=>props.kind==='pan123'&&edit123.value==='permissions'&&permission.value>1&&changeLimitWithPermission.value)
const isGuangya=computed(()=>props.kind==='guangya')
const isLanzou=computed(()=>props.kind==='lanzou'&&!!props.directoryPassword)
const choices=computed(()=>props.kind==='baidu'?[1,3,7,30]:[1,7,30])
const valid=computed(()=>(props.activate?props.kind==='baidu':isLanzou.value||['baidu','pan123','guangya'].includes(props.kind))&&rows.value.length>0&&rows.value.length<=50&&
 rows.value.every(row=>row.share.id&&(props.activate?row.share.canActivate:row.share.canEdit))&&new Set(rows.value.map(row=>row.share.id)).size===rows.value.length)
const accessChosen=computed(()=>!isGuangya.value||access.value==='clear'||access.value==='set'&&/^[A-Za-z0-9]{4}$/.test(password.value))
const retryable=computed(()=>rows.value.filter(row=>row.state==='skipped'||isLanzou.value&&row.state==='failed'))
const hasUnknown=computed(()=>rows.value.some(row=>row.state==='unknown'))
const canRetry=computed(()=>valid.value&&started.value&&!busy.value&&!checking.value&&!props.blocked&&!hasUnknown.value&&retryable.value.length>0)
const canStart=computed(()=>{
 if(!valid.value||busy.value||started.value||props.blocked||!agreed.value)return false
 if(props.activate)return true
 if(props.kind==='pan123'&&edit123.value==='permissions')return pan123PermissionOptions.some(row=>row.value===permission.value)&&(!editCombined123.value||limit123.value!==null)
 if(props.kind==='pan123'&&edit123.value==='limit')return canLimit123.value&&limit123.value!==null
 if(props.paid)return false
 if(isLanzou.value)return access.value==='clear'||access.value==='set'&&/^[A-Za-z0-9]{1,6}$/.test(password.value)
 return accessChosen.value&&(days.value===0||choices.value.includes(days.value))
})
const done=computed(()=>rows.value.filter(row=>row.state==='ready').length)
const labels:Record<string,string>={queued:'未开始',working:'处理中',ready:'已核对',failed:'失败',unknown:'待确认',skipped:'未提交'}
let disposed=false
let submitted=false
const completedLinks=new Set<string>()
function close(){if(!busy.value&&!checking.value)emit('close',submitted)}
function shouldStop(){return disposed||props.blocked||stopping.value}
function prepareRetry(){
 if(disposed||!canRetry.value)return
 for(const row of retryable.value)row.state='queued'
 retrying.value=true
 agreed.value=false
 error.value=''
 started.value=false
 dialog.value?.focus()
}

async function checkDirectoryLinks(directory:DirectoryPassword,targets:Row[]){
 const originalLinks=new Map<string,OriginalAccess>()
 const seenLinks=new Set(completedLinks)
 const groupSize=directory.readGroup?5:1
 for(let pos=0;pos<targets.length;pos+=groupSize){
  if(shouldStop())return null
  const group=targets.slice(pos,pos+groupSize)
  let links:(ManagedShareLink|null)[]|null=null
  if(group.length>1&&directory.readGroup){
   try{links=await directory.readGroup(group.map(row=>row.share.id))}
   catch{ /* Read this group one item at a time if the combined request fails. */ }
  }
  for(const [i,row] of group.entries()){
   if(shouldStop())return null
   try{
    const link=(links?.length===group.length?links[i]:null)??await directory.read(row.share.id)
    if(shouldStop())return null
    if(!link.url)throw Error('未取得原链接')
    if(typeof link.password!=='string')throw Error('未取得原链接的提取码状态')
    if(seenLinks.has(link.url))throw Error('同一个分享链接被选中多次，请只保留一条')
    seenLinks.add(link.url)
    originalLinks.set(row.share.id,{url:link.url,password:link.password})
    checkedLinks.value=originalLinks.size
   }catch(e){
    throw Error(row.share.name+'：'+String(e))
   }
  }
 }
 return originalLinks
}

function recordResult(row:Row,result:ShareResult){
 row.state=result.state==='ready'||result.state==='failed'?result.state:'unknown'
 row.message=result.message
 row.receiptId=row.state==='unknown'&&result.canCheck?result.id:undefined
 if(row.state==='ready'&&row.originalURL)completedLinks.add(row.originalURL)
}

async function check(row:Row){
 if(disposed||busy.value||checking.value||props.blocked||!rows.value.includes(row)||row.state!=='unknown'||!row.receiptId)return
 const receiptId=row.receiptId
 checking.value=row.share.id;error.value=''
 try{
  const result=await api.CheckCloudShare(receiptId)
  if(disposed)return
  if(result?.id!==receiptId||!['ready','failed','unknown'].includes(result.state))throw Error('回执结果不匹配')
  recordResult(row,result)
 }catch{
  if(!disposed)error.value='本次未能完成核对，原回执已保留。'
 }finally{checking.value=''}
}

async function run(){
 if(disposed||!canStart.value)return
 const activate=props.activate
 const targetDays=days.value
 const targetPermission=props.kind==='pan123'&&edit123.value==='permissions'?permission.value:0
 const targetLimit=props.kind==='pan123'&&(edit123.value==='limit'||editCombined123.value)?limit123.value:null
 const directoryPassword=isLanzou.value?props.directoryPassword:undefined
 const clearDirectoryPassword=access.value==='clear'
 const targetPassword=(directoryPassword||isGuangya.value)&&access.value==='set'?password.value:''
 const targets=rows.value.filter(row=>row.state==='queued')
 const handles=targets.map(row=>row.share.id)
 checkedLinks.value=0;attemptSize.value=targets.length;attemptSubmitted.value=false;stopping.value=false
 started.value=true;busy.value=true;preflight.value=true
 try{
  let originalLinks:Map<string,OriginalAccess>|null=null
  if(activate)await api.ValidateBaiduCloudShareActivationBatch(handles)
  else if(targetPermission&&targetLimit!==null)await api.ValidatePan123CloudShareDownloadSettingsBatch(handles,targetPermission,targetLimit)
  else if(targetPermission||targetLimit!==null)await api.ValidatePan123CloudShareTrafficBatch(handles,targetPermission,targetLimit??0)
  else if(directoryPassword){
   originalLinks=await checkDirectoryLinks(directoryPassword,targets)
   if(!originalLinks)return
  }else if(isGuangya.value)await api.ValidateGuangyaCloudShareSettingsBatch(handles,targetPassword,targetDays)
  else await api.ValidateManagedCloudShareExpiryBatch(handles,targetDays)
  preflight.value=false
  for(const row of targets){
   if(shouldStop())break
   row.state='working'
   row.message=''
   submitted=true
   attemptSubmitted.value=true
   try{
    let result:ShareResult
    const original=originalLinks?.get(row.share.id)
    row.originalURL=original?.url
    if(activate)result=await api.ActivateManagedBaiduShare(row.share.id)
    else if(targetPermission&&targetLimit!==null)result=await api.SetManagedPan123ShareDownloadSettings(row.share.id,targetPermission,targetLimit)
    else if(targetPermission)result=await api.SetManagedPan123ShareTrafficMode(row.share.id,targetPermission)
    else if(targetLimit!==null)result=await api.SetManagedPan123ShareTrafficLimit(row.share.id,targetLimit)
    else if(directoryPassword){
     if(!original)throw Error('未取得原分享设置')
     result=await (clearDirectoryPassword?directoryPassword.clear(row.share.id,original):directoryPassword.update(row.share.id,original,targetPassword))
    }
    else result=await api.UpdateManagedCloudShare(row.share.id,targetPassword,targetDays)
    if(disposed)return
    recordResult(row,result)
   }catch{
    if(disposed)return
    row.state='unknown'
    row.message='无法确认本项结果，请先在本机分享记录核对。'
   }
   if(row.state!=='ready')break
  }
 }catch(e){
  if(!disposed)error.value='本轮预检未通过，未修改任何分享。'+String(e)
 }finally{
  if(!disposed)for(const row of targets)if(row.state==='queued')row.state='skipped'
  preflight.value=false;busy.value=false
 }
}
onMounted(()=>dialog.value?.focus())
onUnmounted(()=>{disposed=true})
</script>
<template>
 <div class="modal-backdrop" @click.self="close">
  <section ref="dialog" tabindex="-1" class="operation-dialog share-dialog batch-expiry-dialog" role="dialog" aria-modal="true" aria-labelledby="batch-expiry-title" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="close">
   <div class="modal-heading"><h2 id="batch-expiry-title">{{activate?'批量激活百度原链接':isLanzou?'批量修改蓝奏提取码':paid?'批量编辑付费分享下载设置':isGuangya||kind==='pan123'?'批量编辑原分享设置':'批量修改原分享有效期'}}</h2><button class="secondary" :disabled="busy||!!checking" @click="close">关闭</button></div>
   <p>{{accountName}} · {{rows.length}} 条</p>
   <p class="preview-note">{{activate?'恢复原链接访问，沿用各条分享的提取码和有效期。':isLanzou?'可统一设置新提取码或取消提取码，沿用原链接和有效期。':isGuangya?'保留原链接，统一修改所选分享的有效期和访问方式。':'保留原链接和提取码。'}}整批预检通过后逐项提交，遇到失败或待确认时停止后续。</p>
   <p v-if="kind==='pan123'&&!paid&&edit123==='expiry'" class="preview-note">123 的期限选项受原分享创建时间限制，永久期限设至 2099 年。</p>
   <ol class="batch-expiry-rows">
    <li v-for="row in rows" :key="row.share.id">
     <div class="share-history-card-heading"><strong>{{row.share.name}}</strong><span class="muted">{{labels[row.state]}}</span></div>
     <p v-if="row.share.sourcePath" class="muted">{{row.share.sourcePath}}</p>
     <p>{{row.message||row.share.expiry}}</p>
     <button v-if="row.receiptId" class="secondary" :disabled="busy||!!checking||blocked" @click="check(row)">{{checking===row.share.id?'正在核对…':'只读核对'}}</button>
    </li>
   </ol>
   <p v-if="retrying&&!started" class="preview-note">剩余项目会按上次选择的设置重新核对。请确认后提交。</p>
   <fieldset v-if="!started&&!activate" :disabled="blocked||!valid||retrying">
    <label v-if="kind==='pan123'">修改项目<select v-model="edit123" @change="agreed=false"><option v-if="!paid" value="expiry">分享有效期</option><option value="permissions">分享下载权限</option><option v-if="canLimit123" value="limit">下载流量上限</option></select></label>
    <label v-if="!activate&&!isLanzou&&!paid&&edit123==='expiry'">新的有效期<select v-model.number="days" @change="agreed=false"><option :value="-1" disabled>请选择</option><option :value="0">永久</option><option v-for="value in choices" :key="value" :value="value">从修改起 {{value}} 天</option></select></label>
    <template v-if="kind==='pan123'&&edit123==='permissions'">
     <label>新权限<select v-model.number="permission" @change="agreed=false"><option :value="0" disabled>请选择</option><option v-for="row in pan123PermissionOptions" :key="row.value" :value="row.value">{{row.label}}</option></select></label>
     <p v-if="permission===1">关闭两项权限会同时清除所选分享的流量上限。</p>
     <p v-else-if="permission>1">符合条件的下载或播放会消耗你的分享流量包。</p>
     <label v-if="permission>1" class="share-switch"><input v-model="changeLimitWithPermission" type="checkbox" @change="agreed=false"/>同时设置下载流量上限</label>
    </template>
    <template v-if="kind==='pan123'&&(edit123==='limit'||editCombined123)">
     <label>新上限<select v-model="limitMode" @change="agreed=false"><option value="" disabled>请选择</option><option value="limit">设置上限</option><option value="unlimited">取消上限</option></select></label>
     <template v-if="limitMode==='limit'">
      <label>流量数值<input v-model="limitAmount" inputmode="numeric" autocomplete="off" @input="agreed=false"/><small>输入正整数，最多 1024 TiB</small></label>
      <label>单位<select v-model.number="limitUnit" @change="agreed=false"><option :value="2**20">MiB</option><option :value="2**30">GiB</option><option :value="2**40">TiB</option></select></label>
     </template>
     <p v-if="limitMode==='unlimited'">取消上限后，下载仍可能消耗你的分享流量包。</p>
    </template>
    <label v-if="isGuangya||isLanzou">提取码设置
     <select v-model="access" @change="agreed=false">
      <option value="" disabled>请选择</option>
      <option value="set">统一设置新提取码</option>
      <option value="clear">取消提取码，持链接即可访问</option>
     </select>
    </label>
    <label v-if="(isLanzou||isGuangya)&&access==='set'">新提取码
     <input v-model="password" type="text" :maxlength="isLanzou?6:4" autocomplete="off" spellcheck="false" :placeholder="isLanzou?'1 至 6 位字母或数字':'4 位字母或数字'" @input="agreed=false"/>
    </label>
   </fieldset>
   <label v-if="!started" class="share-switch"><input v-model="agreed" type="checkbox" :disabled="blocked||!valid"/>{{activate?'我已核对账号和项目，确认恢复所选原链接的访问':kind==='pan123'&&edit123!=='expiry'?'我已核对账号、所选分享和新的下载设置':(isLanzou||isGuangya)&&access==='clear'?'我确认取消所选分享的提取码，持原链接即可访问':isLanzou?'我已核对账号、所选项目和新提取码':isGuangya?'我已核对账号、所选分享、新期限和新提取码':'我已核对账号、所选分享和新期限'}}</label>
   <p v-if="started" role="status">
    <template v-if="preflight">正在核对原分享<template v-if="isLanzou"> · {{checkedLinks}} / {{attemptSize}}</template>，本轮尚未提交修改。</template>
    <template v-else-if="!busy&&isLanzou&&stopping&&!attemptSubmitted">已停止预检，本轮未提交修改。</template>
    <template v-else>{{busy?'正在处理原分享。':checking?'正在读取原回执。':'本批已结束。'}} 已核对 {{done}} / {{rows.length}} 条。<template v-if="!busy&&rows.some(row=>row.state==='unknown'&&!row.receiptId)">没有可在此核对的回执时，请到本机分享记录查看。</template></template>
   </p>
   <p v-if="error" class="banner error" role="alert">{{error}}</p>
   <BatchOperationReport v-if="started&&!busy" :operation="activate?'批量激活百度原链接':'批量编辑分享设置'" :account-name="accountName" :rows="rows.map(row=>({name:row.share.name,state:row.state}))" :disabled="!!checking||blocked"/>
   <div class="share-history-actions">
    <button v-if="!started" class="primary" :disabled="!canStart" @click="run">{{retrying?'确认处理剩余项目':activate?'确认激活所选分享':'确认修改所选分享'}}</button>
    <button v-if="canRetry" class="secondary" @click="prepareRetry">{{isLanzou?'重试剩余项目':'继续未提交项目'}}（{{retryable.length}}）…</button>
    <button v-if="busy" class="secondary" :disabled="stopping" @click="stopping=true">{{stopping?'等待当前请求结束':'停止后续处理'}}</button>
   </div>
  </section>
 </div>
</template>
<style scoped>
.batch-expiry-dialog{width:min(720px,calc(100vw - 40px));max-height:calc(100vh - 40px);overflow:auto}
.batch-expiry-rows{max-height:40vh;overflow:auto;margin:16px 0;padding-left:28px}
.batch-expiry-rows li{padding:8px;border-bottom:1px solid var(--line);overflow-wrap:anywhere}
.batch-expiry-rows p{margin:6px 0}
</style>
