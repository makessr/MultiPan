export type ResultFilter = 'all' | 'verified' | 'unverified' | 'unfinished'
type Outcome = {status:string;name:string;result?:{verification?:string}}

export function resultCategory(item:Outcome):Exclude<ResultFilter,'all'>{
 return item.status==='saved'?(item.result?.verification==='verified'?'verified':'unverified'):'unfinished'
}

export function resultCounts(items:readonly Outcome[]){
 const counts={all:items.length,verified:0,unverified:0,unfinished:0}
 for(const item of items)counts[resultCategory(item)]++
 return counts
}

export function filterResults<T extends Outcome>(items:readonly T[],filter:ResultFilter,query:string):T[]{
 const search=query.trim().toLocaleLowerCase('zh-CN')
 return items.filter(item=>(filter==='all'||resultCategory(item)===filter)&&(!search||item.name.toLocaleLowerCase('zh-CN').includes(search)))
}
