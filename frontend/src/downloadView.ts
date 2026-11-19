export type DownloadSort='newest'|'oldest'|'name'|'attention'
type Row={kind:string;status:string;input:{name:string};destination:string}
const names=new Intl.Collator('zh-CN',{numeric:true,sensitivity:'base'})
const rank:Record<string,number>={failed:0,paused:1,running:2,queued:3,saved:4}
export function downloadRows<T extends Row>(jobs:readonly T[],status:string,kind:string,query:string,sort:DownloadSort):T[]{
 const text=query.trim().toLocaleLowerCase('zh-CN')
 return jobs.map((job,index)=>({job,index})).filter(({job})=>(status==='all'||job.status===status)&&(!kind||job.kind===kind)&&(!text||[job.input.name,job.destination].some(value=>value.toLocaleLowerCase('zh-CN').includes(text)))).sort((a,b)=>{
  if(sort==='oldest')return a.index-b.index
  if(sort==='name')return names.compare(a.job.input.name,b.job.input.name)||b.index-a.index
  if(sort==='attention')return (rank[a.job.status]??5)-(rank[b.job.status]??5)||b.index-a.index
  return b.index-a.index
 }).map(row=>row.job)
}
export function downloadPage<T>(rows:readonly T[],requested:number,size:number){
 const pageSize=[25,50,100,200].includes(size)?size:50
 const pages=Math.max(1,Math.ceil(rows.length/pageSize))
 const page=Math.max(1,Math.min(pages,Number.isFinite(requested)?Math.floor(requested):1))
 const start=(page-1)*pageSize
 return {page,pages,pageSize,first:rows.length?start+1:0,last:Math.min(start+pageSize,rows.length),items:rows.slice(start,start+pageSize)}
}
