export type SearchPage<T> = {page:number;items:T[]|null;hasMore:boolean;incomplete?:boolean}

export function createPageSearch<T>(
 read:(page:number)=>Promise<SearchPage<T>>,
 match:(row:T)=>boolean,
 identity?:(row:T)=>string,
 mode:'all'|'first'='all',
){
 const pageLimit=10, rowLimit=500
 const hits=new Map<number,number>()
 const seen=new Set<string>()
 let target=1, checkedPages=0, checkedRows=0, total=0
 let pending:{page:SearchPage<T>;items:T[];offset:number}|undefined
 let finished=false

 return async function scan(active:()=>boolean,stopped:()=>boolean,onMatches?:(page:number,rows:T[],incomplete:boolean)=>void){
  let pagesThisRound=0, rowsThisRound=0, reason=''
  try{
   while(!finished&&pagesThisRound<pageLimit&&rowsThisRound<rowLimit){
    if(!active())return null
    if(stopped())break
    if(!pending){
     const page=await read(target)
     if(!active())return null
     if(page.page!==target||(!Array.isArray(page.items)&&(page.hasMore||page.items!==null))){
      throw Error('网盘返回的页码或列表不一致')
     }
     const items=page.items??[]
     if(page.hasMore&&items.length===0)throw Error('网盘返回了无记录的中间页')
     if(identity){
      const ids=items.map(identity)
      if(ids.some(id=>!id||seen.has(id))||new Set(ids).size!==ids.length){
       throw Error('列表出现重复或缺失的项目标识')
      }
      ids.forEach(id=>seen.add(id))
     }
     pending={page,items,offset:0}
     checkedPages++
    }

    if(mode==='first'&&stopped())break
    let rows=pending.items.slice(pending.offset,pending.offset+rowLimit-rowsThisRound)
    if(mode==='first'){
     const index=rows.findIndex(match)
     if(index>=0)rows=rows.slice(0,index+1)
    }
    const matched=rows.filter(match)
    if(matched.length){
     hits.set(target,(hits.get(target)??0)+matched.length)
     onMatches?.(target,matched,!!pending.page.incomplete)
    }
    total+=matched.length
    pending.offset+=rows.length
    pagesThisRound++
    rowsThisRound+=rows.length
    checkedRows+=rows.length

    if(mode==='first'&&matched.length){
     if(pending.page.incomplete)reason='命中页由网盘标记为不完整'
     finished=true
     break
    }
    if(pending.offset<pending.items.length)break
    const {page}=pending
    pending=undefined
    target++
    if(page.incomplete){
     reason='网盘返回了不完整的列表'
     finished=true
    }else if(!page.hasMore){
     finished=true
    }
   }
  }catch(e){
   reason='读取中断。'+String(e)
   finished=true
  }
  if(!active())return null
  if(!finished)reason=stopped()?'搜索已停止':'达到本轮 10 页或 500 条上限'
  const summary='已检查 '+checkedPages+' 页、'+checkedRows+' 条，匹配 '+total+' 条'
  return {
   hits:Array.from(hits,([page,count])=>({page,count})),
   summary:reason?summary+'。'+reason+'，结果可能不完整。':summary+'。',
   canContinue:!finished,
   incomplete:!!reason,
  }
 }
}
