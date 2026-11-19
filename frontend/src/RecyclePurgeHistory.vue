<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import * as api from '../bindings/multipan/desktopservice'
import type {TrashReceipt} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accountId:string;refreshKey?:string;blocked?:boolean;serverClear?:boolean;lanzou?:boolean;receiptId?:string}>()
const emit=defineEmits<{mutation:[active:boolean]}>()
const rows=ref<TrashReceipt[]>([]),busy=ref(false),error=ref(''),message=ref(''),query=ref(''),state=ref('all'),page=ref(1),stop=ref(false),checking=ref(false)
const filtered=computed(()=>rows.value.filter(r=>props.receiptId?r.id===props.receiptId:(state.value==='all'||r.state===state.value)&&(r.name+' '+r.original).toLocaleLowerCase().includes(query.value.trim().toLocaleLowerCase())))
const visible=computed(()=>filtered.value.slice((page.value-1)*50,page.value*50))
const pending=computed(()=>rows.value.filter(r=>r.canCheck))
const canClear=computed(()=>rows.value.some(r=>['confirmed','not_sent'].includes(r.state)))
function label(value:string){return ({working:'处理中',unknown:'待核对',confirmed:'已受理并移除',not_sent:'未提交'} as Record<string,string>)[value]??value}
let generation=0,disposed=false,reload=false
async function load(){if(disposed||busy.value||!props.accountId)return;const stamp=generation,id=props.accountId;busy.value=true;error.value='';try{const result=await api.CloudRecyclePurgeHistory(id);if(!disposed&&stamp===generation&&id===props.accountId)rows.value=result??[]}catch(e){if(!disposed&&stamp===generation){rows.value=[];error.value=String(e)}}finally{busy.value=false;if(reload&&!disposed){reload=false;await load()}}}
async function check(targets:TrashReceipt[]){if(disposed||busy.value||props.blocked||!targets.length)return;const stamp=generation;busy.value=true;checking.value=true;stop.value=false;error.value='';emit('mutation',true);let count=0;try{for(const r of targets){if(disposed||stamp!==generation||stop.value)break;if(!r.canCheck)continue;message.value='正在只读核对 '+(count+1)+' / '+targets.length+'：'+r.name;await api.CheckCloudRecyclePurge(r.id);count++}if(!disposed&&stamp===generation)message.value='已核对 '+count+' 条原结果；没有重新发送永久删除请求'}catch(e){if(!disposed&&stamp===generation)message.value='操作未完成：'+String(e)}finally{checking.value=false;busy.value=false;emit('mutation',false);if(!disposed){reload=false;await load()}}}
async function clear(){if(disposed||busy.value||props.blocked||!canClear.value)return;busy.value=true;emit('mutation',true);const stamp=generation;try{const n=await api.ClearCloudRecyclePurgeHistory(props.accountId);if(!disposed&&stamp===generation)message.value='已清理 '+n+' 条已核对或未提交记录；待核对记录保留'}catch(e){if(!disposed&&stamp===generation)message.value='清理未完成：'+String(e)}finally{busy.value=false;emit('mutation',false);if(!disposed)await load()}}
watch([query,state,()=>props.receiptId],()=>{page.value=1})
watch(()=>props.accountId+'|'+props.refreshKey,()=>{generation++;rows.value=[];message.value='';error.value='';page.value=1;stop.value=true;if(busy.value){reload=true;return}void load()})
onMounted(()=>load())
onUnmounted(()=>{disposed=true;generation++;stop.value=true;emit('mutation',false)})
</script>
<template>
 <section class="copy-history" aria-label="本机永久删除记录">
  <div class="recycle-receipts-heading"><h2>本机永久删除记录</h2><div class="copy-history-actions"><button class="secondary" :disabled="busy||blocked" @click="load">刷新记录</button><button v-if="!receiptId" class="secondary" :disabled="busy||blocked||!pending.length" @click="check(pending)">核对全部待确认（{{pending.length}}）</button><button v-if="!receiptId" class="secondary" :disabled="busy||blocked||!canClear" @click="clear">清理已完成记录</button><button v-if="checking" class="secondary" :disabled="stop" @click="stop=true">停止后续核对</button></div></div>
  <p v-if="serverClear" class="recycle-note">光鸭的指定项永久删除和清空记录都加密保存。待核对时读取原任务和回收站状态，不会重新提交请求。</p>
  <p v-else-if="lanzou" class="recycle-note">蓝奏记录加密保存。收到删除回执且两次回读均未发现原项目时才确认结果。官网返回无法识别的空页时，请到官网核对。复查只读，清理仅影响本机记录。</p>
  <p v-else class="recycle-note">记录加密保存，重启后只读核对，不重新发送删除。必须有已保存的服务端受理回执，且完整扫描确认回收站原记录已无，才能标记已受理并移除。缺受理回执时，即使记录消失仍为待核对；请到官网核查。清理只影响本机记录。</p>
  <div v-if="!receiptId" class="copy-history-actions"><input v-model="query" type="search" placeholder="筛选名称或原位置" aria-label="筛选永久删除记录"/><select v-model="state" aria-label="永久删除记录状态"><option value="all">全部状态</option><option value="unknown">待核对</option><option value="confirmed">已受理并移除</option><option value="not_sent">未提交</option></select></div>
  <p v-if="error" class="banner error" role="alert">{{error}}</p><p v-if="message" role="status">{{message}}</p><p v-if="busy" role="status">正在处理永久删除记录…</p><p v-else-if="!visible.length" class="recycle-note">{{receiptId?'原永久删除回执已不存在或不可读取。':rows.length?'没有匹配的记录':'当前账号暂无本机永久删除记录。'}}</p>
  <article v-for="r in visible" :key="r.id" class="share-history-card"><div class="share-history-card-heading"><strong>{{r.name}}</strong><span>{{label(r.state)}}</span></div><p v-if="r.original">原位置：{{r.original}}</p><p>{{r.message}}</p><button v-if="r.canCheck" class="secondary" :disabled="busy||blocked" @click="check([r])">检查原结果</button></article>
  <footer class="recycle-footer"><span>第 {{page}} 页 · 共 {{filtered.length}} 条</span><div><button class="secondary" :disabled="busy||page<=1" @click="page--">上一页</button><button class="secondary" :disabled="busy||page*50>=filtered.length" @click="page++">下一页</button></div></footer>
 </section>
</template>
