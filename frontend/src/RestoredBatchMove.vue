<script setup lang="ts">
import {nextTick,onUnmounted,ref} from 'vue'
import FolderPicker from './FolderPicker.vue'
import BatchOperationReport from './BatchOperationReport.vue'
import {trapDialogFocus} from './dialogFocus'
import {CheckConfirmedRestoredFileMove,MoveConfirmedRestoredFile,ValidateRestoredFileMoves} from '../bindings/multipan/internal/desktop/desktopservice'

const props=defineProps<{
 accountId:string
 accountName:string
 kind:string
 checkOnly?:boolean
 targets:{id:string;name:string}[]
 blocked?:boolean
}>()
const emit=defineEmits<{close:[]}>()
const dialog=ref<HTMLElement|null>(null)
const choosing=ref(true)
const destination=ref<{id:string;name:string}|null>(null)
const busy=ref(false)
const checking=ref(false)
const stopped=ref(false)
const started=ref(false)
const status=ref('')
const error=ref('')
type RowState='queued'|'working'|'confirmed'|'at_original'|'at_target'|'unknown'|'skipped'
const rows=ref(props.targets.map(target=>({...target,state:'queued' as RowState,detail:''})))
const labels:Record<RowState,string>={queued:props.checkOnly?'待核对':'待移动',working:'正在处理',
 confirmed:'已移动',at_original:'仍在原目录',at_target:'已在所选目标',unknown:'待核对',skipped:'未提交'}
let disposed=false
onUnmounted(()=>{disposed=true})

function close(){
 if(!busy.value)emit('close')
}

async function choose(id:string,name:string){
 if(props.blocked||busy.value||started.value&&!props.checkOnly||!id)return
 if(props.checkOnly){
  started.value=false
  for(const row of rows.value){row.state='queued';row.detail=''}
  status.value=''
 }
 destination.value={id,name}
 choosing.value=false
 error.value=''
 await nextTick()
 dialog.value?.focus()
}

async function move(){
 const target=destination.value
 if(!target||props.checkOnly||props.blocked||busy.value||started.value||!rows.value.length)return
 busy.value=true
 stopped.value=false
 status.value='正在检查原目录和目标，尚未提交移动。'
 error.value=''
 try{
  await ValidateRestoredFileMoves(props.accountId,target.id,rows.value.map(row=>row.id))
  if(disposed||stopped.value||props.blocked){
   status.value='已停止，尚未提交移动。'
   return
  }
  started.value=true
  for(const row of rows.value){
   if(disposed||stopped.value||props.blocked)break
   status.value='正在移动「'+row.name+'」'
   row.state='working'
   try{
    await MoveConfirmedRestoredFile(props.accountId,row.id,target.id)
    row.state='confirmed'
   }catch(e){
    row.state='unknown'
    row.detail=String(e)
    break
   }
  }
  for(const row of rows.value)if(row.state==='queued')row.state='skipped'
  const count=rows.value.filter(row=>row.state==='confirmed').length
  status.value='已移动 '+count+' / '+rows.value.length+' 项。'
 }catch(e){
  status.value=''
  error.value='整批未提交：'+String(e)
 }finally{
  busy.value=false
 }
}

async function check(){
 const target=destination.value
 if(!target||props.blocked||busy.value)return
 const candidates=props.checkOnly?rows.value:rows.value.filter(row=>row.state==='unknown'||row.state==='at_original')
 if(!candidates.length)return
 busy.value=true;checking.value=true;stopped.value=false;error.value='';started.value=true
 let processed=0
 try{
  for(const row of candidates){
   if(disposed||stopped.value||props.blocked)break
   status.value='正在核对「'+row.name+'」'
   row.state='working'
   try{
    const result=await CheckConfirmedRestoredFileMove(props.accountId,row.id,target.id)
    row.state=result.state==='confirmed'?'at_target':result.state==='at_original'?'at_original':'unknown'
    row.detail=result.message
   }catch(e){
    row.state='unknown'
    row.detail=String(e)
   }
   processed++
  }
  status.value='本轮已核对 '+processed+' / '+candidates.length+' 项。'
 }finally{
  busy.value=false;checking.value=false
 }
}
</script>

<template>
 <FolderPicker v-if="choosing" :account-id="accountId" :account-name="accountName" :kind="kind"
  initial-folder="" @selected="choose" @close="close"/>
 <div v-else class="modal-backdrop">
  <section ref="dialog" class="operation-dialog share-dialog" tabindex="-1" role="dialog" aria-modal="true"
   aria-labelledby="restored-batch-move-title" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="close">
   <div class="modal-heading">
    <h2 id="restored-batch-move-title">{{checkOnly?'核对去向':'移动已还原文件'}}（{{rows.length}} 项）</h2>
    <button class="secondary" :disabled="busy" @click="close">关闭</button>
   </div>
   <p class="dialog-location">{{accountName}} / {{destination?.name}}</p>
   <p v-if="checkOnly">按所选目标与原目录的当前文件身份和大小核对。</p>
   <p v-else>整批预检查通过后逐项移动。取消时，未移动的文件留在原目录。</p>
   <ul class="recycle-batch-preview">
    <li v-for="row in rows" :key="row.id">
     <strong>{{row.name}}</strong> · {{labels[row.state]}}
     <p v-if="row.detail">{{row.detail}}</p>
    </li>
   </ul>
   <p v-if="status" role="status">{{status}}</p>
   <p v-if="error" role="alert" class="banner error">{{error}}</p>
   <div class="share-history-actions">
    <button v-if="busy" class="secondary" :disabled="stopped" @click="stopped=true">{{checking?'停止后续核对':'停止后续移动'}}</button>
    <template v-else-if="checkOnly">
     <button class="secondary" :disabled="blocked" @click="choosing=true">重新选择目录</button>
     <button class="primary" :disabled="blocked" @click="check">{{started?'再次核对':'开始核对'}}</button>
    </template>
    <template v-else-if="!started">
     <button class="secondary" :disabled="blocked" @click="choosing=true">重新选择目录</button>
     <button class="primary" :disabled="blocked" @click="move">确认移动</button>
    </template>
    <button v-else-if="rows.some(row=>row.state==='unknown'||row.state==='at_original')" class="secondary" :disabled="blocked" @click="check">只读核对待确认项</button>
   </div>
   <BatchOperationReport v-if="started&&!busy" :operation="checkOnly?'核对已还原文件去向':'批量移动已还原文件'"
    :account-name="accountName+' / '+destination?.name" :rows="rows"/>
  </section>
 </div>
</template>
