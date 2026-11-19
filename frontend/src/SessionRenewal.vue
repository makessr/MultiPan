<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import * as api from '../bindings/multipan/desktopservice'
import type {Account,SessionRenewalView} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accounts:Account[];blocked?:boolean;refreshKey?:string}>()
const emit=defineEmits<{mutation:[active:boolean];account:[id:string]}>()
const views=ref<SessionRenewalView[]>([]),error=ref(''),message=ref(''),actionId=ref(''),confirmID=ref('')
let disposed=false,generation=0,timer:ReturnType<typeof setTimeout>|undefined
const accounts=computed(()=>props.accounts.filter(a=>a.kind==='pan123'))
function label(id:string){const index=accounts.value.findIndex(a=>a.id===id);return index<0?'原账号已移除':'123云盘'+(accounts.value.length>1?' '+(index+1):'')}
function view(id:string){return views.value.find(r=>r.accountId===id)}
function time(value?:string){return !value||value.startsWith('0001-')?'尚无':new Date(value).toLocaleString()}
function outcome(value:string){return ({manual_reset:'手动凭据已更换，旧来源已隔离，自动更新关闭',enabled:'已启用自动更新',disabled:'自动更新已关闭',confirmed:'令牌更新已验证并保存',unconfirmed:'更新结果未确认；有已保存令牌可只读核对',candidate_mismatch:'待确认令牌身份不一致，已弃用，请手动重新登录',read_failed:'未确认原账号身份，未发送更新；自动更新暂停',identity_changed:'身份不一致，旧来源已隔离，请手动重新登录'} as Record<string,string>)[value]??'尚未更新'}
async function load(){if(disposed)return;clearTimeout(timer);const stamp=++generation;try{const rows=await api.SessionRenewals();if(!disposed&&stamp===generation)views.value=rows??[]}catch{if(!disposed&&stamp===generation)error.value='读取登录维护状态失败，请稍后刷新'}finally{if(!disposed&&stamp===generation&&!actionId.value)timer=setTimeout(load,1500)}}
function prepare(id:string){const r=view(id);if(disposed||actionId.value||confirmID.value||props.blocked||!r||r.busy||r.pending)return;confirmID.value=id;error.value='';message.value=''}
async function toggle(id:string){const r=view(id);if(disposed||actionId.value||confirmID.value||props.blocked||!r||r.busy||r.pending)return;await action(id,()=>api.SetSessionAutoRenew(id,!r.enabled),r.enabled?'已关闭此账号自动更新':'已启用此账号自动更新；首次后台检查时尝试，之后每12小时一次')}
async function renew(){const id=confirmID.value,r=view(id);if(!id||!r||r.pending||r.busy||actionId.value||props.blocked||disposed)return;confirmID.value='';await action(id,()=>api.RenewAccountSession(id),'已核验同一账号并保存当前令牌；没有重新执行原任务')}
async function check(id:string){const r=view(id);if(disposed||actionId.value||confirmID.value||props.blocked||!r?.pending||!r.canCheck||r.busy)return;await action(id,()=>api.CheckSessionRenewal(id),'已核对并保存已收到的同账号令牌；自动更新保持关闭，没有重新请求更新或执行原任务')}
async function action(id:string,fn:()=>Promise<unknown>,success:string){actionId.value=id;clearTimeout(timer);const stamp=++generation;error.value='';message.value='';emit('mutation',true);try{await fn();if(!disposed&&stamp===generation)message.value=success}catch(e){if(!disposed&&stamp===generation)error.value=String(e)}finally{actionId.value='';emit('mutation',false);if(!disposed)await load()}}
function login(id:string){if(!disposed&&!actionId.value&&!confirmID.value&&!props.blocked&&!view(id)?.busy&&accounts.value.some(a=>a.id===id))emit('account',id)}
watch(()=>JSON.stringify([props.refreshKey,props.accounts.map(a=>[a.id,a.kind,a.endpoint,a.username,a.userAgent])]),()=>{generation++;confirmID.value='';views.value=[];error.value='';message.value='';void load()})
onMounted(()=>{void load()})
onUnmounted(()=>{disposed=true;generation++;clearTimeout(timer);emit('mutation',false)})
</script>
<template>
<section>
 <div class="heading"><h1>123 登录维护</h1><button class="secondary" :disabled="!!actionId||blocked" @click="load">刷新本机状态</button></div>
 <details class="operation-details"><summary>实验功能与令牌更新</summary><p>更新可能使旧令牌失效。请先完成或暂停此账号的任务。结果未确认时将暂停自动更新，有已保存的返回令牌时可只读核对，否则需要手动登录。</p>
  <p>自动更新默认关闭。启用后，应用运行期间每 12 小时尝试更新。核验为同一账号后保留原任务关联。</p></details>
 <p v-if="error" class="banner error" role="alert">{{error}}</p><p v-if="message" class="banner notice" role="status">{{message}}</p>
 <section v-if="confirmID" class="share-history-card" role="alertdialog" aria-label="确认更新123登录令牌"><strong>{{label(confirmID)}} · 更新登录令牌</strong><p>确认当前账号与任务状态后更新。请等待本次结果，不要重复提交；取消不会请求更新。</p><div class="renew-actions"><button class="primary" :disabled="!!actionId||blocked||view(confirmID)?.busy||view(confirmID)?.pending" @click="renew">确认更新</button><button class="secondary" :disabled="!!actionId" @click="confirmID=''">取消</button></div></section>
 <p v-if="!accounts.length" class="recycle-note">暂无可维护的123账号，请先添加或登录。</p>
 <article v-for="a in accounts" :key="a.id" class="share-history-card"><div class="share-history-card-heading"><strong>{{label(a.id)}}</strong><span>{{view(a.id)?.busy?'正在处理':view(a.id)?.pending?(view(a.id)?.canCheck?'可核对已收到的令牌':'等待手动重新登录'):view(a.id)?.enabled?'自动更新已启用':'自动更新关闭'}}</span></div><p>{{outcome(view(a.id)?.outcome??'')}}</p><p>最近尝试：{{time(view(a.id)?.last)}}<template v-if="view(a.id)?.enabled&&!view(a.id)?.pending"> · 本机下次计划：{{time(view(a.id)?.next)}}</template></p><p v-if="view(a.id)?.pending" class="banner notice">上次更新结果未确认，重启也不会自动重发。若可核对，只读取已保存令牌的服务端身份，一致后保存，自动更新保持关闭；否则请重新登录原账号。</p><p v-if="view(a.id)?.checkedAt&&!view(a.id)?.checkedAt.startsWith('0001-')">最近身份核对：{{time(view(a.id)?.checkedAt)}}</p><div class="renew-actions"><button v-if="view(a.id)?.canCheck" class="primary" :disabled="!!actionId||!!confirmID||blocked||view(a.id)?.busy" @click="check(a.id)">核对已收到的令牌</button><button class="secondary" :disabled="!!actionId||!!confirmID||blocked||!view(a.id)||view(a.id)?.busy||view(a.id)?.pending" @click="prepare(a.id)">更新此账号令牌</button><button class="secondary" :disabled="!!actionId||!!confirmID||blocked||!view(a.id)||view(a.id)?.busy||view(a.id)?.pending" @click="toggle(a.id)">{{view(a.id)?.enabled?'关闭自动更新':'启用每12小时自动更新'}}</button><button class="secondary" :disabled="!!actionId||!!confirmID||blocked||view(a.id)?.busy" @click="login(a.id)">手动重新登录</button></div></article>
</section>
</template>
<style scoped>.renew-actions{display:flex;gap:12px;align-items:center;flex-wrap:wrap;margin-top:16px}</style>
