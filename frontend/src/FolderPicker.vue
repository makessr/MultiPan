<script setup lang="ts">
import {trapDialogFocus} from './dialogFocus'
import {ref,onMounted,onUnmounted,computed} from 'vue'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {CloudFolder,FolderPage} from '../bindings/multipan/internal/transfer/models'
const props=defineProps<{accountId:string;accountName:string;initialFolder:string;kind:string;purpose?:'upload'|'move'|'copy'|'source'|'restore';sourceName?:string;sourceNames?:string[];forbiddenRoots?:string[];initialBreadcrumbs?:CloudFolder[];requiredFolderId?:string;excludedFolderIds?:string[];directCopy?:boolean;snapshotCopy?:boolean;snapshotOnly?:boolean}>()
function forbidden(id:string){return (props.forbiddenRoots??[]).some(root=>id===root||id.startsWith(root+'/'))}
const verb=computed(()=>props.purpose==='copy'?'复制':'移动')
const rootID=computed(()=>props.kind==='guangya'?'root':props.kind==='ctfile'?'d0':['baidu','ctdav','webdav'].includes(props.kind)?'/':props.kind!=='lanzou'?'0':'-1')
const emit=defineEmits<{close:[];selected:[id:string,path:string,trail:CloudFolder[],directCopy?:boolean]}>()
const page=ref<FolderPage|null>(null),busy=ref(false),saving=ref(false),error=ref(''),query=ref('')
const sortOrder=ref<'asc'|'desc'>('asc')
const nameCollator=new Intl.Collator('zh-CN',{numeric:true,sensitivity:'base'})
let request=0,disposed=false
const entries=computed(()=>(page.value?.folders??[]).filter(f=>f.name.toLocaleLowerCase().includes(query.value.toLocaleLowerCase())).sort((a,b)=>nameCollator.compare(a.name,b.name)*(sortOrder.value==='asc'?1:-1)))
const path=computed(()=>(page.value?.breadcrumbs??[]).map(f=>f.name).join(' / '))
async function load(id:string,crumbs?:CloudFolder[]){const ticket=++request;busy.value=true;error.value='';query.value='';try{const next=await api.ListCloudFolders(props.accountId,id);if(disposed||ticket!==request)return;if(['quark','pan123','ctfile','guangya'].includes(props.kind)&&crumbs)next.breadcrumbs=crumbs;page.value=next}catch(e){if(!disposed&&ticket===request)error.value=e instanceof Error?e.message:String(e)}finally{if(!disposed&&ticket===request)busy.value=false}}
const canSelect=computed(()=>!!page.value&&!busy.value&&!saving.value&&!error.value&&!forbidden(page.value.currentId)&&
 !props.excludedFolderIds?.includes(page.value.currentId)&&
 (!props.requiredFolderId||page.value.currentId===props.requiredFolderId)&&
 (!(props.purpose==='move'||props.purpose==='copy')||page.value.currentId!==props.initialFolder))
async function select(direct=false){
 if(!page.value||!canSelect.value||direct&&!props.directCopy)return
 saving.value=true
 try{
  const id=page.value.currentId,trail=[...(page.value.breadcrumbs??[])]
  if(direct)emit('selected',id,path.value,trail,true)
  else emit('selected',id,path.value,trail)
 }catch(e){error.value=e instanceof Error?e.message:String(e)}
 finally{saving.value=false}
}
function go(folder:CloudFolder){if(forbidden(folder.id))return;const old=page.value?.breadcrumbs??[];const index=old.findIndex(f=>f.id===folder.id);load(folder.id,index>=0?old.slice(0,index+1):[...old,folder])}
onMounted(()=>load(props.initialFolder||rootID.value,props.initialBreadcrumbs));onUnmounted(()=>{disposed=true;request++})
</script>
<template>
<div class="modal-backdrop folder-backdrop" @click.self="!saving&&emit('close')"><section class="folder-modal" @keydown.esc.prevent="!saving&&emit('close')" role="dialog" aria-modal="true" @keydown.tab="trapDialogFocus" aria-labelledby="folder-title">
 <div class="modal-heading"><div><h2 id="folder-title">{{purpose==='restore'?'选择还原目录':purpose==='source'?'选择来源目录':(purpose==='move'||purpose==='copy')?'选择'+verb+'目标':'选择保存目录'}}</h2><p><template v-if="!purpose||purpose==='upload'||purpose==='source'||purpose==='restore'">{{accountName}}{{purpose==='restore'?'':' · '}}</template><template v-if="purpose!=='restore'">{{kind==='guangya'?'光鸭云盘':kind==='ctdav'?'诚通 WebDAV':kind==='webdav'?'WebDAV':kind==='ctfile'?'诚通网盘':kind==='baidu'?'百度网盘':kind==='pan123'?'123 云盘':kind==='quark'?'夸克网盘':'蓝奏云'}}</template></p><p v-if="(purpose==='move'||purpose==='copy')" class="move-source-name">{{verb}}项目：{{sourceName}}</p></div><button class="icon-button" aria-label="关闭目录选择" :disabled="saving" @click="emit('close')">×</button></div>
<p v-if="purpose==='copy'" class="cloud-folder-move-help">{{kind==='lanzou'?'每批最多 20 项。单文件最多 200 MB，单个文件夹内文件合计最多 400 MB。副本放入独立子目录。':kind==='quark'?'通过本机中转复制。源项目保留，进度可在网盘中转查看。':kind==='pan123'?'最多 50 项，可混选文件和文件夹。每项复制到目标下的独立子目录，源项目保留。':kind==='guangya'?'最多 50 项，可混选文件和文件夹。单文件或单文件夹内文件合计最多 2 GiB。副本放入独立子目录，内容核对使用网盘下载额度。':'复制会保留源项目，同名目标不覆盖；文件夹内部内容请另行核对。'}}</p>
 <p v-if="directCopy" class="cloud-folder-move-help">{{snapshotCopy?'经本机中转直接复制到目标目录，遇到同名项目时停止。':forbiddenRoots?.length?'直接复制会在目标下创建同名副本，遇到同名项目时停止。':'直接复制使用秒传。发现目标同名或秒传未命中时停止。'}}</p>
 <ul v-if="(purpose==='move'||purpose==='copy')&&(sourceNames?.length??0)>1" class="folder-move-preview"><li v-for="(name,i) in sourceNames" :key="i">{{name}}</li></ul><p v-if="(purpose==='move'||purpose==='copy')&&(sourceNames?.length??0)>1&&!(purpose==='copy'&&kind==='quark')" class="cloud-folder-move-help">逐项{{verb}}；整批预检查通过后开始。首项失败或结果待确认时停止后续，已成功项目不回滚。</p>
 <nav class="folder-crumbs" aria-label="当前目录"><button v-if="!page" @click="load(rootID)" :disabled="busy">根目录</button><template v-for="(crumb,i) in page?.breadcrumbs??[]" :key="crumb.id"><span v-if="i">/</span><button :disabled="busy||saving" @click="go(crumb)">{{crumb.name}}</button></template></nav>
 <div class="folder-toolbar"><input v-model="query" placeholder="筛选当前目录中的文件夹" aria-label="筛选文件夹" :disabled="busy||saving"/><select v-model="sortOrder" aria-label="按名称排序" :disabled="saving"><option value="asc">名称升序 ↑</option><option value="desc">名称降序 ↓</option></select><button class="secondary" :disabled="busy||saving" @click="load(page?.currentId??initialFolder??rootID,page?.breadcrumbs??undefined)">刷新</button></div>
 <p v-if="error" class="banner error" role="alert">{{error}}</p>
 <p v-if="requiredFolderId&&page?.currentId!==requiredFolderId" class="cloud-folder-move-help">请选择已还原文件夹的原所在目录（ID {{requiredFolderId}}）。</p>
 <div class="folder-entries" :aria-busy="busy"><p v-if="busy" class="folder-empty" role="status">正在读取网盘目录…</p><p v-else-if="!error&&!entries.length" class="folder-empty">{{query?'没有匹配的文件夹':'此目录没有子文件夹，可以直接选用当前目录。'}}</p><button v-for="f in busy?[]:entries" :key="f.id" class="folder-entry" @click="go(f)" :disabled="saving||forbidden(f.id)"><span class="folder-glyph">▰</span><span>{{f.name}}</span><span class="folder-chevron">›</span></button></div>
 <div class="folder-selection"><span>{{purpose==='restore'?'还原位置':purpose==='source'?'来源位置':(purpose==='move'||purpose==='copy')?'目标位置':'保存位置'}}</span><strong>{{busy?'正在读取…':path||'尚未选择'}}</strong><small v-if="purpose==='restore'&&page&&excludedFolderIds?.includes(page.currentId)">此目录是所选项目的原位置。请选择其他目录。</small><small v-if="(purpose==='move'||purpose==='copy')&&page?.currentId===initialFolder">请选择其他目录。{{purpose==='copy'?'复制保留源项目，不修改默认上传位置。':forbiddenRoots?.length?'相关上传位置将随文件夹路径更新。':'移动不会修改默认上传位置。'}}</small></div>
 <div class="modal-actions"><button class="secondary" :disabled="busy||saving||(page?.currentId===rootID)" @click="load(rootID)">返回根目录</button><button class="secondary" :disabled="saving" @click="emit('close')">取消</button><button v-if="directCopy" :class="snapshotOnly?'primary':'secondary'" :disabled="!canSelect" @click="select(true)">{{snapshotCopy?'经中转复制到此目录':'直接复制到此目录'}}</button><button v-if="!snapshotOnly" class="primary" :disabled="!canSelect" @click="select()">{{saving?'正在处理…':purpose==='copy'&&directCopy?'复制到独立子目录':(purpose==='move'||purpose==='copy')?verb+'到此目录':'使用此目录'}}</button></div>
</section></div>
</template>


