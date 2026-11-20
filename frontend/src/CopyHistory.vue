<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {CopyDestinationLocation,CopyReceipt} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accountId:string;refreshKey?:string;blocked?:boolean}>()
const emit=defineEmits<{mutation:[active:boolean];open:[accountId:string,location:CopyDestinationLocation]}>()
const rows=ref<CopyReceipt[]>([]),busy=ref(false),error=ref(''),message=ref(''),query=ref(''),state=ref('all'),page=ref(1),stop=ref(false),checking=ref(false)
const filtered=computed(()=>rows.value.filter(r=>(state.value==='all'||r.state===state.value)&&(r.name+' '+r.destination+' '+(r.outputFolder??'')).toLocaleLowerCase().includes(query.value.trim().toLocaleLowerCase())))
const visible=computed(()=>filtered.value.slice((page.value-1)*50,page.value*50))
const pending=computed(()=>rows.value.filter(r=>r.canCheck))
const canClear=computed(()=>rows.value.some(r=>['confirmed','not_sent'].includes(r.state)))
function label(value:string){return ({working:'处理中',unknown:'待核对',confirmed:'清单已核对',not_sent:'未提交'} as Record<string,string>)[value]??value}
let generation=0,disposed=false,reload=false
async function load(){if(disposed||busy.value||!props.accountId)return;const stamp=generation,id=props.accountId;busy.value=true;error.value='';try{const result=await api.CloudCopyHistory(id);if(!disposed&&stamp===generation&&id===props.accountId)rows.value=result??[]}catch(e){if(!disposed&&stamp===generation){rows.value=[];error.value=String(e)}}finally{busy.value=false;if(reload&&!disposed){reload=false;await load()}}}
async function check(targets:CopyReceipt[]){if(disposed||busy.value||props.blocked||!targets.length)return;const stamp=generation;busy.value=true;checking.value=true;stop.value=false;error.value='';emit('mutation',true);let count=0;try{for(const r of targets){if(disposed||stamp!==generation||stop.value)break;if(!r.canCheck)continue;message.value='正在只读核对 '+(count+1)+' / '+targets.length+'：'+r.name;await api.CheckCloudCopy(r.id);count++}if(!disposed&&stamp===generation)message.value='已核对 '+count+' 条原结果；没有重新发送复制请求'}catch(e){if(!disposed&&stamp===generation)message.value='操作未完成：'+String(e)}finally{checking.value=false;busy.value=false;emit('mutation',false);if(!disposed){reload=false;await load()}}}
async function clear(){if(disposed||busy.value||props.blocked||!canClear.value)return;busy.value=true;emit('mutation',true);const stamp=generation;try{const n=await api.ClearCloudCopyHistory(props.accountId);if(!disposed&&stamp===generation)message.value='已清理 '+n+' 条已核对或未提交记录；待核对记录保留'}catch(e){if(!disposed&&stamp===generation)error.value=String(e)}finally{busy.value=false;emit('mutation',false);if(!disposed)await load()}}
async function openDestination(row:CopyReceipt,result=false){
 if(disposed||busy.value||props.blocked||row.state!=='confirmed'||row.accountId!==props.accountId)return
 if(result&&!row.canOpenResult)return
 const stamp=generation,id=props.accountId
 busy.value=true;error.value=''
 try{
  const locate=result?api.LocateCloudCopyResult:api.LocateCloudCopyDestination
  const location=await locate(id,row.id)
  if(!disposed&&stamp===generation&&!props.blocked&&location?.folderId)emit('open',id,location)
 }catch(e){if(!disposed&&stamp===generation)error.value=String(e)}
 finally{busy.value=false;if(reload&&!disposed){reload=false;await load()}}
}
watch([query,state],()=>{page.value=1})
watch(()=>props.accountId+'|'+props.refreshKey,()=>{generation++;rows.value=[];message.value='';error.value='';page.value=1;stop.value=true;if(busy.value){reload=true;return}void load()})
onMounted(()=>load())
onUnmounted(()=>{disposed=true;generation++;stop.value=true;emit('mutation',false)})
</script>
<template>
 <section class="copy-history" aria-label="本机复制记录">
  <div class="recycle-receipts-heading"><h2>本机复制记录</h2><div class="copy-history-actions"><button class="secondary" :disabled="busy||blocked" @click="load">刷新记录</button><button class="secondary" :disabled="busy||blocked||!pending.length" @click="check(pending)">核对全部待确认（{{pending.length}}）</button><button class="secondary" :disabled="busy||blocked||!canClear" @click="clear">清理已完成记录</button><button v-if="checking" class="secondary" :disabled="stop" @click="stop=true">停止后续核对</button></div></div>
  <p class="recycle-note">记录加密保存，重启后可继续检查原结果。待核对记录不能清理或重复提交；清理只影响本机记录。123 复制核对文件大小和网盘 MD5，文件夹还核对目录清单。</p>
  <div class="copy-history-actions"><input v-model="query" type="search" placeholder="筛选名称或目标目录" aria-label="筛选复制记录"/><select v-model="state" aria-label="复制记录状态"><option value="all">全部状态</option><option value="unknown">待核对</option><option value="confirmed">清单已核对</option><option value="not_sent">未提交</option></select></div>
  <p v-if="error" class="banner error" role="alert">{{error}}</p><p v-if="message" role="status">{{message}}</p><p v-if="busy" role="status">正在处理复制记录…</p><p v-else-if="!visible.length" class="recycle-note">{{rows.length?'没有匹配的记录':'当前账号暂无本机复制记录。0.72.0及更早版本的复制不会补录。'}}</p>
  <article v-for="r in visible" :key="r.id" class="share-history-card"><div class="share-history-card-heading"><strong>{{r.name}}</strong><span>{{label(r.state)}}</span></div><p>目标：{{r.destination}}<span v-if="r.outputFolder"> / {{r.outputFolder}}</span></p><p>{{r.message}}</p><button v-if="r.canCheck" class="secondary" :disabled="busy||blocked" @click="check([r])">检查原复制结果</button><button v-else-if="r.state==='confirmed'" class="secondary" :disabled="busy||blocked" @click="openDestination(r)">打开目标目录</button><button v-if="r.canOpenResult" class="secondary" :disabled="busy||blocked" @click="openDestination(r,true)">打开副本目录</button></article>
  <footer class="recycle-footer"><span>第 {{page}} 页 · 共 {{filtered.length}} 条</span><div><button class="secondary" :disabled="busy||page<=1" @click="page--">上一页</button><button class="secondary" :disabled="busy||page*50>=filtered.length" @click="page++">下一页</button></div></footer>
 </section>
</template>
