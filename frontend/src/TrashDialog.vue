<script setup lang="ts">
import {computed,onUnmounted,ref} from 'vue'
import BatchOperationReport from './BatchOperationReport.vue'
import {trapDialogFocus} from './dialogFocus'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {TrashInput} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{inputs:TrashInput[];accountName:string;location:string;browserRestore?:boolean}>()
const emit=defineEmits<{close:[];changed:[]}>()
const agreed=ref(false),busy=ref(false),started=ref(false),stop=ref(false),error=ref(''),status=ref('')
const results=ref<{name:string;state:string;message:string}[]>([])
const canStart=computed(()=>!busy.value&&!started.value&&agreed.value&&props.inputs.length>0&&props.inputs.length<=50)
let disposed=false
function label(state:string){return ({confirmed:'已移入回收站',working:'处理中',unknown:'待核对',not_sent:'未提交',skipped:'未执行'} as Record<string,string>)[state]??state}
async function start(){
 if(!canStart.value||disposed)return
 busy.value=true;started.value=true;error.value='';status.value='正在检查全部所选项目…'
 const inputs=props.inputs.map(r=>({...r,entry:{...r.entry}}))
 let preflightPassed=false
 try{
  const prepared=await api.PrepareCloudTrash(inputs)
  if(!prepared||prepared.length!==inputs.length||prepared.some((r,i)=>r.name!==inputs[i].entry.name||r.folder!==inputs[i].entry.folder||!r.handle))throw Error('检查结果与所选项目不一致，未提交')
  preflightPassed=true
  for(let i=0;i<prepared.length;i++){
   if(disposed||stop.value)break
   const row=prepared[i];status.value='正在处理 '+(i+1)+' / '+prepared.length+'：'+row.name
   results.value.push({name:row.name,state:'working',message:'本项结果待核对，请查看本机移入记录'})
   const r=await api.TrashCloudEntry(row.handle)
   if(disposed)break
   results.value[i]={name:row.name,state:r.state,message:r.message}
   if(r.state!=='confirmed'){stop.value=true}
  }
  if(!disposed)status.value=stop.value?'已停止后续处理，请先核对逐项结果':'本次处理完成'
 }catch(e){if(!disposed){error.value=String(e);status.value='处理未完成；未开始的项目不会继续提交。请在本机记录核对已提交项目。'}}
 finally{
  busy.value=false
  if(!disposed){
   const current=results.value.at(-1)
   if(current?.state==='working')current.state='unknown'
   for(const row of inputs.slice(results.value.length))results.value.push({name:row.entry.name,state:preflightPassed?'skipped':'not_sent',message:'没有发送请求'})
   emit('changed')
  }
 }
}
onUnmounted(()=>{disposed=true;stop.value=true})
</script>
<template>
 <div class="modal-backdrop" @click.self="!busy&&emit('close')" @keydown.esc.prevent="!busy&&emit('close')">
  <section class="operation-dialog" role="dialog" aria-modal="true" aria-labelledby="trash-title" @keydown.tab="trapDialogFocus">
   <h2 id="trash-title">移入回收站</h2><p>网盘：{{accountName}} · 目录：{{location}}</p>
   <p>共 {{inputs.length}} 项。<template v-if="browserRestore"><template v-if="inputs.some(r=>r.entry.folder)">蓝奏支持没有子文件夹的文件夹，其中的文件会一起移入回收站。程序会在提交前检查子文件夹。</template>可在 MultiPan 的回收站核对，到蓝奏云官网还原。</template><template v-else>文件夹及其中内容一起移入服务商回收站。可在“回收站”页面核对和还原。</template>保留期限以服务商为准。</p>
   <ul><li v-for="r in inputs" :key="(r.entry.folder?'folder:':'file:')+r.entry.id">{{r.entry.name}}{{r.entry.folder?'（文件夹，含内部内容）':''}}</li></ul>
   <label v-if="!started" class="checkbox-row"><input v-model="agreed" type="checkbox"/>我已核对账号、目录和上述项目，确认移入回收站</label>
   <p>开始前检查整批项目，执行前再次核对原项目。遇到未确认结果停止后续；停止会等待当前项目返回，已完成项目保留。云端并发修改可能影响结果。</p>
   <p v-if="error" class="banner error" role="alert">{{error}}</p><p v-if="status" role="status">{{status}}</p>
   <ul aria-label="移入回收站逐项结果"><li v-for="(r,i) in results" :key="i"><strong>{{r.name}} · {{label(r.state)}}</strong><p>{{r.message}}</p></li></ul>
   <BatchOperationReport v-if="started&&!busy" operation="移入回收站" :account-name="accountName" :rows="results"/>
   <div class="modal-actions"><button class="secondary" :disabled="busy" @click="emit('close')">{{started?'关闭':'取消'}}</button><button v-if="!started" class="primary" :disabled="!canStart" @click="start">确认移入回收站</button><button v-if="busy" class="secondary" :disabled="stop" @click="stop=true">停止后续处理</button></div>
  </section>
 </div>
</template>
