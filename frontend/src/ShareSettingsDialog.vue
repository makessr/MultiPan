<script setup lang="ts">
import {ref,computed,onMounted,nextTick} from 'vue'
import {trapDialogFocus} from './dialogFocus'
import {shareLink,shareText} from './shareText'
import {pan123PermissionOptions as permissionOptions,pan123TrafficLimit} from './pan123ShareTraffic'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {ManagedShareLink,ShareResult} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{handle:string;name:string;kind?:string;accountName?:string;activate?:boolean;originalExpiry?:string;paid?:boolean}>()
const emit=defineEmits<{close:[attempted:boolean]}>()
const dialog=ref<HTMLElement|null>(null),original=ref<ManagedShareLink|null>(null),busy=ref(true),attempted=ref(false),protectedShare=ref(false),password=ref(''),days=ref(-1),result=ref<ShareResult|null>(null),error=ref(''),status=ref('')
const isBaidu=computed(()=>props.kind==='baidu')
const isPan123=computed(()=>props.kind==='pan123')
const expiryOnly=computed(()=>isBaidu.value||isPan123.value)
const agreed=ref(false)
const edit123=ref<'expiry'|'title'|'traffic'|'permissions'>(props.paid?'title':'expiry'),title=ref('')
const renameTitle=computed(()=>isPan123.value&&edit123.value==='title')
const editTraffic=computed(()=>isPan123.value&&edit123.value==='traffic')
const editPermissions=computed(()=>isPan123.value&&edit123.value==='permissions')
const permissionChoice=ref(0),permissionConsent=ref(false)
const currentPermission=computed(()=>permissionOptions.find(row=>row.value===original.value?.trafficMode123)?.label??'')
const trafficMode=ref<'limit'|'unlimited'|''>(''),trafficAmount=ref(''),trafficUnit=ref(2**30)
const targetLimit=computed(()=>pan123TrafficLimit(trafficMode.value,trafficAmount.value,trafficUnit.value))
const changeLimitWithPermission=ref(false)
const canSetLimitWithPermission=computed(()=>editPermissions.value&&permissionChoice.value>1&&
 (original.value?.trafficCap123!=null||original.value?.trafficMode123===1))
const editCombined123=computed(()=>canSetLimitWithPermission.value&&changeLimitWithPermission.value)
const currentLimit=computed(()=>{
 const bytes=original.value?.trafficCap123
 if(bytes==null)return ''
 if(bytes===0)return '无上限'
 for(const [unit,value] of [['TiB',2**40],['GiB',2**30],['MiB',2**20]] as const){
  if(bytes%value===0)return bytes/value+' '+unit
 }
 return bytes+' B'
})
const newTitle=computed(()=>title.value.trim())
const valid=computed(()=>{
 if(props.activate)return isBaidu.value&&agreed.value
 if(!original.value)return false
 if(props.paid&&edit123.value==='expiry')return false
 if(editPermissions.value){
  if(!currentPermission.value||!permissionOptions.some(row=>row.value===permissionChoice.value))return false
  const permissionChanged=permissionChoice.value!==original.value.trafficMode123
  if(editCombined123.value)return permissionConsent.value&&targetLimit.value!==null&&
   (permissionChanged||targetLimit.value!==original.value.trafficCap123)
  return permissionChanged&&(permissionChoice.value===1||permissionConsent.value)
 }
 if(editTraffic.value)return original.value.trafficCap123!=null&&targetLimit.value!==null&&targetLimit.value!==original.value.trafficCap123
 if(isPan123.value&&renameTitle.value){
  const length=Array.from(newTitle.value).length
  return length>0&&length<=255&&newTitle.value!==original.value.name&&!/[<>|?:/\\\p{Cc}]/u.test(newTitle.value)
 }
 return (isBaidu.value?[0,1,3,7,30]:[0,1,7,30]).includes(days.value)&&(expiryOnly.value||!protectedShare.value||/^[A-Za-z0-9]{4}$/.test(password.value))
})
async function read(){busy.value=true;error.value='';try{original.value=await api.ReadManagedCloudShare(props.handle);title.value=original.value.name;password.value=original.value.password;protectedShare.value=!!password.value}catch(e){error.value=String(e)}finally{busy.value=false}}
async function save(){
 if(busy.value||attempted.value||!valid.value)return
 attempted.value=true;busy.value=true;error.value=''
 try{
  if(props.activate)result.value=await api.ActivateManagedBaiduShare(props.handle)
  else if(editCombined123.value&&targetLimit.value!==null)result.value=await api.SetManagedPan123ShareDownloadSettings(props.handle,permissionChoice.value,targetLimit.value)
  else if(editPermissions.value)result.value=await api.SetManagedPan123ShareTrafficMode(props.handle,permissionChoice.value)
  else if(editTraffic.value&&targetLimit.value!==null)result.value=await api.SetManagedPan123ShareTrafficLimit(props.handle,targetLimit.value)
  else if(isPan123.value&&renameTitle.value)result.value=await api.RenameManagedPan123Share(props.handle,newTitle.value)
  else result.value=await api.UpdateManagedCloudShare(props.handle,!expiryOnly.value&&protectedShare.value?password.value:'',days.value)
 }catch(e){error.value=String(e)+'；请在分享记录核对，不要再次提交'}finally{busy.value=false}
}
async function check(){if(busy.value||!result.value?.canCheck)return;busy.value=true;error.value='';try{result.value=await api.CheckCloudShare(result.value.id)}catch(e){error.value=String(e)}finally{busy.value=false}}
async function copy(){if(busy.value||result.value?.state!=='ready')return;busy.value=true;try{await api.CopyDownloadReport(shareText(result.value));status.value='已复制修改后的原分享信息'}catch(e){error.value=String(e)}finally{busy.value=false}}
onMounted(()=>{void nextTick(()=>dialog.value?.focus());if(props.activate)busy.value=false;else void read()})
</script>
<template>
 <div class="modal-backdrop"><section ref="dialog" tabindex="-1" class="operation-dialog share-dialog" role="dialog" aria-modal="true" aria-labelledby="share-settings-title" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="!busy&&emit('close',attempted)">
  <div class="modal-heading"><h2 id="share-settings-title">{{activate?'激活百度原分享':isBaidu?'修改百度原分享期限':isPan123?'编辑 123 原分享':'编辑光鸭原分享'}}</h2><button class="secondary" :disabled="busy" @click="emit('close',attempted)">关闭</button></div>
  <p class="dialog-location">{{accountName?accountName+' / ':''}}{{name}}</p><p class="preview-note">{{activate?'激活后原链接会恢复访问，继续使用原提取码和有效期。':editPermissions?'保留原主题、链接、提取码和有效期。':editTraffic?'保留原主题、链接、提取码、有效期和下载权限。':renameTitle?'修改主题会保留原链接、提取码、有效期和流量设置。':expiryOnly?'保留原链接及提取码，有效期从本次修改起计算。':'保留原链接，原访问方式随提取码变化，有效期从本次修改起计算。'}}</p>
  <p v-if="isPan123&&edit123==='expiry'" class="preview-note">可用选项受原分享创建时间限制。「永久」按官网规则设至 2099 年。</p>
  <p v-if="original">当前提取码：{{original.password||'无'}} · {{original.expiry}}</p>
  <form v-if="activate" @submit.prevent="save">
   <p>原有效期 · {{originalExpiry}}</p>
   <label v-if="!attempted" class="share-switch"><input v-model="agreed" type="checkbox"/>我已核对账号和项目，确认恢复原链接访问</label>
   <button v-if="!attempted" class="primary" type="submit" :disabled="busy||!valid">确认激活</button>
  </form>
  <form v-else @submit.prevent="save">
   <fieldset :disabled="busy||attempted||!original">
    <label v-if="isPan123">修改项目<select v-model="edit123" @change="permissionConsent=false"><option v-if="!paid" value="expiry">分享有效期</option><option value="title">分享主题</option><option v-if="original?.trafficCap123!=null" value="traffic">下载流量上限</option><option v-if="currentPermission" value="permissions">分享下载权限</option></select></label>
    <label v-if="renameTitle">新主题<input v-model="title" maxlength="510" autocomplete="off"/><small>1 至 255 个字符</small></label>
    <template v-else-if="editTraffic||editPermissions">
     <template v-if="editPermissions">
      <p>当前权限 · {{currentPermission}}</p>
      <label>新权限<select v-model.number="permissionChoice" @change="permissionConsent=false"><option :value="0" disabled>请选择</option><option v-for="row in permissionOptions" :key="row.value" :value="row.value">{{row.label}}</option></select></label>
      <p v-if="permissionChoice===1&&original?.trafficCap123" class="preview-note">关闭两项权限会同时清除这条分享的流量上限。</p>
      <label v-if="canSetLimitWithPermission" class="share-switch"><input v-model="changeLimitWithPermission" type="checkbox" @change="permissionConsent=false"/>同时设置下载流量上限</label>
     </template>
     <template v-if="editTraffic||editCombined123">
      <p v-if="currentLimit">当前下载流量上限 · {{currentLimit}}</p>
      <label>新上限<select v-model="trafficMode" @change="permissionConsent=false"><option value="" disabled>请选择</option><option value="limit">设置上限</option><option value="unlimited">取消上限</option></select></label>
      <template v-if="trafficMode==='limit'">
       <label>流量数值<input v-model="trafficAmount" inputmode="numeric" autocomplete="off" @input="permissionConsent=false"/><small>输入正整数，最多 1024 TiB</small></label>
       <label>单位<select v-model.number="trafficUnit" @change="permissionConsent=false"><option :value="2**20">MiB</option><option :value="2**30">GiB</option><option :value="2**40">TiB</option></select></label>
      </template>
      <p v-if="trafficMode==='unlimited'&&!editPermissions" class="preview-note">取消上限后，下载仍可能消耗你的分享流量包。</p>
     </template>
     <label v-if="editPermissions&&permissionChoice>1" class="share-switch"><input v-model="permissionConsent" type="checkbox"/>我理解符合条件的下载或播放会消耗我的分享流量包</label>
    </template>
    <template v-else>
     <label v-if="!expiryOnly" class="share-switch"><input v-model="protectedShare" type="checkbox"/>使用提取码</label>
     <label v-if="protectedShare&&!expiryOnly">新提取码<input v-model="password" maxlength="4" autocomplete="off" spellcheck="false"/><small>4位字母或数字</small></label>
     <label>重新设定有效期<select v-model.number="days"><option :value="-1" disabled>请选择目标有效期</option><option :value="0">{{isPan123?'永久（至2099年）':'永久'}}</option><option :value="1">从现在起1天</option><option v-if="isBaidu" :value="3">从现在起3天</option><option :value="7">从现在起7天</option><option :value="30">从现在起30天</option></select></label>
    </template>
   </fieldset>
   <button v-if="!attempted" type="submit" class="primary" :disabled="busy||!valid">{{editCombined123?'保存下载设置':editPermissions?'保存分享下载权限':editTraffic?'保存下载流量上限':renameTitle?'保存新主题':'保存原分享设置'}}</button>
  </form>
  <p v-if="busy" role="status">正在处理，请勿重复操作。</p><p v-if="error" class="banner error" role="alert">{{error}}</p><p v-if="status" role="status">{{status}}</p>
  <section v-if="result" class="share-result"><strong>{{result.state==='ready'?'设置已核对':result.state==='failed'?'未提交或原记录已失效':'结果待确认'}}</strong><p>{{result.message}}</p><template v-if="result.state==='ready'"><label>原分享链接<input :value="shareLink(result)" readonly/></label><p>实际提取码：{{result.password||'无'}}</p><button class="primary" :disabled="busy" @click="copy">复制更新后的分享信息</button></template><button v-if="result.canCheck" class="secondary" :disabled="busy" @click="check">检查原设置结果</button></section>
  <p class="preview-note">修改回执加密保存在本机，重启后在分享记录查看。结果待确认时可只读核对原回执。旧本机分享回执会停止提供过时的码和链接。</p>
 </section></div>
</template>
