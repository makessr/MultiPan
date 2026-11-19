<script setup lang="ts">
import {computed,ref,watch,onMounted,nextTick} from 'vue'
import {trapDialogFocus} from './dialogFocus'
import {downloadPage} from './downloadView'
import type {UploadReview,UploadSkip} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{report:UploadReview|null;checking:boolean;submitting:boolean;error:string}>()
const emit=defineEmits<{cancel:[];check:[];submit:[skips:UploadSkip[]]}>()
const excluded=ref<string[]>([]),page=ref(1),dialog=ref<HTMLElement|null>(null)
const key=(r:{fileIndex:number;targetIndex:number})=>r.fileIndex+':'+r.targetIndex
watch(()=>props.report,report=>{excluded.value=report?.items?.filter(r=>r.status==='conflict'||r.status==='unknown').map(key)??[];page.value=1},{immediate:true})
const priority:Record<string,number>={conflict:0,unknown:1,reuse:2,ready:3}
const rows=computed(()=>[...(props.report?.items??[])].sort((a,b)=>(priority[a.status]??1)-(priority[b.status]??1)))
const view=computed(()=>downloadPage(rows.value,page.value,25))
const count=computed(()=>props.report?.items?.filter(r=>!excluded.value.includes(key(r))).length??0)
const conflicts=computed(()=>props.report?.items?.filter(r=>r.status==='conflict').length??0)
const unknown=computed(()=>props.report?.items?.filter(r=>r.status==='unknown').length??0)
function toggle(row:{fileIndex:number;targetIndex:number},include:boolean){if(props.checking||props.submitting)return;const id=key(row);excluded.value=include?excluded.value.filter(x=>x!==id):[...new Set([...excluded.value,id])]}
function submit(){if(!props.report?.items||props.checking||props.submitting||!count.value)return;emit('submit',props.report.items.filter(r=>excluded.value.includes(key(r))).map(({fileIndex,targetIndex})=>({fileIndex,targetIndex})))}
onMounted(()=>nextTick(()=>dialog.value?.focus()))
const cloudName=(kind:string)=>(({lanzou:'蓝奏云',quark:'夸克网盘',pan123:'123 云盘',baidu:'百度网盘',ctdav:'诚通网盘',webdav:'WebDAV',guangya:'光鸭云盘',ctfile:'诚通 OpenAPI'} as Record<string,string>)[kind]??kind)
</script>
<template>
 <div class="modal-backdrop"><section ref="dialog" tabindex="-1" class="operation-dialog upload-plan-dialog" role="dialog" aria-modal="true" aria-labelledby="upload-review-title" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="!submitting&&emit('cancel')">
  <div class="modal-heading"><h2 id="upload-review-title">{{checking?'正在检查上传目标':'检查上传计划'}}</h2><button class="secondary" :disabled="submitting" @click="emit('cancel')">{{checking?'取消检查':'取消'}}</button></div>
  <p v-if="checking" role="status">正在读取目标目录并检查同名项目，尚未创建任何上传任务。最多等待60秒。</p>
  <template v-else-if="report"><p>共 {{report.items?.length??0}} 项目标计划 · 同名冲突 {{conflicts}} 项 · 未能确认 {{unknown}} 项 · 将上传 {{count}} 项</p><p class="preview-note">同名文件默认跳过；唯一同名文件夹可以复用。不覆盖、不自动改名。未确认项默认跳过，可勾选后按网盘规则尝试。检查后目录仍可能变化，实际结果以任务记录为准。</p>
   <ul class="upload-review-list"><li v-for="row in view.items" :key="key(row)"><label><input type="checkbox" :aria-label="'上传计划：'+row.name+'，'+cloudName(row.kind)" :disabled="submitting||row.status==='conflict'" :checked="!excluded.includes(key(row))" @change="toggle(row,($event.target as HTMLInputElement).checked)"/><strong>{{row.name}}</strong><span :class="{'form-error':row.status==='conflict'}">{{row.status==='conflict'?'跳过同名':row.status==='reuse'?'复用目录':row.status==='ready'?'可上传':'未确认'}}</span></label><small>目标 {{row.targetIndex+1}} · {{cloudName(row.kind)}} / {{row.destination}}</small><p>{{row.message}}</p></li></ul>
   <div v-if="view.pages>1" class="queue-pagination"><button class="secondary" :disabled="submitting||view.page===1" @click="page--">上一页</button><span>第 {{view.page}} / {{view.pages}} 页</span><button class="secondary" :disabled="submitting||view.page===view.pages" @click="page++">下一页</button></div>
  </template>
  <p v-if="error" role="alert" class="banner error">{{error}}</p><div class="modal-actions"><button v-if="!checking" class="secondary" :disabled="submitting" @click="emit('check')">重新检查</button><button v-if="report&&!checking" class="primary" :disabled="submitting||!count" @click="submit">{{submitting?'正在创建任务…':'上传保留的 '+count+' 项'}}</button></div>
 </section></div>
</template>
