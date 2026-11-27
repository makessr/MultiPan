<script setup lang="ts">
import {ref,computed,onMounted,nextTick} from 'vue'
import {trapDialogFocus} from './dialogFocus'
import {shareText,shareLink,newShareCode} from './shareText'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {CloudEntry,ShareInput,ShareRecreation,ShareResult} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accountId:string;parentId:string;kind:string;entry:CloudEntry;editPassword?:boolean;accountName?:string;sourcePath?:string;showReceiptLink?:boolean;recreation?:ShareRecreation;group?:CloudEntry[]}>()
const emit=defineEmits<{close:[];receipt:[id:string]}>()
const modify=ref(props.kind==='lanzou'&&!!props.editPassword),protectedShare=ref(props.kind==='baidu'||!!props.recreation?.protected),password=ref(newShareCode()),days=ref(props.recreation?.days??0),autoFill=ref(true)
const access=ref<'set'|'clear'>('set'),confirmPublic=ref(false)
const originalPrice=props.recreation?.paidPriceFen123??0
const paid=ref(originalPrice>0),paidPrice=ref(originalPrice>0?(originalPrice/100).toFixed(2):''),paidDescription=ref(''),confirmPaid=ref(false)
const canChoosePaid=computed(()=>props.kind==='pan123'&&!props.group?.length)
const isPaid=computed(()=>canChoosePaid.value&&paid.value)
const paidPriceFen=computed(()=>{
 const match=/^(\d{1,4})(?:\.(\d{1,2}))?$/.exec(paidPrice.value.trim())
 return match?Number(match[1])*100+Number((match[2]??'').padEnd(2,'0')):0
})
const clearing=computed(()=>props.kind==='lanzou'&&modify.value&&access.value==='clear')
const busy=ref(false),attempted=ref(false),result=ref<ShareResult|null>(null),error=ref(''),copyStatus=ref(''),dialog=ref<HTMLElement|null>(null)
const title=computed(()=>props.kind==='lanzou'?'蓝奏云分享':props.kind==='pan123'?'123 云盘分享':props.kind==='baidu'?'百度网盘分享':props.kind==='guangya'?'光鸭云盘分享':'夸克网盘分享')
const actionLabel=computed(()=>isPaid.value?'提交付费分享审核':props.kind!=='lanzou'?(props.recreation?'确认重新分享':'创建分享'):clearing.value?'确认取消提取码':modify.value?'设置提取码并核对':'读取现有分享链接')
const valid=computed(()=>props.kind==='lanzou'
 ?!modify.value||(clearing.value?confirmPublic.value:/^[A-Za-z0-9]{1,6}$/.test(password.value))
 :isPaid.value?confirmPaid.value&&paidPriceFen.value>=100&&paidPriceFen.value<=999900&&[...paidDescription.value].length<=200
 :props.kind!=='baidu'&&!protectedShare.value||/^[A-Za-z0-9]{4}$/.test(password.value))
const resultLink=computed(()=>result.value?shareLink(result.value,autoFill.value):'')
const canCheck=computed(()=>result.value?.canCheck||result.value?.state==='working')
function openReceipt(){if(props.showReceiptLink&&!busy.value&&result.value?.id)emit('receipt',result.value.id)}
async function submit(){
 if(busy.value||attempted.value||!valid.value)return
 busy.value=true;attempted.value=true;error.value='';copyStatus.value=''
 const submittedPassword=isPaid.value?'':props.kind==='lanzou'?(modify.value&&!clearing.value?password.value:''):
  (props.kind==='baidu'||protectedShare.value?password.value:'')
 const input:ShareInput={
  accountId:props.accountId,parentId:props.parentId,entryId:props.entry.id,name:props.entry.name,folder:props.entry.folder,
  action:props.kind!=='lanzou'?'create':modify.value?'password':'get',password:submittedPassword,
  days:props.kind==='lanzou'||isPaid.value?0:days.value,
  ...(clearing.value?{clearPassword:true}:{}),
  ...(isPaid.value?{paidPriceFen123:paidPriceFen.value,paidDescription123:paidDescription.value.trim()}:{}),
 }
 if(props.group?.length)input.targets=props.group.map(entry=>({id:entry.id,name:entry.name,folder:entry.folder}))
 try{
  const original=props.recreation
  result.value=original
   ?await api.RecreateCloudShare(original.receiptId,original.attemptId,input)
   :await api.ShareCloudEntry(input)
 }
 catch(e){error.value=String(e)}
 finally{busy.value=false}
}
async function check(){if(busy.value||!result.value?.id||!canCheck.value)return;busy.value=true;error.value='';copyStatus.value='';try{result.value=await api.CheckCloudShare(result.value.id)}catch(e){error.value=String(e)}finally{busy.value=false}}
async function copy(linkOnly=false){if(busy.value||!result.value)return;const text=shareText(result.value,linkOnly,autoFill.value);if(!text)return;busy.value=true;copyStatus.value='';try{await api.CopyDownloadReport(text);copyStatus.value=linkOnly?'分享链接已复制':'链接、提取码及有效期说明已复制'}catch(e){copyStatus.value=String(e)}finally{busy.value=false}}
onMounted(()=>nextTick(()=>dialog.value?.focus()))
</script>
<template>
 <div class="modal-backdrop"><section ref="dialog" tabindex="-1" class="operation-dialog share-dialog" role="dialog" aria-modal="true" aria-labelledby="share-dialog-title" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="!busy&&emit('close')">
  <div class="modal-heading"><h2 id="share-dialog-title">{{recreation?'重新分享 · '+title:title}}</h2><button class="secondary" :disabled="busy" @click="emit('close')">关闭</button></div>
  <p v-if="accountName" class="dialog-location">{{accountName}}<template v-if="sourcePath"> / {{sourcePath}}</template></p>
  <template v-if="group?.length">
   <p class="dialog-location">{{group.length}} 个原项目，共用一个链接</p>
   <ul class="share-recreation-items"><li v-for="item in group" :key="item.id">{{item.folder?'文件夹':'文件'}} · {{item.name}}</li></ul>
  </template>
  <p v-else class="dialog-location">{{entry.folder?'文件夹':'文件'}}：{{entry.name}}</p>
  <p v-if="recreation" class="preview-note">确认后将提交分享设置。原链接如需撤销，可在云端分享中单独处理。</p>
  <p v-if="kind==='lanzou'" class="preview-note">修改提取码会影响项目的已有链接，有效期沿用网盘设置。</p>
  <p v-else class="preview-note">点击创建后才向网盘提交分享。收到链接的人可能访问文件内容；分享文件夹会包含其中的内容，请确认范围。</p>
  <label v-if="kind==='baidu'||kind==='guangya'&&protectedShare" class="share-switch"><input v-model="autoFill" type="checkbox" :disabled="busy"/>链接自动填充提取码</label>
  <form @submit.prevent="submit"><fieldset :disabled="busy||attempted">
   <label v-if="canChoosePaid">分享类型<select v-model="paid" @change="confirmPaid=false"><option :value="false">免费分享</option><option :value="true">付费分享</option></select></label>
   <template v-if="isPaid">
    <label>付费金额（元）<input v-model="paidPrice" inputmode="decimal" placeholder="1 至 9999，最多两位小数" @input="confirmPaid=false"/></label>
    <label>资源描述<textarea v-model="paidDescription" maxlength="200" rows="3" @input="confirmPaid=false"/></label>
    <label class="share-switch"><input v-model="confirmPaid" type="checkbox"/>我已核对分享范围和金额，确认提交 123 云盘审核</label>
   </template>
   <label v-if="kind==='lanzou'" class="share-switch"><input v-model="modify" type="checkbox" @change="confirmPublic=false"/>编辑这个项目的提取码</label><label v-else-if="!isPaid" class="share-switch"><input v-model="protectedShare" type="checkbox" :disabled="kind==='baidu'"/>使用提取码{{kind==='baidu'?'（百度本版必填）':''}}</label>
   <label v-if="kind==='lanzou'&&modify">提取码设置<select v-model="access" @change="confirmPublic=false"><option value="set">设置新提取码</option><option value="clear">取消提取码</option></select></label>
   <label v-if="clearing" class="share-switch"><input v-model="confirmPublic" type="checkbox"/>我确认取消保护，访问原链接将无需提取码</label>
   <label v-if="kind==='lanzou'?modify&&!clearing:!isPaid&&protectedShare">提取码<input v-model="password" :maxlength="kind!=='lanzou'?4:6" autocomplete="off" spellcheck="false" :pattern="kind!=='lanzou'?'[A-Za-z0-9]{4}':'[A-Za-z0-9]{1,6}'" required/><small>{{kind!=='lanzou'?'4位字母或数字':'1–6位字母或数字'}}</small></label>
   <label v-if="kind!=='lanzou'&&!isPaid">有效期<select v-model.number="days"><option :value="1">1天</option><option :value="7">7天</option><option v-if="kind!=='baidu'" :value="30">30天</option><option :value="0">{{kind==='pan123'?'永久（至2099年）':'永久'}}</option></select></label>
  </fieldset><button v-if="!attempted" type="submit" class="primary" :disabled="busy||!valid">{{actionLabel}}</button></form>
  <p v-if="busy" role="status">正在处理，请勿重复提交。创建或修改请求一旦提交，关闭窗口或退出程序不会撤销云端操作。</p>
  <section v-if="result" class="share-result"><strong>{{result.state==='ready'?'分享结果已取得':result.state==='working'?'原操作仍在处理中':result.state==='unknown'?'结果待确认':'未取得可用分享'}}</strong><p>{{result.message}}</p><template v-if="result.state==='ready'"><label>分享链接<input :value="resultLink" readonly aria-label="分享链接"/></label><p>提取码：{{result.password||'无'}} · {{result.expiry||(kind!=='lanzou'?'申请有效期：'+(result.days===0?'永久':result.days+'天'):'沿用网盘现有有效期')}}</p><div class="saved-location-actions"><button class="primary" :disabled="busy" @click="copy(false)">复制完整分享信息</button><button class="secondary" :disabled="busy" @click="copy(true)">只复制链接</button></div></template><button v-if="canCheck" class="secondary" :disabled="busy" @click="check">检查原操作结果</button></section>
  <button v-if="showReceiptLink&&result?.id" class="secondary" :disabled="busy" @click="openReceipt">查看本机回执</button>
  <p v-if="error" class="banner error" role="alert">{{error}}</p><p v-if="copyStatus" role="status">{{copyStatus}}</p><p class="preview-note">本窗口不会发送链接给他人。未确认时先检查原操作或到官网核对，不会自动重新创建分享。分享回执加密保存在本机，重启后可到分享记录页检查原结果；缺少原标识时请到官网核对，不重复创建。</p>
 </section></div>
</template>

<style scoped>
.share-recreation-items{max-height:11rem;overflow:auto;overflow-wrap:anywhere;padding-inline-start:1.5rem}
</style>
