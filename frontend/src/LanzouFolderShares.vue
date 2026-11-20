<script setup lang="ts">
import {computed,nextTick,onUnmounted,ref,watch} from 'vue'
import {Browser} from '@wailsio/runtime'
import BatchManagedShareReadDialog from './BatchManagedShareReadDialog.vue'
import BatchShareExpiryDialog from './BatchShareExpiryDialog.vue'
import BatchShareDialog from './BatchShareDialog.vue'
import FolderPicker from './FolderPicker.vue'
import GlobalSearch from './GlobalSearch.vue'
import ShareDialog from './ShareDialog.vue'
import {shareAccountLabel,shareWebsite} from './shareAccounts'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {Account,CloudEntry,CloudFolder,CloudSearchResult,LanzouShareLocation} from '../bindings/multipan/internal/transfer/models'

const props=defineProps<{accounts:Account[];blocked?:boolean;refreshKey?:string;initialLocation?:LanzouShareLocation|null}>()
const emit=defineEmits<{mutation:[active:boolean]}>()
const accounts=computed(()=>props.accounts.filter(a=>a.kind==='lanzou'))
const accountId=ref(props.initialLocation?.accountId??accounts.value[0]?.id??'')
let initialLocation=props.initialLocation
const account=computed(()=>accounts.value.find(a=>a.id===accountId.value))
const folderId=ref('-1'),folderPath=ref('根目录'),picker=ref(false)
const view=ref<'directory'|'search'|'candidates'>('directory')
const root:CloudFolder={id:'-1',name:'根目录'}
const trail=ref<CloudFolder[]>([root])
const entries=ref<CloudEntry[]>([]),busy=ref(false),error=ref(''),query=ref(''),page=ref(1)
const locatedEntry=ref<CloudEntry|null>(null)
const batch=ref<{entries:CloudEntry[];initialSelection:CloudEntry[]}|null>(null)
type ReadTarget={id:string;name:string;sourcePath:string;parentId:string;entry:CloudEntry;canEdit:true}
const readQueue=ref<ReadTarget[]>([])
const queuedBatch=ref<{accountId:string;accountName:string;targets:ReadTarget[];editPassword:boolean}|null>(null)
const single=ref<{accountId:string;accountName:string;parentId:string;entry:CloudEntry;editPassword:boolean;sourcePath?:string}|null>(null)
const filtered=computed(()=>entries.value.filter(row=>row.name.toLocaleLowerCase().includes(query.value.trim().toLocaleLowerCase())))
const pageCount=computed(()=>Math.max(1,Math.ceil(filtered.value.length/50)))
const rows=computed(()=>filtered.value.slice((page.value-1)*50,page.value*50))
const locked=computed(()=>props.blocked||busy.value||picker.value||!!batch.value||!!single.value||!!queuedBatch.value)
const websiteError=ref('')
let generation=0,disposed=false,deferredLoad=false
let searchActionTrigger:HTMLElement|null=null

function accountLabel(a:Account){return shareAccountLabel(a,accounts.value)}
async function openWebsite(){
 const url=shareWebsite('lanzou')
 if(disposed||locked.value||!account.value||!url)return
 websiteError.value=''
 try{await Browser.OpenURL(url)}
 catch{if(!disposed)websiteError.value='无法打开蓝奏官网'}
}

async function load(focusEntry?:CloudEntry){
 const a=account.value,id=folderId.value
 const request=++generation
 entries.value=[];locatedEntry.value=null;page.value=1;error.value=''
 deferredLoad=!!a&&!!props.blocked
 if(!a||deferredLoad){busy.value=false;return}
 busy.value=true
 try{
  const result=await api.ListCloudFiles(a.id,id)
  if(disposed||request!==generation||a.id!==accountId.value)return
  if(result.currentId!==id)throw Error('蓝奏返回的目录与所选目录不一致')
  const crumbs=result.breadcrumbs??[]
  if(crumbs[0]?.id===root.id&&crumbs.at(-1)?.id===id&&new Set(crumbs.map(row=>row.id)).size===crumbs.length&&(!focusEntry||crumbs.at(-1)?.name!=='目录 '+id)){
   trail.value=crumbs
   folderPath.value=crumbs.map(row=>row.name).join(' / ')
  }
  entries.value=result.entries??[]
  if(focusEntry){
   const found=entries.value.filter(row=>row.id===focusEntry.id&&row.folder===focusEntry.folder)
   if(found.length!==1||found[0]?.name!==focusEntry.name){
    error.value='搜索结果已变化，请根据当前目录重新选择项目。'
   }else{
    locatedEntry.value=found[0]
    page.value=Math.floor(entries.value.indexOf(found[0])/50)+1
   }
  }
 }catch(e){
  if(!disposed&&request===generation)error.value=String(e)
 }finally{
  if(!disposed&&request===generation)busy.value=false
 }
}

function searchResultTrail(row:CloudSearchResult){
 if(view.value==='directory'||!account.value||row.accountId!==accountId.value)return
 const crumbs=row.trail??[]
 if(crumbs[0]?.id!==root.id||crumbs.at(-1)?.id!==row.parentId||new Set(crumbs.map(crumb=>crumb.id)).size!==crumbs.length)return
 return crumbs
}

async function openSearchResult(row:CloudSearchResult){
 const crumbs=searchResultTrail(row)
 if(locked.value||!crumbs)return
 view.value='directory'
 folderId.value=row.parentId
 trail.value=crumbs
 folderPath.value=crumbs.map(crumb=>crumb.name).join(' / ')
 query.value=''
 await load(row.entry)
}

function chooseFolder(id:string,path:string,crumbs:CloudFolder[]=[]){
 picker.value=false
 if(locked.value||!account.value)return
 trail.value=crumbs[0]?.id===root.id&&crumbs.at(-1)?.id===id?crumbs:[root]
 folderId.value=id;folderPath.value=path||'目录 '+id;query.value=''
 void load()
}

function goToFolder(id:string){
 if(locked.value||!account.value||id===folderId.value)return
 const index=trail.value.findIndex(row=>row.id===id)
 const child=entries.value.find(row=>row.folder&&row.id===id)
 if(index<0&&!child)return
 const next=index>=0?trail.value.slice(0,index+1):[...trail.value,{id:child!.id,name:child!.name}]
 chooseFolder(id,next.map(row=>row.name).join(' / '),next)
}

function openLinks(){
 if(locked.value||!account.value||!rows.value.length)return
 batch.value={entries:rows.value,initialSelection:[]}
 emit('mutation',true)
}

function queueKey(entry:CloudEntry,parentId=folderId.value){
 return JSON.stringify([parentId,entry.folder,entry.id])
}

function toggleQueued(entry:CloudEntry,checked:boolean){
 if(locked.value||!account.value||!rows.value.includes(entry))return
 updateQueue(entry,folderId.value,folderPath.value,checked)
}

function toggleSearchQueued(row:CloudSearchResult,checked:boolean,disabled:boolean){
 const crumbs=searchResultTrail(row)
 if(disabled||locked.value||!crumbs)return
 updateQueue(row.entry,row.parentId,crumbs.map(crumb=>crumb.name).join(' / '),checked)
}

function unqueuedCandidates(rows:CloudSearchResult[]){
 const queued=new Set(readQueue.value.map(row=>row.id))
 return rows.filter(row=>searchResultTrail(row)&&!queued.has(queueKey(row.entry,row.parentId)))
}

function canQueueCandidatePage(rows:CloudSearchResult[]){
 const count=unqueuedCandidates(rows).length
 return count>0&&readQueue.value.length+count<=50
}

function queueCandidatePage(rows:CloudSearchResult[],disabled:boolean){
 if(disabled||locked.value||view.value!=='candidates'||!canQueueCandidatePage(rows))return
 for(const row of unqueuedCandidates(rows))toggleSearchQueued(row,true,false)
}

function updateQueue(entry:CloudEntry,parentId:string,sourcePath:string,checked:boolean){
 const id=queueKey(entry,parentId)
 if(!checked){removeQueued(id);return}
 if(readQueue.value.some(row=>row.id===id))return
 if(readQueue.value.length>=50)return
 readQueue.value.push({id,name:entry.name,sourcePath,parentId,entry:{...entry},canEdit:true})
}

function removeQueued(id:string){
 if(locked.value)return
 readQueue.value=readQueue.value.filter(row=>row.id!==id)
}

function clearQueue(){
 readQueue.value=[]
}

function refreshFolder(){
 if(locked.value)return
 clearQueue()
 void load()
}

function openQueue(editPassword=false){
 const a=account.value
 if(locked.value||!a||!readQueue.value.length)return
 queuedBatch.value={accountId:a.id,accountName:accountLabel(a),targets:[...readQueue.value],editPassword}
 emit('mutation',true)
}

function queuedTarget(id:string){
 const current=queuedBatch.value
 const target=current?.targets.find(row=>row.id===id)
 if(!current||!target||current.accountId!==accountId.value||disposed)throw Error('读取清单的蓝奏账号或项目已变化')
 return {current,target}
}

async function locateQueuedShare(id:string){
 const batch=queuedBatch.value
 const target=batch?.targets.find(row=>row.id===id)
 if(disposed||props.blocked||!batch||batch.editPassword||batch.accountId!==accountId.value||!target)return
 closeLinks()
 await loadShareDirectory(target.parentId,target.entry,target.sourcePath)
}

async function loadShareDirectory(parentId:string,entry:CloudEntry,sourcePath=''){
 view.value='directory'
 folderId.value=parentId
 folderPath.value=sourcePath||(parentId===root.id?root.name:'目录 '+parentId)
 trail.value=[root]
 query.value=''
 await load(entry)
}

async function readQueuedLink(id:string){
 const {current,target}=queuedTarget(id)
 return api.ReadLanzouDirectoryShare(current.accountId,target.parentId,target.entry.id,target.entry.name,target.entry.folder)
}
async function readQueuedLinks(ids:string[]){
 const targets=ids.map(queuedTarget)
 const first=targets[0]
 if(!first)throw Error('蓝奏分享读取清单已变化')
 return api.ReadLanzouDirectoryShares(first.current.accountId,targets.map(({target})=>({
  parentId:target.parentId,entryId:target.entry.id,name:target.entry.name,folder:target.entry.folder,
 })))
}

async function updateQueuedPassword(id:string,original:{url:string;password:string},password:string){
 const {current,target}=queuedTarget(id)
 if(!current.editPassword)throw Error('请先打开批量修改提取码')
 return api.UpdateLanzouDirectorySharePassword(current.accountId,target.parentId,target.entry.id,target.entry.name,target.entry.folder,original.url,original.password,password)
}

async function clearQueuedPassword(id:string,original:{url:string;password:string}){
 const {current,target}=queuedTarget(id)
 if(!current.editPassword)throw Error('请先打开批量修改提取码')
 return api.ClearLanzouDirectorySharePassword(current.accountId,target.parentId,target.entry.id,target.entry.name,target.entry.folder,original.url,original.password)
}

const directoryPassword={read:readQueuedLink,readGroup:readQueuedLinks,update:updateQueuedPassword,clear:clearQueuedPassword}

function openItem(entry:CloudEntry,editPassword=false){
 const a=account.value
 if(locked.value||!a||!rows.value.includes(entry))return
 single.value={accountId:a.id,accountName:accountLabel(a),parentId:folderId.value,entry:{...entry},editPassword}
 emit('mutation',true)
}

function openSearchItem(row:CloudSearchResult,editPassword:boolean,disabled:boolean,event?:Event){
 const a=account.value,crumbs=searchResultTrail(row)
 if(disposed||disabled||locked.value||!a||!crumbs)return
 searchActionTrigger=event&&event.currentTarget instanceof HTMLElement?event.currentTarget:null
 single.value={accountId:a.id,accountName:accountLabel(a),parentId:row.parentId,entry:{...row.entry},editPassword,sourcePath:crumbs.map(crumb=>crumb.name).join(' / ')}
 emit('mutation',true)
}

function closeLinks(restoreFocus=false){
 if(!batch.value&&!single.value&&!queuedBatch.value)return
 batch.value=null
 single.value=null
 queuedBatch.value=null
 const trigger=searchActionTrigger
 searchActionTrigger=null
 emit('mutation',false)
 if(restoreFocus&&trigger)void nextTick(()=>{if(!disposed&&trigger.isConnected)trigger.focus()})
}

watch(query,()=>{page.value=1})
watch(()=>props.blocked,blocked=>{if(!blocked&&deferredLoad)void load()})
watch([accountId,()=>props.refreshKey,()=>JSON.stringify(accounts.value.map(a=>[a.id,a.endpoint,a.username]))],()=>{
 const next=accounts.value.find(a=>a.id===accountId.value)??accounts.value[0]
 if((next?.id??'')!==accountId.value){accountId.value=next?.id??'';return}
 closeLinks()
 clearQueue()
 picker.value=false
 const location=initialLocation
 initialLocation=null
 if(location&&location.accountId===accountId.value){
  void loadShareDirectory(location.parentId,location.entry)
 }else{
  view.value='directory';folderId.value=root.id;folderPath.value=root.name;trail.value=[root];query.value=''
  void load()
 }
},{immediate:true})
onUnmounted(()=>{disposed=true;generation++;closeLinks()})
</script>

<template>
 <section aria-label="蓝奏目录分享">
  
  <div class="share-history-filters">
   <label>蓝奏账号<select v-model="accountId" :disabled="locked"><option v-if="!accounts.length" value="">暂无蓝奏账号</option><option v-for="a in accounts" :key="a.id" :value="a.id">{{accountLabel(a)}}</option></select></label>
   <template v-if="view==='directory'">
    <label>筛选项目<input v-model="query" type="search" :disabled="locked||!account" placeholder="按项目名称筛选"/></label>
    <button class="secondary" :disabled="locked||!account" @click="picker=true">选择目录</button>
    <button class="secondary" :disabled="locked||!account" @click="refreshFolder">刷新目录</button>
   </template>
   <button v-if="view!=='directory'" class="secondary" :disabled="locked" @click="view='directory'">返回目录</button>
   <button class="secondary" :disabled="locked||!account" :aria-pressed="view==='search'" @click="view='search'">搜索蓝奏项目</button>
   <button class="secondary" :disabled="locked||!account" :aria-pressed="view==='candidates'" @click="view='candidates'">跨目录候选清单</button>
   <button class="secondary" :disabled="locked||!account" @click="openWebsite">打开蓝奏官网</button>
  </div>
  <p v-if="websiteError" class="banner error" role="alert">{{websiteError}}</p>
  <details v-if="readQueue.length" class="share-history-card">
   <summary>跨目录读取清单 · {{readQueue.length}} / 50 项</summary>
   <div class="share-history-actions">
    <button class="secondary" :disabled="locked" @click="openQueue()">读取清单链接</button>
    <button class="secondary" :disabled="locked" @click="openQueue(true)">批量修改提取码…</button>
    <button class="secondary" :disabled="locked" @click="clearQueue">清空清单</button>
   </div>
   <ul>
    <li v-for="target in readQueue" :key="target.id">
     {{target.sourcePath}} / {{target.name}} · {{target.entry.folder?'文件夹':'文件'}}
     <button class="text-button" :disabled="locked" :aria-label="'移除 '+target.name" @click="removeQueued(target.id)">移除</button>
    </li>
   </ul>
  </details>
  <p v-if="readQueue.length===50" role="status">读取清单已满，移除项目后可继续添加。</p>
  <GlobalSearch v-if="view!=='directory'&&account" :key="view" :accounts="accounts" :fixed-account-id="account.id"
   :refresh-key="refreshKey" :blocked="locked" :list-lanzou-candidates="view==='candidates'" @open="openSearchResult">
   <template v-if="view==='candidates'" #page-actions="{rows,disabled}">
    <button v-if="rows.length" class="secondary" :disabled="disabled||!canQueueCandidatePage(rows)"
     @click="queueCandidatePage(rows,disabled)">本页未选项加入读取清单</button>
   </template>
   <template #result-actions="{row,disabled}">
    <template v-if="searchResultTrail(row)">
    <label class="share-switch">
     <input type="checkbox"
      :checked="readQueue.some(target=>target.id===queueKey(row.entry,row.parentId))"
      :disabled="disabled||(readQueue.length>=50&&!readQueue.some(target=>target.id===queueKey(row.entry,row.parentId)))"
      :aria-label="'加入读取清单 '+row.entry.name"
      @change="toggleSearchQueued(row,($event.target as HTMLInputElement).checked,disabled)"/>
     加入读取清单
    </label>
    <button type="button" class="secondary" :disabled="disabled" @click="openSearchItem(row,false,disabled,$event)">读取原链接</button>
    <button type="button" class="secondary" :disabled="disabled" @click="openSearchItem(row,true,disabled,$event)">修改提取码</button>
    </template>
   </template>
  </GlobalSearch>
  <template v-else>
  <nav v-if="account" class="folder-crumbs" aria-label="蓝奏分享当前目录">
   <template v-for="(crumb,index) in trail" :key="crumb.id">
    <span v-if="index">/</span><strong v-if="crumb.id===folderId" aria-current="page">{{crumb.name}}</strong>
    <button v-else type="button" :disabled="locked" @click="goToFolder(crumb.id)">{{crumb.name}}</button>
   </template>
   <strong v-if="trail.at(-1)?.id!==folderId" aria-current="page">{{folderPath}}</strong>
  </nav>
  <p v-if="busy" role="status">正在读取蓝奏目录…</p>
  <p v-else-if="error" class="banner error" role="alert">{{error}}</p>
  <p v-else-if="account&&!rows.length" class="share-history-empty">{{entries.length?'目录中没有匹配的项目。':'当前目录没有文件或文件夹。'}}</p>
  <template v-if="rows.length">
   <button class="secondary" :disabled="locked" @click="openLinks()">批量读取本页链接</button>
   <article v-for="row in rows" :key="(row.folder?'folder:':'file:')+row.id" class="share-history-card">
    <div class="share-history-card-heading">
     <div><button v-if="row.folder" type="button" class="cloud-folder-link" :disabled="locked" :aria-label="'进入 '+row.name" @click="goToFolder(row.id)">{{row.name}}</button><strong v-else>{{row.name}}</strong><small>{{row.folder?'文件夹':'文件'}}<template v-if="row===locatedEntry"> · 搜索命中</template></small></div>
     <div class="share-history-actions">
      <label class="share-switch"><input type="checkbox" :checked="readQueue.some(target=>target.id===queueKey(row))" :disabled="locked||(readQueue.length>=50&&!readQueue.some(target=>target.id===queueKey(row)))" :aria-label="'加入读取清单 '+row.name" @change="toggleQueued(row,($event.target as HTMLInputElement).checked)"/>加入读取清单</label>
      <button class="secondary" :disabled="locked" @click="openItem(row)">读取分享链接</button>
      <button class="secondary" :disabled="locked" @click="openItem(row,true)">修改提取码</button>
     </div>
    </div>
   </article>
   <div class="share-history-actions">
    <button class="secondary" :disabled="locked||page<=1" @click="page--">上一页</button>
    <span>第 {{page}} / {{pageCount}} 页 · 共 {{filtered.length}} 项</span>
    <button class="secondary" :disabled="locked||page>=pageCount" @click="page++">下一页</button>
   </div>
  </template>
  </template>
  <FolderPicker v-if="picker&&account" :account-id="account.id" :account-name="accountLabel(account)" :initial-folder="folderId" kind="lanzou" purpose="source" @close="picker=false" @selected="chooseFolder"/>
  <BatchShareDialog v-if="batch&&account" :account-id="account.id" :parent-id="folderId" kind="lanzou" :entries="batch.entries" :initial-selection="batch.initialSelection" @close="closeLinks"/>
  <BatchShareExpiryDialog v-if="queuedBatch?.editPassword" :account-name="queuedBatch.accountName" kind="lanzou" :targets="queuedBatch.targets" :blocked="blocked" :directory-password="directoryPassword" @close="closeLinks"/>
  <BatchManagedShareReadDialog v-else-if="queuedBatch" :account-name="queuedBatch.accountName" kind="lanzou" :targets="queuedBatch.targets" :blocked="blocked" :read-link="readQueuedLink" :read-links="readQueuedLinks" can-locate @locate="locateQueuedShare" @close="closeLinks"/>
  <ShareDialog v-if="single" :account-id="single.accountId" :account-name="single.accountName" :parent-id="single.parentId" kind="lanzou" :entry="single.entry" :edit-password="single.editPassword" :source-path="single.sourcePath" @close="closeLinks(true)"/>
 </section>
</template>
