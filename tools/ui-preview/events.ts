const listeners=new Map<string,Set<(event:any)=>void>>();
export const Events={On(name:string,fn:(event:any)=>void){const group=listeners.get(name)??new Set();group.add(fn);listeners.set(name,group);return ()=>group.delete(fn)}};
export function emit(name:string,data:any){for(const fn of listeners.get(name)??[])fn({data})}

export const Browser={
 async OpenURL(url:string){
  const target=new URL(url)
  if(target.protocol!=='https:'&&target.protocol!=='http:')throw Error('预览版仅支持 HTTP 和 HTTPS 链接')
  window.open(target.href,'_blank','noopener,noreferrer')
 },
}
