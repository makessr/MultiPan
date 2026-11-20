<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import {shareAccountLabel} from './shareAccounts'
import type {Account,AccountCheckStatus} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accounts:Account[];blocked?:boolean;refreshKey?:string}>()
const emit=defineEmits<{account:[id:string]}>()
const selected=ref<string[]>([]),status=ref<AccountCheckStatus|null>(null),busy=ref(false),error=ref('')
let disposed=false,generation=0,timer:ReturnType<typeof setTimeout>|undefined
const rows=computed(()=>status.value?.rows??[])
const running=computed(()=>status.value?.state==='running')
const checked=computed(()=>rows.value.filter(r=>!['queued','checking'].includes(r.state)).length??0)
const accountRows=computed(()=>{
 const results=new Map((status.value?.previousRows??[]).map(result=>[result.accountId,result]))
 for(const result of rows.value)results.set(result.accountId,result)
 return props.accounts.map(account=>({id:account.id,name:shareAccountLabel(account,props.accounts),result:results.get(account.id)}))
})
function label(state?:string){return ({queued:'等待',checking:'检查中',available:'读取通过',unconfirmed:'未能确认',cancelled:'已停止',changed:'需要重查'} as Record<string,string>)[state??'']??'未检查'}
function toggle(id:string,on:boolean){if(busy.value||running.value||props.blocked)return;if(!on)selected.value=selected.value.filter(x=>x!==id);else if(selected.value.length<20&&!selected.value.includes(id))selected.value.push(id)}
async function poll(){if(disposed)return;const stamp=++generation;clearTimeout(timer);try{const s=await api.AccountCheckStatus('');if(disposed||stamp!==generation)return;status.value=s;error.value='';if(s.state==='running')timer=setTimeout(poll,800)}catch(e){if(!disposed&&stamp===generation)error.value='读取检查状态失败：'+String(e)}}
async function start(ids:string[]=selected.value){if(disposed||busy.value||running.value||props.blocked||!ids.length||ids.length>20)return;busy.value=true;error.value='';clearTimeout(timer);const stamp=++generation;try{const s=await api.StartAccountCheck([...ids]);if(disposed){await api.CancelAccountCheck(s.id);return}if(stamp!==generation){await api.CancelAccountCheck(s.id);return}status.value=s;timer=setTimeout(poll,200)}catch(e){if(!disposed&&stamp===generation)error.value=String(e)}finally{if(!disposed)busy.value=false}}
async function stop(){if(disposed||busy.value||!running.value||!status.value?.id)return;busy.value=true;try{await api.CancelAccountCheck(status.value.id);await poll()}catch(e){if(!disposed)error.value=String(e)}finally{if(!disposed)busy.value=false}}
function update(id:string){if(!disposed&&!busy.value&&!running.value&&!props.blocked&&props.accounts.some(a=>a.id===id))emit('account',id)}
watch(()=>JSON.stringify([props.refreshKey,props.accounts.map(a=>[a.id,a.kind,a.endpoint,a.username,a.folder,a.userAgent])]),()=>{generation++;status.value=null;selected.value=selected.value.filter(id=>props.accounts.some(a=>a.id===id));void poll()})
onMounted(()=>{void poll()})
onUnmounted(()=>{disposed=true;generation++;clearTimeout(timer);if(status.value?.state==='running')void api.CancelAccountCheck(status.value.id).catch(()=>{})})
</script>
<template>
<section class="account-health">
 <div class="heading"><h1>账号连接检查</h1><button class="secondary" :disabled="busy||!running" @click="stop">停止检查</button></div>
 <details class="operation-details"><summary>检查范围</summary><p>逐账号执行认证读取，每批最多 20 个账号，每账号最多 15 秒、整批最多 2 分钟。DAV 检查默认目录。检查结果仅在本次运行中有效，离开页面会结束后续检查。</p></details>
 <p v-if="error" class="banner error" role="alert">{{error}}</p>
 <div class="health-toolbar"><button class="secondary" :disabled="busy||running||blocked||!accounts.length" @click="selected=accounts.slice(0,20).map(a=>a.id)">选择前20个账号</button><button class="secondary" :disabled="busy||running||blocked" @click="selected=[]">取消选择</button><button class="primary" :disabled="busy||running||blocked||!selected.length" @click="start()">检查所选（{{selected.length}}）</button><span v-if="status" role="status">{{status.state==='running'?'正在检查':status.state==='stopped'?'已停止':status.state==='complete'?'本轮已结束':'尚未开始'}} · {{checked}} / {{rows.length}}</span><button class="secondary" :disabled="busy" @click="poll">刷新状态</button></div>
 <p v-if="!accounts.length" class="recycle-note">请先添加网盘账号。</p>
 <div v-if="accountRows.length" class="health-accounts">
  <article v-for="account in accountRows" :key="account.id" class="health-account">
   <div class="health-row">
    <label class="health-identity" :title="account.name">
     <input type="checkbox" :checked="selected.includes(account.id)"
      :disabled="busy||running||blocked||selected.length>=20&&!selected.includes(account.id)"
      @change="toggle(account.id,($event.target as HTMLInputElement).checked)"/>
     <strong>{{account.name}}</strong>
     <span class="health-state" :data-state="account.result?.state">{{label(account.result?.state)}}</span>
     <time v-if="account.result?.checkedAt&&!account.result.checkedAt.startsWith('0001-')"
      class="health-time" :datetime="account.result.checkedAt">本次尝试时间 {{new Date(account.result.checkedAt).toLocaleString()}}</time>
    </label>
    <div class="health-actions">
     <button class="secondary" :disabled="busy||running||blocked" @click="start([account.id])">检查此账号</button>
     <button class="secondary" :disabled="busy||running||blocked" @click="update(account.id)">更新登录 / 配置</button>
    </div>
   </div>
   <p v-if="account.result?.message" class="health-result">{{account.result.message}}</p>
  </article>
 </div>
</section>
</template>
<style scoped>
.health-toolbar,.health-actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
.health-toolbar{margin:16px 0}
.health-accounts{border-top:1px solid var(--line)}
.health-account{padding:10px 0;border-bottom:1px solid var(--line)}
.health-row{display:flex;align-items:center;justify-content:space-between;gap:8px 16px;flex-wrap:wrap}
.health-identity{display:flex;align-items:baseline;gap:9px;flex:1;min-width:min(100%,360px)}
.health-identity input{align-self:center;flex:none;margin:0;accent-color:var(--accent)}
.health-identity strong{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-weight:600}
.health-state,.health-time{color:var(--muted);font-size:12px;white-space:nowrap}
.health-time{font-variant-numeric:tabular-nums}
.health-state[data-state=available]{color:var(--success)}
.health-result{display:block;margin:5px 0 0 22px;color:var(--muted);font-size:12px;overflow-wrap:anywhere}
</style>
