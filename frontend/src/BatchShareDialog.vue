<script setup lang="ts">
import {ref,computed,onMounted,onUnmounted,nextTick} from 'vue'
import {trapDialogFocus} from './dialogFocus'
import {newShareCode,shareText,shareLink} from './shareText'
import * as api from '../bindings/multipan/desktopservice'
import type {CloudEntry,ShareInput,ShareResult} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accountId:string;parentId:string;kind:string;entries:CloudEntry[];initialSelection:CloudEntry[]}>()
const emit=defineEmits<{close:[]}>()
const key=(e:CloudEntry)=>(e.folder?'d:':'f:')+e.id
const items=props.entries.map(e=>({...e}))
const selected=ref([...new Set(props.initialSelection.map(key))].filter(k=>items.some(e=>key(e)===k))),protectedShare=ref(props.kind==='baidu'),password=ref(newShareCode()),days=ref(0),autoFill=ref(true)
const canCombine=['baidu','pan123','guangya','quark'].includes(props.kind)
const combine=ref(canCombine&&props.kind!=='quark')
type Row={entry:CloudEntry;input:ShareInput;state:'queued'|'running'|'done'|'uncertain';result:ShareResult|null;error:string}
const rows=ref<Row[]>([]),busy=ref(false),attempted=ref(false),stop=ref(false),error=ref(''),status=ref(''),dialog=ref<HTMLElement|null>(null)
const preflighting=ref(false)
const processing=computed(()=>preflighting.value||rows.value.some(r=>r.state==='running'))
let disposed=false
const supported=['lanzou','quark','pan123','baidu','guangya'].includes(props.kind)
const valid=computed(()=>supported&&selected.value.length>0&&selected.value.length<=50&&(props.kind==='lanzou'||!protectedShare.value&&props.kind!=='baidu'||/^[A-Za-z0-9]{4}$/.test(password.value)))
const unresolved=computed(()=>rows.value.some(r=>r.state==='uncertain'||r.result?.state==='unknown'||r.result?.state==='working'))
const remaining=computed(()=>rows.value.filter(r=>r.state==='queued').length)
const ready=computed(()=>rows.value.filter(r=>r.result?.state==='ready'))
function toggle(e:CloudEntry,checked:boolean){if(attempted.value||busy.value)return;const k=key(e);if(checked){if(selected.value.length>=50){error.value='每批最多50项';return}selected.value=[...new Set([...selected.value,k])]}else selected.value=selected.value.filter(v=>v!==k)}
function selectAll(){if(attempted.value||busy.value)return;selected.value=items.slice(0,50).map(key);error.value=items.length>50?'当前目录超过50项，已选前50项。':''}
async function run(){
 if(busy.value||unresolved.value||disposed)return
 const pending=rows.value.filter(row=>row.state==='queued')
 if(!pending.length)return
 busy.value=true;stop.value=false;error.value='';status.value=''
 try{
  if(props.kind!=='lanzou'){
   preflighting.value=true
   try{await api.ValidateShareCreationBatch(pending.map(row=>row.input))}
   catch(e){if(!disposed)error.value='本轮预检未通过，未创建分享。'+String(e);return}
   finally{preflighting.value=false}
  }
  for(const row of pending){if(stop.value||disposed)break
   row.state='running'
   try{row.result=await api.ShareBatchCloudEntry(row.input);row.state=['ready','failed'].includes(row.result.state)?'done':'uncertain'}
   catch(e){row.state='uncertain';row.error=String(e)}
   if(row.state==='uncertain'){stop.value=true;break}
 }}finally{busy.value=false}
}
async function submit(){
 if(busy.value||attempted.value||!valid.value||disposed)return
 attempted.value=true
 rows.value=items.filter(e=>selected.value.includes(key(e))).map(entry=>({entry,input:{accountId:props.accountId,parentId:props.parentId,entryId:entry.id,name:entry.name,folder:entry.folder,action:props.kind==='lanzou'?'get':'create',password:props.kind==='lanzou'?'':props.kind==='baidu'||protectedShare.value?password.value:'',days:props.kind==='lanzou'?0:days.value},state:'queued',result:null,error:''}))
 if(combine.value&&canCombine&&rows.value.length>1){const first=rows.value[0]!;const count=rows.value.length;first.input.targets=rows.value.map(r=>({id:r.entry.id,name:r.entry.name,folder:r.entry.folder}));first.entry={...first.entry,name:first.entry.name+' 等'+count+'项（合并分享）'};rows.value=[first]}
 await run()
}
async function check(row:Row){if(busy.value||disposed||!row.result?.id||!row.result.canCheck)return;busy.value=true;row.error='';try{row.result=await api.CheckCloudShare(row.result.id);row.state=['ready','failed'].includes(row.result.state)?'done':'uncertain'}catch(e){row.error=String(e)}finally{busy.value=false}}
async function copy(linksOnly=false){if(busy.value||disposed)return;const text=ready.value.map(r=>shareText(r.result!,linksOnly,autoFill.value)).filter(Boolean).join('\n\n');if(!text)return;busy.value=true;try{await api.CopyDownloadReport(text);status.value='已复制已取得的分享；待确认和未开始项未包含。'}catch(e){error.value=String(e)}finally{busy.value=false}}
function resultLink(row:Row){return row.result?shareLink(row.result,autoFill.value):''}
function rowLabel(row:Row){return row.state==='queued'?'未开始':row.state==='running'?'处理中':row.state==='uncertain'?'结果待确认':row.result?.state==='ready'?'已取得分享':'未取得分享'}
onMounted(()=>nextTick(()=>dialog.value?.focus()))
onUnmounted(()=>{disposed=true;stop.value=true})
</script>
<template>
 <div class="modal-backdrop"><section ref="dialog" tabindex="-1" class="operation-dialog share-dialog batch-share-dialog" role="dialog" aria-modal="true" aria-labelledby="batch-share-title" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="!busy&&emit('close')">
  <div class="modal-heading"><h2 id="batch-share-title">{{kind==='lanzou'?'读取蓝奏现有分享':'批量分享'}}</h2><button class="secondary" :disabled="busy" @click="emit('close')">关闭</button></div>
  <p class="preview-note">{{kind==='lanzou'?'读取所选项目已有的链接和提取码。':combine?'所选项目合成一个分享链接。':'每个项目分别生成一条链接。'}}文件夹会包含其中内容，每批最多50项。</p>
  <template v-if="!attempted"><div class="batch-share-selectionbar"><button class="secondary" @click="selectAll">选择当前显示项目（最多50项）</button><button class="text-button" @click="selected=[]">清空</button><strong class="batch-share-count">已选 {{selected.length}} 项</strong></div>
   <ul class="batch-share-items"><li v-for="entry in items" :key="key(entry)"><label><input type="checkbox" :checked="selected.includes(key(entry))" @change="toggle(entry,($event.target as HTMLInputElement).checked)"/><span>{{entry.folder?'文件夹':'文件'}} · {{entry.name}}</span></label></li></ul>
   <p v-if="kind==='lanzou'" class="preview-note">蓝奏云只读取现有分享，不批量修改提取码或有效期。</p>
   <fieldset v-else :disabled="busy"><label v-if="canCombine">分享方式<select v-model="combine"><option :value="true">合并分享：生成一个链接</option><option :value="false">分别分享：每项一个链接</option></select></label><label class="share-switch"><input v-model="protectedShare" type="checkbox" :disabled="kind==='baidu'"/>使用提取码{{kind==='baidu'?'（必填）':''}}</label><label v-if="protectedShare">{{combine?'提取码':'统一提取码'}}<input v-model="password" maxlength="4" autocomplete="off" spellcheck="false"/><small>4位字母或数字</small></label><label>有效期<select v-model.number="days"><option :value="0">{{kind==='pan123'?'永久（至2099年）':'永久'}}</option><option :value="1">1天</option><option :value="7">7天</option><option v-if="kind!=='baidu'" :value="30">30天</option></select></label></fieldset>
   <div class="batch-share-footer"><strong>已选 {{selected.length}} 项</strong><button class="primary" :disabled="!valid||busy" @click="submit">{{kind==='lanzou'?'读取所选分享':combine?'创建一个分享链接':'分别创建分享链接'}}</button></div>
  </template>
  <template v-else><div class="batch-share-progress"><strong>已取得 {{ready.length}} 个链接 · 未开始 {{remaining}} 组</strong><button v-if="busy&&processing" class="secondary" :disabled="stop" @click="stop=true">{{stop?'正在停止…':'停止后续项目'}}</button><button v-else-if="remaining" class="primary" :disabled="busy||unresolved" @click="run">继续未开始项目</button></div>
   <p v-if="preflighting" role="status">正在核对本轮所选项目，尚未创建链接。</p>
   <p v-if="unresolved" class="banner error">存在待确认结果，已停止后续项目。请检查原操作结果或到分享记录页核对，不要重新创建。</p>
   <ul class="batch-share-items batch-share-results"><li v-for="row in rows" :key="key(row.entry)"><strong>{{row.entry.name}}</strong><span>{{rowLabel(row)}}</span><p v-if="row.result?.message">{{row.result.message}}</p><div v-if="row.result?.state==='ready'&&resultLink(row)" class="batch-share-link"><label>分享链接<input :value="resultLink(row)" readonly :aria-label="row.entry.name+' 分享链接'" @focus="($event.target as HTMLInputElement).select()"/></label><p>提取码：{{row.result.password||'无'}}<template v-if="row.result.expiry"> · {{row.result.expiry}}</template></p></div><p v-if="row.error" role="alert">{{row.error}}</p><button v-if="row.result?.canCheck" class="secondary" :disabled="busy" @click="check(row)">检查原操作结果</button></li></ul>
   <label v-if="kind==='baidu'||kind==='guangya'" class="share-switch"><input v-model="autoFill" type="checkbox" :disabled="busy"/>复制链接自动填充提取码</label><div class="batch-share-footer"><button class="primary" :disabled="busy||!ready.length" @click="copy(false)">复制分享信息</button><button class="secondary" :disabled="busy||!ready.length" @click="copy(true)">复制链接</button></div>
  </template>
  <p v-if="error" class="banner error" role="alert">{{error}}</p><p v-if="status" role="status">{{status}}</p><p class="preview-note">已提交项目的回执加密保存在本机，重启后在分享记录中查看；未开始项目不会自动恢复。停止后续项目不会撤销已提交操作。关闭窗口后本批选择不保留。</p>
 </section></div>
</template>
<style scoped>
.batch-share-dialog{width:min(720px,calc(100vw - 40px))}.batch-share-items{max-height:32vh;overflow:auto;list-style:none;padding:0;margin:14px 0;border:1px solid #e4e8ee;border-radius:8px}.batch-share-items li{padding:10px 12px;border-bottom:1px solid #eef0f4;overflow-wrap:anywhere}.batch-share-items li:last-child{border-bottom:0}.batch-share-items label{display:flex;align-items:center;gap:10px;margin:0}.batch-share-items input[type=checkbox]{width:15px;flex:none}.batch-share-results li{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:8px 16px}.batch-share-results li>span{color:#657388;white-space:nowrap}.batch-share-results li>p{grid-column:1/-1}.batch-share-results li>button{justify-self:start;grid-column:1/-1}.batch-share-results p{font-size:12px;color:#657388;margin:8px 0}.batch-share-results strong{min-width:0}.batch-share-selectionbar,.batch-share-progress{display:flex;align-items:center;gap:12px;flex-wrap:wrap}.batch-share-count{margin-left:auto;white-space:nowrap}.batch-share-footer{display:flex;align-items:center;justify-content:flex-end;gap:10px;flex-wrap:wrap;padding-top:16px;margin-top:12px;border-top:1px solid #e4e8ee}.batch-share-footer>strong{margin-right:auto}.batch-share-dialog{max-height:calc(100vh - 40px);overflow-y:auto}.batch-share-dialog fieldset{margin-top:16px}.batch-share-progress{justify-content:space-between}.batch-share-results{max-height:36vh}
.batch-share-dialog fieldset{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:10px 18px}.batch-share-dialog fieldset>label{margin:0}.batch-share-dialog fieldset>label:first-child,.batch-share-dialog fieldset>.share-switch{grid-column:1/-1}.batch-share-items{max-height:20vh}.batch-share-results{max-height:36vh}.batch-share-footer{position:sticky;bottom:-24px;background:#fff;z-index:1;padding:14px 0}.batch-share-dialog>.preview-note:last-child{margin-top:10px}@media(max-width:560px){.batch-share-dialog fieldset{grid-template-columns:1fr}.batch-share-count{margin-left:0}}
.batch-share-link{grid-column:1/-1;min-width:0}.batch-share-items .batch-share-link label{display:block}.batch-share-link input{width:100%;min-width:0;box-sizing:border-box;margin-top:6px;font-family:Consolas,monospace;font-size:12px}.batch-share-link p{margin:8px 0}
.batch-share-dialog fieldset>label:not(.share-switch){display:flex;flex-direction:column;align-items:stretch;justify-content:flex-start;align-self:start;gap:7px}.batch-share-dialog fieldset input:not([type=checkbox]),.batch-share-dialog fieldset select{height:38px;min-height:38px;margin:0;padding:8px 11px;line-height:20px}.batch-share-dialog fieldset small{margin:0;line-height:16px}
</style>
