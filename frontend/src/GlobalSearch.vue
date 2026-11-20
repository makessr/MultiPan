<script setup lang="ts">
import {computed,onUnmounted,ref,watch} from 'vue'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {Account,CloudSearchStatus,CloudSearchResult} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accounts:Account[];initialAccountId?:string;fixedAccountId?:string;refreshKey?:string;blocked?:boolean;listLanzouCandidates?:boolean}>()
const emit=defineEmits<{open:[row:CloudSearchResult]}>()
const accounts=computed(()=>props.accounts.filter(a=>(!props.fixedAccountId||a.id===props.fixedAccountId)&&['lanzou','quark','pan123','baidu','guangya','ctdav','webdav','ctfile'].includes(a.kind)))
const accountId=ref(props.fixedAccountId||props.initialAccountId||accounts.value[0]?.id||''),query=ref(''),includePath=ref(false),busy=ref(false),stopping=ref(false),error=ref(''),status=ref<CloudSearchStatus|null>(null),page=ref(1),kind=ref('all')
const rows=computed(()=>(status.value?.items??[]).filter(r=>kind.value==='all'||(kind.value==='folders')===r.entry.folder))
const visible=computed(()=>rows.value.slice((page.value-1)*50,page.value*50))
function label(id:string){const a=accounts.value.find(a=>a.id===id);if(!a)return '原账号已不可用';const same=accounts.value.filter(b=>b.kind===a.kind);return ({lanzou:'蓝奏云',quark:'夸克网盘',pan123:'123 云盘',baidu:'百度网盘',guangya:'光鸭云盘',ctdav:'诚通WebDAV',webdav:'WebDAV',ctfile:'诚通OpenAPI'} as Record<string,string>)[a.kind]+(same.length>1?' '+(same.findIndex(b=>b.id===id)+1):'')}
function stateLabel(value:string){return ({running:'扫描中',completed:'扫描结束',partial:'部分结果',limited:'达到上限',cancelled:'已停止',invalidated:'来源已变化'} as Record<string,string>)[value]??value}
let generation=0,disposed=false,timer:ReturnType<typeof setTimeout>|undefined,session=''
function clearTimer(){if(timer!==undefined){clearTimeout(timer);timer=undefined}}
async function poll(stamp:number){if(disposed||stamp!==generation||!session)return;try{const result=await api.CloudSearchStatus(session);if(disposed||stamp!==generation)return;status.value=result;busy.value=result.state==='running';if(busy.value)timer=setTimeout(()=>void poll(stamp),600);else stopping.value=false}catch(e){if(!disposed&&stamp===generation){error.value=String(e);busy.value=false;stopping.value=false;if(status.value){status.value.state='partial';status.value.message='读取搜索进度失败，已请求停止；显示的是已扫描部分'}if(session)void api.CancelCloudSearch(session).catch(()=>{})}}}
async function start(){
 if(disposed||busy.value||props.blocked||(!props.listLanzouCandidates&&!query.value.trim())||!accounts.value.length)return
 const current=props.fixedAccountId||accountId.value
 if(props.listLanzouCandidates&&(!props.fixedAccountId||!accounts.value.some(a=>a.id===current&&a.kind==='lanzou')))return
 const allAccounts=!props.fixedAccountId&&current==='*'
 const ids=allAccounts?accounts.value.map(a=>a.id):[current]
 if(!allAccounts&&!accounts.value.some(a=>a.id===current))return
 if(ids.length>20){error.value='全部账号超过20个，请选择单个账号';return}
 clearTimer()
 const stamp=++generation
 busy.value=true;stopping.value=false;error.value='';status.value=null;page.value=1;session=''
 try{
  const result=await api.StartCloudSearch({accountIds:ids,query:props.listLanzouCandidates?'':query.value.trim(),
   includePath:!props.listLanzouCandidates&&includePath.value,firstMatchPerAccount:allAccounts,listLanzouCandidates:props.listLanzouCandidates})
  if(disposed||stamp!==generation){void api.CancelCloudSearch(result.id).catch(()=>{});return}
  session=result.id;status.value=result;busy.value=result.state==='running'
  if(busy.value)timer=setTimeout(()=>void poll(stamp),100)
 }catch(e){if(!disposed&&stamp===generation){error.value=String(e);busy.value=false}}
}
async function stop(){if(!session||!busy.value||stopping.value)return;stopping.value=true;try{await api.CancelCloudSearch(session)}catch(e){error.value=String(e);stopping.value=false}}
function invalidate(){const old=session;generation++;session='';clearTimer();status.value=null;busy.value=false;stopping.value=false;error.value='';page.value=1;if(old)void api.CancelCloudSearch(old).catch(()=>{})}
function resultAvailable(row:CloudSearchResult){
 return !disposed&&!busy.value&&!props.blocked&&status.value?.state!=='invalidated'&&accounts.value.some(a=>a.id===row.accountId)
}
function open(row:CloudSearchResult){if(resultAvailable(row))emit('open',row)}
watch(kind,()=>{page.value=1})
watch(()=>JSON.stringify([props.fixedAccountId,props.refreshKey,props.listLanzouCandidates,props.accounts.map(a=>[a.id,a.kind,a.endpoint,a.username])]),()=>{invalidate();if(props.fixedAccountId)accountId.value=props.fixedAccountId;else if(accountId.value!=='*'&&!accounts.value.some(a=>a.id===accountId.value))accountId.value=accounts.value[0]?.id??''})
onUnmounted(()=>{disposed=true;invalidate()})
</script>
<template>
 <section class="global-search">
  <div v-if="!fixedAccountId" class="view-heading"><h1>目录搜索</h1></div>
  <p v-if="listLanzouCandidates" class="recycle-note">列出当前账号根目录及两级子目录中的项目。选入读取清单后才能逐项查看链接。项目是否已发布分享以网盘为准。</p>
  <p v-else-if="fixedAccountId" class="recycle-note">搜索当前账号的根目录及两级子目录，支持文件名、文件夹名和路径。</p>
  <details v-else class="operation-details"><summary>搜索根目录及两级子目录</summary><p>选择全部账号时，每个网盘找到首个匹配后继续搜索下一盘；单账号返回范围内的全部匹配。最多扫描 2 分钟、500 个目录、5000 个项目，保留 1000 条结果。</p></details>
  <form class="search-controls" @submit.prevent="start"><label v-if="!fixedAccountId">网盘<select v-model="accountId" :disabled="busy||blocked" aria-label="选择搜索网盘"><option v-if="!accounts.length" value="">暂无可搜索账号</option><option value="*">全部已接入账号（最多20个）</option><option v-for="a in accounts" :key="a.id" :value="a.id">{{label(a.id)}}</option></select></label><label v-if="!listLanzouCandidates" class="search-keyword">搜索词<input v-model="query" maxlength="100" :disabled="busy||blocked" placeholder="输入文件或文件夹名称" aria-label="目录搜索词"/></label><label v-if="!listLanzouCandidates" class="search-path"><input v-model="includePath" type="checkbox" :disabled="busy||blocked"/>同时匹配路径</label><button class="primary" :disabled="busy||blocked||(!listLanzouCandidates&&!query.trim())||!accounts.length">{{listLanzouCandidates?'读取候选清单':'开始搜索'}}</button><button v-if="busy" type="button" class="secondary" :disabled="stopping" @click="stop">{{stopping?'正在停止…':'停止搜索'}}</button></form>
  <p v-if="error" class="banner error" role="alert">{{error}}</p>
  <section v-if="status" class="search-progress" role="status"><strong>{{stateLabel(status.state)}} · 已读 {{status.directories}} 个目录 / {{status.scanned}} 个项目 · 找到 {{(status.items?.length??0)}} 项</strong><p>{{status.message}}</p><p v-if="busy">当前网盘：{{status.currentAccountId?label(status.currentAccountId):'准备扫描'}} · {{status.current}}</p><p v-if="busy">停止后保留已找到结果；离开页面会停止本次搜索。</p></section>
  <details v-if="status?.issues?.length" class="download-details"><summary>未完整扫描的原因（最多显示20条）</summary><p v-for="(issue,i) in status.issues??[]" :key="i">{{issue}}</p></details>
  <div v-if="status" class="search-result-toolbar"><label>显示<select v-model="kind" aria-label="搜索结果类型"><option value="all">全部项目</option><option value="files">文件</option><option value="folders">文件夹</option></select></label><span>{{rows.length}} 条 · 每页 50 条</span><slot name="page-actions" :rows="visible" :disabled="busy||!!blocked||status?.state==='invalidated'"/></div>
  <p v-if="status&&!rows.length" class="recycle-empty">{{busy?'正在查找…':status.state==='completed'?'本次扫描没有匹配项。':'已扫描部分没有匹配项，不能据此判断整个网盘没有该项目。'}}</p>
  <div v-else-if="status" class="search-table-scroll">
   <table class="search-table">
    <thead><tr><th>名称</th><th>网盘</th><th>类型</th><th>所在路径</th><th>操作</th></tr></thead>
    <tbody>
     <tr v-for="r in visible" :key="r.accountId+':'+r.parentId+':'+r.entry.folder+':'+r.entry.id">
      <td>{{r.entry.name}}</td><td>{{label(r.accountId)}}</td><td>{{r.entry.folder?'文件夹':'文件'}}</td><td>{{r.path}}</td>
      <td>
       <div class="share-history-actions">
        <button class="secondary" :disabled="!resultAvailable(r)" @click="open(r)">打开所在目录</button>
        <slot name="result-actions" :row="r" :disabled="!resultAvailable(r)"/>
       </div>
      </td>
     </tr>
    </tbody>
   </table>
  </div>
  <footer class="recycle-footer"><span>第 {{page}} 页</span><div><button class="secondary" :disabled="busy||page<=1" @click="page--">上一页</button><button class="secondary" :disabled="busy||page*50>=rows.length" @click="page++">下一页</button></div></footer>
 </section>
</template>
