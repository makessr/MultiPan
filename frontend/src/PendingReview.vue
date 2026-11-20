<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {Account,PendingReviewRow} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accounts:Account[];blocked?:boolean;refreshKey?:string}>()
const emit=defineEmits<{mutation:[active:boolean];open:[kind:string,accountId:string,receiptId:string]}>()
const rows=ref<PendingReviewRow[]>([]),total=ref(0),limited=ref(false),query=ref(''),kind=ref(''),account=ref(''),eligibleOnly=ref(false),page=ref(1),selection=ref<string[]>([])
const busy=ref(false),checking=ref(false),stopRequested=ref(false),error=ref(''),loadedAt=ref('')
type ReviewResult=Pick<PendingReviewRow,'key'|'name'|'accountId'|'kind'>&{state:string;message:string}
const results=ref<ReviewResult[]>([]),recentResults=ref<ReviewResult[]>([])
const previousResults=computed(()=>{
 const current=new Set(results.value.filter(result=>result.state!=='skipped').map(result=>result.key))
 return recentResults.value.filter(result=>!current.has(result.key))
})
function retainResults(){
 const completed=results.value.filter(result=>result.state!=='skipped')
 const keys=new Set(completed.map(result=>result.key))
 recentResults.value=[...completed,...recentResults.value.filter(result=>!keys.has(result.key))].slice(0,100)
}
const categories:Record<string,string>={session_renewal:'登录令牌更新',share:'创建或读取分享',share_settings:'分享设置',share_revoke:'撤销分享',copy:'盘内复制',trash:'移入回收站',restore:'回收站还原',purge:'永久删除',relay:'网盘中转'}
let disposed=false,generation=0,reload=false
const locked=computed(()=>busy.value||checking.value)
function accountLabel(id:string){const a=props.accounts.find(a=>a.id===id);if(!a)return '原账号已移除';const same=props.accounts.filter(b=>b.kind===a.kind);const name=({lanzou:'蓝奏云',quark:'夸克',pan123:'123',baidu:'百度',guangya:'光鸭',ctfile:'诚通OpenAPI',ctdav:'诚通WebDAV',webdav:'WebDAV'} as Record<string,string>)[a.kind]??'网盘';return name+(same.length>1?' '+(same.findIndex(b=>b.id===id)+1):'')}
const accountIDs=computed(()=>[...new Set(rows.value.map(r=>r.accountId))])
const filtered=computed(()=>rows.value.filter(r=>(!kind.value||r.kind===kind.value)&&(!account.value||r.accountId===account.value)&&(!eligibleOnly.value||r.canCheck)&&(!query.value.trim()||(r.name+' '+accountLabel(r.accountId)).toLocaleLowerCase().includes(query.value.trim().toLocaleLowerCase()))))
const pages=computed(()=>Math.max(1,Math.ceil(filtered.value.length/50)))
const visible=computed(()=>filtered.value.slice((page.value-1)*50,page.value*50))
const chosen=computed(()=>visible.value.filter(r=>r.canCheck&&selection.value.includes(r.key)))
function stateLabel(value:string){return ({unknown:'待核对',working:'处理中',ready:'回执可用',confirmed:'结果已确认',verified:'内容已核对',failed:'原操作已结束',error:'核对未完成',skipped:'未处理'} as Record<string,string>)[value]??value}
function toggle(key:string,on:boolean){if(locked.value||props.blocked)return;if(!on)selection.value=selection.value.filter(x=>x!==key);else if(selection.value.length<20&&visible.value.some(r=>r.key===key&&r.canCheck)&&!selection.value.includes(key))selection.value.push(key)}
function selectPage(){if(!locked.value&&!props.blocked)selection.value=visible.value.filter(r=>r.canCheck).slice(0,20).map(r=>r.key)}
async function load(){
 if(disposed||locked.value||props.blocked)return
 const stamp=++generation;busy.value=true;error.value='';emit('mutation',true)
 try{const s=await api.PendingReview();if(disposed||stamp!==generation)return;rows.value=s.rows??[];total.value=s.total;limited.value=s.limited;selection.value=[];page.value=Math.min(page.value,pages.value);loadedAt.value=new Date().toLocaleString()}
 catch(e){if(!disposed&&stamp===generation)error.value='读取本机回执失败：'+String(e)}
 finally{busy.value=false;emit('mutation',false);if(reload&&!disposed){reload=false;await load()}}
}
async function run(targets:PendingReviewRow[]=chosen.value){
 if(disposed||locked.value||props.blocked||!targets.length||targets.length>20||targets.some(r=>!r.canCheck)||new Set(targets.map(r=>r.key)).size!==targets.length)return
 retainResults()
 const stamp=generation,inputs=targets.map(r=>({...r}));checking.value=true;stopRequested.value=false;results.value=[];error.value='';emit('mutation',true)
 try{for(const row of inputs){
  if(disposed||stamp!==generation)return
  const identity={key:row.key,name:row.name,accountId:row.accountId,kind:row.kind}
  if(stopRequested.value||props.blocked){results.value.push({...identity,state:'skipped',message:'未提交本次核对请求'});continue}
  try{const result=await api.CheckPendingReview(row.kind,row.id);if(disposed||stamp!==generation)return;if(!result||!result.state)throw Error('缺少核对结果');results.value.push({...identity,state:result.state,message:result.message})}
  catch{if(disposed||stamp!==generation)return;results.value.push({...identity,state:'error',message:hasOriginalModule(row.kind)?'本次未能完成核对，原回执保留；请到原模块查看详情':'本次未能完成核对，原回执保留；请在网盘官网核查'});stopRequested.value=true}
 }}finally{checking.value=false;emit('mutation',false);if(!disposed){reload=false;selection.value=[];await load()}}
}
function hasOriginalModule(kind:string){return kind!=='restore'&&kind!=='purge'}
function open(row:PendingReviewRow){
 if(disposed||locked.value||props.blocked||!hasOriginalModule(row.kind))return
 if(!props.accounts.some(a=>a.id===row.accountId)&&row.kind!=='share'&&row.kind!=='share_settings'){
  error.value='原账号已移除，请到原账号核对记录'
  return
 }
 emit('open',row.kind,row.accountId,row.id)
}
watch([query,kind,account,eligibleOnly],()=>{page.value=1;selection.value=[]})
watch(page,()=>selection.value=[])
watch(()=>JSON.stringify([props.refreshKey,props.accounts.map(a=>[a.id,a.kind,a.endpoint,a.username,a.folder,a.userAgent])]),()=>{generation++;stopRequested.value=true;rows.value=[];selection.value=[];results.value=[];recentResults.value=[];total.value=0;limited.value=false;loadedAt.value='';if(locked.value)reload=true;else void load()})
onMounted(()=>{void load()})
onUnmounted(()=>{disposed=true;generation++;stopRequested.value=true;emit('mutation',false)})
</script>
<template>
<section class="pending-review">
 <div class="heading"><h1>待核对中心</h1><button class="secondary" :disabled="locked||blocked" @click="load">刷新本机回执</button></div>
 <details class="operation-details"><summary>核对范围</summary><p>刷新读取本机回执。核对连接网盘读取原结果，每次最多 20 项，停止时保留已有结果。中转和登录令牌更新需回到原页面核对。</p></details>
 <p v-if="error" class="banner error" role="alert">{{error}}</p><p v-if="limited" class="banner notice" role="status">共有 {{total}} 条待核对或处理中记录，此页仅展示最新2000条；更早记录请到原模块查看。筛选仅作用于已展示记录。</p>
 <div class="review-filters"><label>操作<select v-model="kind" :disabled="locked||blocked"><option value="">全部操作</option><option v-for="(name,id) in categories" :key="id" :value="id">{{name}}</option></select></label><label>账号<select v-model="account" :disabled="locked||blocked"><option value="">全部账号</option><option v-for="id in accountIDs" :key="id" :value="id">{{accountLabel(id)}}</option></select></label><label>查找<input v-model="query" type="search" placeholder="项目名称或网盘" :disabled="locked||blocked"/></label><label><input v-model="eligibleOnly" type="checkbox" :disabled="locked||blocked"/>仅显示此页可核对项</label></div>
 <p class="preview-note">本机共有 {{total}} 条待核对或处理中记录，当前筛选 {{filtered.length}} 条。{{loadedAt?'本次读取：'+loadedAt:''}}</p>
 <div class="review-actions"><button class="secondary" :disabled="locked||blocked" @click="selectPage">选择本页可核对项（最多20项）</button><button class="secondary" :disabled="locked||blocked" @click="selection=[]">取消选择</button><button class="primary" :disabled="locked||blocked||!chosen.length" @click="run()">只读核对所选（{{chosen.length}}）</button><button v-if="checking" class="secondary" :disabled="stopRequested" @click="stopRequested=true">停止后续核对</button><span v-if="checking" role="status">{{stopRequested?'等待当前项返回…':'正在逐项只读核对…'}}</span></div>
 <section v-if="results.length" class="share-history-card" aria-live="polite"><strong>本次核对结果</strong><ul><li v-for="r in results" :key="r.key"><strong>{{r.name}} · {{stateLabel(r.state)}}</strong><small>{{accountLabel(r.accountId)}} · {{categories[r.kind]||r.kind}}</small><p>{{r.message}}</p></li></ul></section>
 <details v-if="previousResults.length" class="operation-details previous-reviews">
  <summary>之前的核对结果（{{previousResults.length}}）</summary>
  <ul><li v-for="r in previousResults" :key="r.key"><strong>{{r.name}} · {{stateLabel(r.state)}}</strong><small>{{accountLabel(r.accountId)}} · {{categories[r.kind]||r.kind}}</small><p>{{r.message}}</p></li></ul>
 </details>
 <p v-if="!visible.length&&!busy&&!error" class="recycle-note">当前范围没有待核对记录</p>
 <article v-for="row in visible" :key="row.key" class="share-history-card"><div class="share-history-card-heading"><label><input type="checkbox" :checked="selection.includes(row.key)" :disabled="locked||blocked||!row.canCheck||selection.length>=20&&!selection.includes(row.key)" @change="toggle(row.key,($event.target as HTMLInputElement).checked)"/><span>{{row.name}}</span></label><span class="review-state">{{stateLabel(row.state)}}</span></div><p>{{categories[row.kind]||row.kind}} · {{accountLabel(row.accountId)}} · {{new Date(row.created).toLocaleString()}}</p><p v-if="!row.canCheck" class="muted">{{hasOriginalModule(row.kind)?'此记录目前需在原模块查看状态或原账号官网核查。':'请在原账号的网盘官网核查此记录。'}}</p><div class="review-actions"><button v-if="row.canCheck" class="secondary" :disabled="locked||blocked" @click="run([row])">只读核对这一项</button><button v-if="hasOriginalModule(row.kind)" class="secondary" :disabled="locked||blocked" @click="open(row)">前往原模块</button></div></article>
 <footer class="review-actions"><button class="secondary" :disabled="locked||blocked||page<=1" @click="page--">上一页</button><span>第 {{page}} / {{pages}} 页</span><button class="secondary" :disabled="locked||blocked||page>=pages" @click="page++">下一页</button></footer>
</section>
</template>
<style scoped>
.review-filters,.review-actions{display:flex;gap:12px;align-items:center;flex-wrap:wrap;margin:16px 0}.review-filters label{display:flex;gap:8px;align-items:center}.review-filters input[type=search]{min-width:180px}.pending-review .share-history-card-heading label span{overflow-wrap:anywhere}.review-state{white-space:nowrap}.pending-review select{min-height:36px}.pending-review li{margin:12px 0}.pending-review p{overflow-wrap:anywhere}
.pending-review li small{display:block;color:var(--muted)}
.previous-reviews ul{padding-left:20px}
</style>
