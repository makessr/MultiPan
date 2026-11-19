export type UploadFilter='all'|'active'|'attention'|'done'
export type UploadSort='newest'|'oldest'|'name'|'attention'
type UploadRow={status:string;kind:string;file:{name:string;relativePath?:string};folder:string;folderDisplay?:string}
const names=new Intl.Collator('zh-CN',{numeric:true,sensitivity:'base'})
const ranks:Record<string,number>={failed:0,paused:1,running:2,queued:3,success:4}

export function uploadCounts(tasks:readonly UploadRow[]){
 const counts={all:tasks.length,active:0,attention:0,done:0}
 for(const t of tasks){
  if(t.status==='running'||t.status==='queued')counts.active++
  else if(t.status==='failed'||t.status==='paused')counts.attention++
  else if(t.status==='success')counts.done++
 }
 return counts
}

export function filterUploadTasks<T extends UploadRow>(tasks:readonly T[],status:UploadFilter,kind:string,query:string,sort:UploadSort='newest'):T[]{
 const search=query.trim().toLocaleLowerCase('zh-CN')
 return tasks.map((t,index)=>({t,index})).filter(({t})=>
  (status==='all'||(status==='active'?(t.status==='running'||t.status==='queued'):status==='attention'?(t.status==='failed'||t.status==='paused'):t.status==='success'))&&
  (!kind||t.kind===kind)&&
  (!search||[t.file.relativePath||t.file.name,t.folderDisplay||t.folder].some(text=>text.toLocaleLowerCase('zh-CN').includes(search)))
 ).sort((a,b)=>sort==='oldest'?a.index-b.index:sort==='name'?names.compare(a.t.file.relativePath||a.t.file.name,b.t.file.relativePath||b.t.file.name)||b.index-a.index:sort==='attention'?(ranks[a.t.status]??5)-(ranks[b.t.status]??5)||b.index-a.index:b.index-a.index).map(({t})=>t)
}

export function uploadTaskReport(t:UploadRow&{sent:number;file:{name:string;size:number};message?:string;lastAttempt?:string},version:string,provider:string,status:string){
 return ['MultiPan '+version,'网盘：'+provider,'文件：'+t.file.name,'目标目录：'+(t.folderDisplay||t.folder),'状态：'+status,'已传输：'+t.sent+' / '+t.file.size+' 字节',...(t.lastAttempt?['最近尝试：'+t.lastAttempt]:[]),...(t.message?['说明：'+t.message]:[])].join('\n')
}

export function uploadSourceKey(f:{path:string;relativePath?:string;directory?:boolean}){return JSON.stringify([f.path.replaceAll('/','\\').toLowerCase(),(f.relativePath??'').toLowerCase(),!!f.directory])}

type GroupRow=UploadRow&{id:string;batchId?:string;accountId:string;sent:number;file:{name:string;relativePath?:string;size:number;directory?:boolean}}
export function uploadGroupKey(t:GroupRow){return JSON.stringify([t.batchId||t.id,t.accountId,t.folder,t.file.relativePath?.split('/')[0]||''])}
export function uploadGroups<T extends GroupRow>(tasks:readonly T[]){
 type Group={key:string;title:string;kind:string;folder:string;tasks:T[];files:number;directories:number;totalBytes:number;sentBytes:number;success:number;failed:number;paused:number;active:number;percent:number;complete:boolean}
 const groups=new Map<string,Group>()
 for(const t of tasks){
  const key=uploadGroupKey(t)
  let g=groups.get(key)
  if(!g){g={key,title:t.file.relativePath?.split('/')[0]||'文件上传',kind:t.kind,folder:t.folderDisplay||t.folder,tasks:[],files:0,directories:0,totalBytes:0,sentBytes:0,success:0,failed:0,paused:0,active:0,percent:0,complete:false};groups.set(key,g)}
  g.tasks.push(t)
  if(t.file.directory)g.directories++
  else{const size=Number.isFinite(t.file.size)?Math.max(0,t.file.size):0;g.files++;g.totalBytes+=size;g.sentBytes+=t.status==='success'?size:Math.min(size,Number.isFinite(t.sent)?Math.max(0,t.sent):0)}
  if(t.status==='success')g.success++
  else if(t.status==='failed')g.failed++
  else if(t.status==='paused')g.paused++
  else if(t.status==='queued'||t.status==='running')g.active++
 }
 return [...groups.values()].reverse().map(g=>{g.complete=g.success===g.tasks.length;g.percent=g.complete?100:Math.min(99,Math.floor(g.totalBytes?g.sentBytes/g.totalBytes*100:g.success/g.tasks.length*100));return g})
}
