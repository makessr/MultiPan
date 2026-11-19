<script setup lang="ts">
import {nextTick,onUnmounted,ref} from 'vue'
import FolderPicker from './FolderPicker.vue'
import {trapDialogFocus} from './dialogFocus'
import {CheckConfirmedRestoredFolderMove,MoveConfirmedRestoredFolder} from '../bindings/multipan/desktopservice'
import type {CloudFolder,RestoredFileMoveCheck} from '../bindings/multipan/internal/transfer/models'

const props=defineProps<{
 accountId:string
 accountName:string
 receiptId:string
 entryId:string
 parentId:string
 name:string
 kind:string
 checkOnly?:boolean
 blocked?:boolean
}>()
const emit=defineEmits<{close:[moved:boolean]}>()
const rootID=props.kind==='baidu'?'/':props.kind==='guangya'?'root':'0'
const sourceTrail=ref<CloudFolder[]|null>(props.parentId===rootID?[{id:rootID,name:'根目录'}]:null)
const sourceName=ref(props.parentId===rootID?'根目录':'')
const targetName=ref('')
const targetID=ref('')
const checkResult=ref<RestoredFileMoveCheck|null>(null)
const checkError=ref('')
const stage=ref<'pick'|'running'|'done'|'uncertain'|'checking'|'checked'>('pick')
const error=ref('')
const dialog=ref<HTMLElement|null>(null)
let disposed=false
onUnmounted(()=>{disposed=true})

function chooseSource(id:string,path:string,trail:CloudFolder[]){
 if(!props.blocked&&id===props.parentId&&trail[0]?.id===rootID&&trail.at(-1)?.id===id){
  sourceTrail.value=trail
  sourceName.value=path
 }
}

async function move(id:string,path:string,trail:CloudFolder[]){
 const source=sourceTrail.value
 if(props.blocked||stage.value!=='pick'||!source||!id||id===props.parentId||id===props.entryId)return
 targetName.value=path
 targetID.value=id
 stage.value='running'
 await nextTick()
 if(disposed)return
 if(props.blocked){
  stage.value='pick'
  return
 }
 dialog.value?.focus()
 try{
  await MoveConfirmedRestoredFolder(props.accountId,props.receiptId,id,source,trail)
  if(!disposed)stage.value='done'
 }catch(e){
  if(!disposed){
   error.value=String(e)
   stage.value='uncertain'
  }
 }
}

function chooseCheckTarget(id:string,path:string){
 if(!props.checkOnly||props.blocked||stage.value!=='pick'||!id)return
 targetID.value=id
 targetName.value=path
 void check()
}

async function check(){
 if(props.blocked||!targetID.value||stage.value==='checking'||stage.value==='running')return
 stage.value='checking'
 checkResult.value=null
 checkError.value=''
 await nextTick()
 if(disposed)return
 dialog.value?.focus()
 try{
  const result=await CheckConfirmedRestoredFolderMove(props.accountId,props.receiptId,targetID.value)
  if(!disposed)checkResult.value=result
 }catch(e){
  if(!disposed)checkError.value=String(e)
 }finally{
  if(!disposed)stage.value='checked'
 }
}

function close(){
 if(stage.value!=='running'&&stage.value!=='checking')emit('close',stage.value==='done'||!props.checkOnly&&checkResult.value?.state==='confirmed')
}
</script>

<template>
 <FolderPicker v-if="stage==='pick'&&checkOnly" key="check-target" :account-id="accountId" :account-name="accountName"
  :kind="kind" :initial-folder="rootID" purpose="source" @selected="chooseCheckTarget" @close="close"/>
 <FolderPicker v-else-if="stage==='pick'&&!sourceTrail" key="original" :account-id="accountId" :account-name="accountName"
  :kind="kind" :initial-folder="rootID" purpose="source" :required-folder-id="parentId" @selected="chooseSource" @close="close"/>
 <FolderPicker v-else-if="stage==='pick'&&sourceTrail" key="destination" :account-id="accountId" :account-name="accountName"
  :kind="kind" :initial-folder="parentId" :initial-breadcrumbs="sourceTrail" purpose="move" :source-name="name"
  :forbidden-roots="[entryId]" @selected="move" @close="close"/>
 <div v-else class="modal-backdrop">
  <section ref="dialog" class="operation-dialog share-dialog" tabindex="-1" role="dialog" aria-modal="true"
   aria-labelledby="restored-folder-move-title" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="close">
   <div class="modal-heading">
    <h2 id="restored-folder-move-title">{{checkOnly?'核对已还原文件夹去向':'移动已还原的文件夹'}}</h2>
    <button class="secondary" :disabled="stage==='running'||stage==='checking'" @click="close">关闭</button>
   </div>
   <p>{{accountName}} · {{name}}</p>
   <p><template v-if="sourceName">原目录 {{sourceName}} → </template>所选目标 {{targetName}}</p>
   <p v-if="stage==='running'" role="status">正在核对目录层级并移动…</p>
   <p v-else-if="stage==='done'" role="status">文件夹已移动。</p>
   <p v-else-if="stage==='checking'" role="status">正在只读核对原目录和所选目标…</p>
   <p v-if="stage==='uncertain'" role="alert" class="banner error">{{error}}</p>
   <p v-if="checkResult" role="status">{{checkResult.message}}</p>
   <p v-if="checkError" role="alert" class="banner error">{{checkError}}</p>
   <button v-if="stage==='uncertain'||stage==='checked'" class="secondary" :disabled="blocked" @click="check">只读核对去向</button>
  </section>
 </div>
</template>
