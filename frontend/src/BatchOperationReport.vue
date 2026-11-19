<script setup lang="ts">
import {computed,ref,onUnmounted} from 'vue'
import * as api from '../bindings/multipan/desktopservice'

const props=defineProps<{operation:string;accountName:string;rows:{name:string;state:string}[];disabled?:boolean}>()
const copying=ref(false)
const feedback=ref('')
let disposed=false
const labels=new Map([
 ['ready','已确认'],['confirmed','已确认'],['unknown','待核对'],['working','待核对'],
 ['failed','失败'],['not_sent','未提交'],['queued','未执行'],['skipped','未执行'],
 ['at_original','仍在原目录'],['at_target','已在所选目标'],
])
function line(value:string){
 const cleaned=value.replace(/[\u0000-\u001f\u007f-\u009f\u2028\u2029]+/g,' ').trim()
 return cleaned.length>512?cleaned.slice(0,512)+'…':cleaned
}
const report=computed(()=>{
 const counts=new Map<string,number>()
 const entries=props.rows.map(row=>{
  const status=labels.get(row.state)??'待核对'
  counts.set(status,(counts.get(status)??0)+1)
  return status+' · '+(line(row.name)||'未命名项目')
 })
 const totals=[...counts].map(([status,count])=>status+' '+count+' 项').join(' · ')
 return [line(props.operation),line(props.accountName),totals,'',...entries].join('\r\n')
})
async function copy(){
 if(disposed||props.disabled||copying.value||!props.rows.length)return
 copying.value=true
 feedback.value=''
 try{
  await api.CopyDownloadReport(report.value)
  if(!disposed)feedback.value='已复制本批结果'
 }catch{
  if(!disposed)feedback.value='无法写入剪贴板，请重试'
 }finally{
  copying.value=false
 }
}
onUnmounted(()=>{disposed=true})
</script>

<template>
 <div v-if="rows.length" class="share-history-actions">
  <button class="secondary" :disabled="disabled||copying" @click="copy">复制批次结果</button>
  <span v-if="feedback" role="status">{{feedback}}</span>
 </div>
</template>
