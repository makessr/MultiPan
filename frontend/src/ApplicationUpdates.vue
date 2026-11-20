<script setup lang="ts">
import {onMounted,onUnmounted,ref} from 'vue'
import {Events} from '@wailsio/runtime'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {ApplicationUpdateInfo} from '../bindings/multipan/internal/desktop/models'

const props=defineProps<{blocked?:boolean}>()
const info=ref<ApplicationUpdateInfo|null>(null)
const running=ref<'check'|'download'|'install'|''>('')
const cancelRequested=ref(false)
const error=ref('')
const message=ref('')
const savedPath=ref('')
const savedVersion=ref('')
const savingPreference=ref(false)
let offUpdate:(()=>void)|undefined

async function refresh(){
 try{info.value=await api.ApplicationUpdateChannel()}
 catch(e){error.value=String(e)}
}

onMounted(()=>{
 offUpdate=Events.On('multipan:application-update-checked',()=>{if(!running.value){message.value='';void refresh()}})
 void refresh()
})
onUnmounted(()=>offUpdate?.())

async function setAutomatic(event:Event){
 if(!info.value?.configured||props.blocked||savingPreference.value)return
 const input=event.target as HTMLInputElement
 savingPreference.value=true
 error.value=''
 try{
  await api.SetAutomaticApplicationUpdateChecks(input.checked)
  await refresh()
 }catch(e){
  input.checked=info.value?.automatic??false
  error.value=String(e)
 }finally{savingPreference.value=false}
}

async function check(){
 if(!info.value?.configured||running.value||props.blocked)return
 running.value='check'
 error.value='';message.value=''
 info.value={...info.value,version:undefined,asset:undefined}
 try{
  info.value=await api.CheckApplicationUpdate()
  if(!info.value.version)message.value='当前版本与发布源一致。'
 }catch(e){await refresh();error.value=String(e)}
 finally{running.value=''}
}

async function download(){
 const version=info.value?.version
 if(!version||running.value||props.blocked)return
 running.value='download'
 cancelRequested.value=false
 error.value='';message.value=''
 try{
  const result=await api.DownloadApplicationUpdate(version)
  if(result.cancelled)message.value='已取消下载。'
  else if(result.path){savedPath.value=result.path;savedVersion.value=version}
 }catch(e){error.value=String(e)}
 finally{running.value='';cancelRequested.value=false}
}

async function openInstaller(){
 const version=info.value?.version
 if(!savedPath.value||!version||version!==savedVersion.value||running.value||props.blocked)return
 running.value='install'
 error.value='';message.value=''
 try{
  if(await api.OpenApplicationUpdateInstaller(version)){
   message.value='安装器已打开。准备安装时，请从托盘退出 MultiPan，再回到安装器确认。'
  }
 }catch(e){error.value=String(e)}
 finally{running.value=''}
}

async function cancel(){
 if(running.value!=='download'||cancelRequested.value)return
 cancelRequested.value=true
 try{await api.CancelApplicationUpdate()}
 catch(e){error.value=String(e);cancelRequested.value=false}
}
</script>

<template>
 <section class="share-history-card transfer-settings" aria-labelledby="app-update-title">
  <h2 id="app-update-title">应用更新</h2>
  <p v-if="info?.currentVersion">当前版本 {{info.currentVersion}}</p>
  <p v-if="info && !info.configured">此构建尚未配置可信更新源。</p>
  <template v-if="info?.configured">
   <label class="tray-setting"><input type="checkbox" :checked="info.automatic" :disabled="!!running||blocked||savingPreference" @change="setAutomatic"/>运行期间自动检查更新</label>
   <p>启用后，程序会在本次运行中每隔至少 24 小时检查一次。安装包仍由你选择下载。</p>
   <div class="share-history-actions">
    <button class="secondary" :disabled="!!running||blocked" @click="check">检查更新</button>
    <button v-if="info.version" class="primary" :disabled="!!running||blocked" @click="download">下载已核对的安装包</button>
    <button v-if="savedPath&&savedVersion===info.version" class="secondary" :disabled="!!running||blocked" @click="openInstaller">打开更新安装器…</button>
    <button v-if="running==='download'" class="secondary" :disabled="cancelRequested" @click="cancel">{{cancelRequested?'正在取消…':'取消下载'}}</button>
   </div>
   <p v-if="info.version">可用版本 {{info.version}} · {{info.asset}}</p>
   <p v-if="info.lastCheck">上次检查 {{new Date(info.lastCheck).toLocaleString()}}</p>
   <p v-if="info.checkError&&!error" class="banner error" role="alert">{{info.checkError}}</p>
  </template>
  <p v-if="running" role="status">{{running==='check'?'正在核对发布清单…':running==='install'?'确认后将重新核对安装包并打开安装器。':'选择保存目录后，程序将下载并核对安装包摘要和 Windows 数字签名。'}}</p>
  <p v-if="message" role="status">{{message}}</p>
  <p v-if="savedPath&&savedVersion===info?.version" role="status" class="transfer-local-path">安装包摘要和 Windows 数字签名已核对，已保存到 {{savedPath}}</p>
  <p v-if="error" class="banner error" role="alert">{{error}}</p>
 </section>
</template>
