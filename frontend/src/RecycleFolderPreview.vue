<script setup lang="ts">
import {computed,nextTick,onMounted,onUnmounted,ref,watch} from 'vue'
import {trapDialogFocus} from './dialogFocus'
import {ListLanzouRecycleFolder} from '../bindings/multipan/internal/desktop/desktopservice'
import type {LanzouRecycleFolderComparison} from '../bindings/multipan/internal/transfer/models'

const props=defineProps<{accountId:string;accountName:string;entryId:string;name:string}>()
const emit=defineEmits<{close:[]}>()
const dialog=ref<HTMLElement|null>(null)
const remote=ref<string[]>([]),original=ref<string[]|null>(null),source=ref<'remote'|'original'|'tree'>('remote')
const remoteFolders=ref<string[]>([]),hasAdditionalEntries=ref(false)
const remoteFolderIds=ref<string[]>([])
const trail=ref([{id:props.entryId,name:props.name}])
const current=computed(()=>trail.value[trail.value.length-1])
const comparison=ref<LanzouRecycleFolderComparison|null>(null)
const busy=ref(true),error=ref(''),query=ref(''),page=ref(1)
const treeRows=ref<string[]>([]),treeStatus=ref(''),scanning=ref(false)
const treeFolderLimit=20,treeRowLimit=1000
const names=computed(()=>source.value==='tree'?treeRows.value:source.value==='original'?original.value??[]:remote.value)
const filtered=computed(()=>names.value.filter(name=>name.toLocaleLowerCase().includes(query.value.trim().toLocaleLowerCase())))
const pageCount=computed(()=>Math.max(1,Math.ceil(filtered.value.length/50)))
const visible=computed(()=>filtered.value.slice((page.value-1)*50,page.value*50))
let pending:ReturnType<typeof ListLanzouRecycleFolder>|undefined
let disposed=false
let readingTree:ReturnType<typeof ListLanzouRecycleFolder>|undefined
let stopTree=false

async function load(){
 if(pending||scanning.value)return
 busy.value=true;error.value='';remote.value=[];original.value=null;comparison.value=null;source.value='remote';page.value=1
 remoteFolders.value=[];remoteFolderIds.value=[];hasAdditionalEntries.value=false;query.value=''
 pending=ListLanzouRecycleFolder(props.accountId,current.value.id,current.value.name)
 try{
  const result=await pending
  if(!disposed){
   remote.value=result.remoteFiles??[]
   remoteFolders.value=result.remoteFolders??[]
   if(result.remoteFolderIds?.length===remoteFolders.value.length)remoteFolderIds.value=result.remoteFolderIds
   hasAdditionalEntries.value=result.hasAdditionalEntries
   original.value=result.hasOriginal?result.originalFiles??[]:null
   comparison.value=result.comparison??null
  }
 }catch(e){if(!disposed)error.value=String(e)}
 finally{pending=undefined;if(!disposed)busy.value=false}
}

function canOpenFolder(index:number){
 const id=remoteFolderIds.value[index]
 return !!id&&id.startsWith('folder:')&&!trail.value.some(folder=>folder.id===id)
}

function openFolder(index:number){
 if(busy.value||scanning.value||!canOpenFolder(index))return
 trail.value=[...trail.value,{id:remoteFolderIds.value[index],name:remoteFolders.value[index]}]
 void load()
}

function returnToFolder(index:number){
 if(busy.value||scanning.value||index<0||index>=trail.value.length-1)return
 trail.value=trail.value.slice(0,index+1)
 void load()
}

async function readTree(){
 if(disposed||busy.value||scanning.value||source.value!=='remote')return
 const account=props.accountId
 const currentFolder=current.value
 const seen=new Set(trail.value.map(folder=>folder.id))
 const folders=[{id:currentFolder.id,name:currentFolder.name,path:currentFolder.name}]
 const rows:string[]=[]
 let attempted=0,unread=0,limited=false
 scanning.value=true;stopTree=false;treeRows.value=[];treeStatus.value='';query.value='';source.value='tree'
 try{
  for(const folder of folders){
   if(disposed||stopTree||account!==props.accountId||rows.length>=treeRowLimit)break
   attempted++
   readingTree=ListLanzouRecycleFolder(account,folder.id,folder.name)
   try{
    const result=await readingTree
    if(disposed||stopTree||account!==props.accountId)break
    rows.push(folder.path+' /')
    for(const file of result.remoteFiles??[]){
     if(rows.length>=treeRowLimit){limited=true;break}
     rows.push(folder.path+' / '+file)
    }
    const childNames=result.remoteFolders??[]
    const childIds=result.remoteFolderIds??[]
    if(result.hasAdditionalEntries&&!childNames.length)unread++
    for(const [index,name] of childNames.entries()){
     if(rows.length>=treeRowLimit){limited=true;break}
     const path=folder.path+' / '+name
     const id=childIds.length===childNames.length?childIds[index]:''
     if(!id?.startsWith('folder:')||seen.has(id)){
      unread++
      rows.push(path+'（子目录未核对）')
      continue
     }
     if(folders.length>=treeFolderLimit){
      limited=true
      rows.push(path+'（达到目录上限）')
      continue
     }
     seen.add(id)
     folders.push({id,name,path})
    }
   }catch{
    if(disposed||stopTree||account!==props.accountId)break
    unread++
    rows.push(folder.path+'（读取失败）')
   }finally{readingTree=undefined}
   treeRows.value=rows.slice()
  }
 }finally{
  scanning.value=false
  if(!disposed&&account===props.accountId){
   const incomplete=unread>0||limited||attempted<folders.length||stopTree
   treeStatus.value=stopTree?'已停止，保留已读取的结果。':incomplete?'清单不完整。':'已读取网页可浏览的目录。'
   if(limited||!stopTree&&attempted<folders.length)treeStatus.value+=' 已达到本次读取上限。'
   if(unread)treeStatus.value+=' '+unread+' 处未能核对。'
  }
 }
}

function stopReadingTree(){
 if(!scanning.value)return
 stopTree=true
 readingTree?.cancel()
}

watch([query,source],()=>{page.value=1})
onMounted(()=>{void nextTick(()=>dialog.value?.focus());void load()})
onUnmounted(()=>{disposed=true;pending?.cancel();stopReadingTree()})
</script>

<template>
 <div class="modal-backdrop" @click.self="emit('close')">
  <section ref="dialog" tabindex="-1" class="operation-dialog share-dialog recycle-folder-preview" role="dialog" aria-modal="true" aria-labelledby="recycle-folder-title" @keydown.tab="trapDialogFocus" @keydown.esc.prevent="emit('close')">
   <div class="modal-heading"><h2 id="recycle-folder-title">回收站文件夹内容</h2><button class="secondary" @click="emit('close')">关闭</button></div>
   <p class="dialog-location">{{accountName}} / {{current.name}}</p>
   <nav v-if="trail.length>1" class="share-history-actions" aria-label="预览浏览记录">
    <button v-for="(folder,index) in trail.slice(0,-1)" :key="folder.id" type="button" class="secondary" :disabled="busy||scanning" @click="returnToFolder(index)">返回 {{folder.name}}</button>
   </nav>
   <p v-if="busy" role="status">正在读取文件夹内容…</p>
   <p v-else-if="error" class="banner error" role="alert">{{error}}</p>
   <template v-else>
    <div v-if="source==='tree'" class="share-history-actions">
     <button v-if="scanning" class="secondary" @click="stopReadingTree">停止读取</button>
     <button v-else class="secondary" @click="source='remote';query=''">返回当前目录</button>
     <span role="status">{{scanning?'正在读取子目录…':treeStatus}}</span>
    </div>
    <label v-if="source!=='tree'&&original!==null">清单来源<select v-model="source"><option value="remote">当前回收站网页</option><option value="original">移入前本机记录</option></select></label>
    <p v-if="source!=='tree'&&comparison" class="recycle-note">按文件 ID 核对本次网页条目。两边均有 {{comparison.matching}} 项，仅本机记录有 {{comparison.originalOnly}} 项，仅本次网页有 {{comparison.currentOnly}} 项。</p>
    <p v-else-if="source!=='tree'&&original!==null" class="recycle-note">本次网页与本机记录无法完整核对，两份清单可分别查看。</p>
    <p class="recycle-note" v-if="source!=='tree'">{{source==='original'?'显示移入前读取的完整文件名。当前回收站内容以网页为准。':'文件名按蓝奏回收站网页显示，可能被截短。'}}</p>
    <template v-if="source==='remote' && hasAdditionalEntries">
     <div class="share-history-actions"><button v-if="remoteFolderIds.some(Boolean)" class="secondary" @click="readTree">读取子目录清单</button></div>
     <p class="recycle-note">网页含目录或其他条目标记，完整层级和内容请到蓝奏官网核对。</p>
     <template v-if="remoteFolders.length">
      <h3>网页列出的目录名称</h3>
      <ul aria-label="网页列出的目录名称">
       <li v-for="(folder,i) in remoteFolders" :key="i">
        {{folder}}
        <button v-if="canOpenFolder(i)" class="secondary" :aria-label="'查看 '+folder" @click="openFolder(i)">查看内容</button>
       </li>
      </ul>
     </template>
    </template>
    <p v-if="source==='tree'" class="recycle-note">名称按网页显示，可能被截短。</p>
    <p v-if="!names.length&&source==='tree'&&!scanning">没有读到目录或文件。</p>
    <p v-else-if="!names.length&&source!=='tree'">{{source==='original'?'移入前本机记录显示此文件夹没有文件。':hasAdditionalEntries?'本次网页没有列出文件。':'蓝奏网页显示此文件夹没有文件。'}}</p>
    <template v-else-if="names.length">
     <label>{{source==='tree'?'筛选路径':'筛选文件名'}}<input v-model="query" type="search"/></label>
     <p v-if="!visible.length">{{source==='tree'?'没有匹配的路径。':'没有匹配的文件名。'}}</p>
     <ul v-else :aria-label="source==='tree'?'目录清单':'文件清单'"><li v-for="(file,i) in visible" :key="i">{{file}}</li></ul>
     <div class="share-history-actions"><button class="secondary" :disabled="page<=1" @click="page--">上一页</button><span>第 {{page}} / {{pageCount}} 页 · 显示 {{filtered.length}} 项</span><button class="secondary" :disabled="page>=pageCount" @click="page++">下一页</button></div>
    </template>
   </template>
   <button v-if="error" class="secondary" @click="load">重新读取</button>
  </section>
 </div>
</template>

<style scoped>
.recycle-folder-preview ul{max-height:36vh;overflow:auto;overflow-wrap:anywhere}
</style>
