<script lang="ts">
import type {SearchPage} from './pageSearch'

export type AccountSearchLocation<T> = {
 accountId:string
 page:number
 entry:T
}
export type AccountSearchSource<T> = {
 id:string
 key?:string
 label:string
 reader:(query:string)=>((page:number)=>Promise<SearchPage<T>&{unverifiedCount?:number}>)
}
</script>

<script setup lang="ts" generic="T extends {name:string}">
import {onUnmounted,ref,shallowRef,watch} from 'vue'
import {createPageSearch} from './pageSearch'
const props=defineProps<{
 sources:AccountSearchSource<T>[]
 scope:string
 blocked?:boolean
 title:string
 searchLabel:string
 returnLabel:string
 note?:string
 allMatches?:boolean
 identity:(entry:T)=>string
 matches:(entry:T,term:string)=>boolean
 describe:(entry:T)=>string|undefined
 openLabel?:(location:AccountSearchLocation<T>)=>string
}>()
const emit=defineEmits<{open:[location:AccountSearchLocation<T>];close:[]}>()
type AccountResult = {
 accountId:string
 sourceKey:string
 label:string
 summary:string
 incomplete:boolean
 locations:AccountSearchLocation<T>[]
}
const query=ref('')
const results=shallowRef<AccountResult[]>([])
const resultPages=ref(new Map<string,number>())
const resultsPerPage=10
const progress=ref('')
const summary=ref('')
const running=ref(false)
const stopping=ref(false)
let generation=0
function resultPage(sourceKey:string){return resultPages.value.get(sourceKey)??1}
function resultPageCount(result:AccountResult){return Math.max(1,Math.ceil(result.locations.length/resultsPerPage))}
function visibleLocations(result:AccountResult){
 const offset=(resultPage(result.sourceKey)-1)*resultsPerPage
 return result.locations.slice(offset,offset+resultsPerPage)
}
function changeResultPage(result:AccountResult,step:number){
 if(running.value||props.blocked||!results.value.includes(result))return
 const page=resultPage(result.sourceKey)+step
 if(page>=1&&page<=resultPageCount(result))resultPages.value.set(result.sourceKey,page)
}

watch(()=>props.scope,()=>{
 generation++
 results.value=[]
 resultPages.value.clear()
 summary.value=''
 progress.value=''
 stopping.value=running.value
})
watch(()=>props.blocked,blocked=>{
 if(blocked&&running.value)stopping.value=true
})
onUnmounted(()=>generation++)

async function search(){
 const input=query.value.trim(),term=input.toLocaleLowerCase()
 if(!term||running.value||props.blocked||!props.sources.length)return
 const sequence=++generation
 const accounts=[...props.sources]
 const active=()=>sequence===generation
 const stopped=()=>stopping.value||!!props.blocked
 results.value=[]
 resultPages.value.clear()
 summary.value=''
 running.value=true
 stopping.value=false
 try{
  for(const account of accounts){
   if(!active()||stopped())break
   const result:AccountResult={accountId:account.id,sourceKey:account.key??account.id,label:account.label,summary:'',incomplete:false,locations:[]}
   const read=account.reader(input)
   let unverified=0
   const scan=createPageSearch(
    async page=>{
     progress.value=`正在查找 ${account.label} · 第 ${page} 页`
     const listed=await read(page)
     unverified+=listed.unverifiedCount??0
     return listed
    },
    row=>props.matches(row,term),
    props.identity,
    props.allMatches?'all':'first',
   )
   const outcome=await scan(active,stopped,(page,entries)=>{
    result.locations.push(...entries.map(entry=>({accountId:account.id,page,entry})))
   })
   if(!active()||!outcome)break
   result.summary=outcome.summary+(unverified?' '+unverified+' 条未返回链接，结果可能不完整。':'')
   result.incomplete=outcome.incomplete||unverified>0
   results.value=[...results.value,result]
  }
  if(active()){
   const matchedAccounts=new Set(results.value.filter(result=>result.locations.length).map(result=>result.accountId))
   const listLabel=new Set(accounts.map(account=>account.id)).size===accounts.length?'账号':'列表'
   const progressText=`已查看 ${results.value.length} / ${accounts.length} 个${listLabel}，${matchedAccounts.size} 个账号有匹配。`
   const status=stopped()?'搜索已停止，结果可能不完整。':
    results.value.some(result=>result.incomplete)?'部分账号结果不完整。':''
   summary.value=status+progressText
  }
 }finally{
  progress.value=''
  running.value=false
  stopping.value=false
 }
}
</script>

<template>
 <section class="share-history-card" :aria-label="title">
  <form class="share-history-actions" @submit.prevent="search">
   <label>{{searchLabel}} <input v-model="query" type="search" :disabled="running||blocked" required/></label>
   <slot name="options" :disabled="running||!!blocked"/>
   <button class="primary" type="submit" :disabled="running||blocked||!query.trim()||!sources.length">查找全部账号</button>
   <button v-if="running" class="secondary" type="button" :disabled="stopping" @click="stopping=true">停止搜索</button>
   <button class="secondary" type="button" :disabled="running" @click="emit('close')">{{returnLabel}}</button>
  </form>
  <p>{{allMatches?'每个列表最多查看 10 页或 500 条。':'每个账号只显示首个匹配项，最多查看 10 页或 500 条。'}}{{note}}</p>
  <p v-if="progress" role="status">{{stopping?'当前读取结束后停止…':progress}}</p>
  <p v-else-if="summary" role="status">{{summary}}</p>
  <ul v-if="results.length" :class="allMatches?'account-search-results':'recycle-batch-preview'" aria-label="各账号查找结果">
   <li v-for="result in results" :key="result.sourceKey">
    <strong>{{result.label}}</strong>
    <p>{{result.summary}}</p>
    <ul v-if="result.locations.length" class="account-search-matches" aria-label="匹配项目">
     <li v-for="location in visibleLocations(result)" :key="identity(location.entry)" class="share-history-actions">
      <span>{{location.entry.name}}<template v-if="describe(location.entry)"> · {{describe(location.entry)}}</template></span>
      <button class="secondary" :disabled="running||blocked" @click="emit('open',location)">
       {{openLabel?.(location)??'打开该账号列表第 '+location.page+' 页'}}
      </button>
     </li>
    </ul>
    <div v-if="resultPageCount(result)>1" class="share-history-actions" role="group" :aria-label="result.label+'结果分页'">
     <button class="secondary" :disabled="running||blocked||resultPage(result.sourceKey)===1" @click="changeResultPage(result,-1)">上一页</button>
     <span role="status">{{resultPage(result.sourceKey)}} / {{resultPageCount(result)}}</span>
     <button class="secondary" :disabled="running||blocked||resultPage(result.sourceKey)===resultPageCount(result)" @click="changeResultPage(result,1)">下一页</button>
    </div>
   </li>
  </ul>
 </section>
</template>
<style scoped>
.account-search-results{list-style:none;margin:0;padding:0}
.account-search-results>li{padding:14px 0;border-top:1px solid var(--line)}
.account-search-results>li>p{margin:4px 0 8px;color:var(--muted)}
.account-search-matches{list-style:none;margin:0;padding:0}
.account-search-matches li{padding:6px 0;overflow-wrap:anywhere}
</style>
