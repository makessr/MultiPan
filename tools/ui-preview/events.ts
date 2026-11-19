const listeners=new Map<string,Set<(event:any)=>void>>();
export const Events={On(name:string,fn:(event:any)=>void){const group=listeners.get(name)??new Set();group.add(fn);listeners.set(name,group);return ()=>group.delete(fn)}};
export function emit(name:string,data:any){for(const fn of listeners.get(name)??[])fn({data})}
