export function shareLinkSearchKey(value:string){
 try{
  const url=new URL(value)
  if((url.protocol!=='https:'&&url.protocol!=='http:')||url.username||url.password||url.port)return ''
  if(url.hostname==='pan.baidu.com'&&url.pathname==='/share/init'){
   const ids=url.searchParams.getAll('surl')
   return ids.length===1&&/^[A-Za-z0-9_-]{1,127}$/.test(ids[0])?'pan.baidu.com/s/1'+ids[0]:''
  }
  const path=url.pathname.replace(/\/$/,'')
  return path?url.hostname+path:''
 }catch{
  return ''
 }
}

export function searchShareLinks(value:string){
 const candidates=value.match(/https?:\/\/[^\s<>"'，。；、！？（）【】《》]+/giu)??[]
 return new Set(candidates.map(link=>shareLinkSearchKey(link.replace(/[),.;!?\]}]+$/u,''))).filter(Boolean))
}

export function shareLink(result:{state:string;url:string;password:string;kind:string},autoFill=true){
 if(result.state!=='ready'||!result.url.startsWith('https://'))return ''
 if(autoFill&&result.kind==='baidu'&&/^[A-Za-z0-9]{4}$/.test(result.password)&&/^https:\/\/pan\.baidu\.com\/s\/[A-Za-z0-9_-]{1,128}$/.test(result.url))return result.url+'?pwd='+result.password
 if(autoFill&&result.kind==='guangya'&&/^[A-Za-z0-9]{4}$/.test(result.password)&&/^https:\/\/www\.guangyapan\.com\/s\/[A-Za-z0-9_-]{1,128}$/.test(result.url))return result.url+'?code='+result.password
 return result.url
}
export function shareText(result:{state:string;name:string;url:string;password:string;kind:string;days:number;expiry?:string},linkOnly=false,autoFill=true){
 if(result.state!=='ready'||!result.url.startsWith('https://'))return ''
 const link=shareLink(result,autoFill)
 if(linkOnly)return link
 const expiry=result.expiry?('当前有效期 · '+result.expiry):['quark','pan123','baidu','guangya'].includes(result.kind)?'申请有效期：'+(result.days===0?(result.kind==='pan123'?'永久（至2099年）':'永久'):result.days+'天'):'有效期：沿用网盘现有设置'
 return ['文件：'+result.name,'链接：'+link,...(result.password?['提取码：'+result.password]:[]),expiry].join('\n')
}
export function managedShareText(result:{name:string;url:string;password:string;expiry:string},kind:string,linkOnly=false,autoFill=true){
 const link=shareLink({state:'ready',url:result.url,password:result.password,kind},autoFill)
 if(!link||linkOnly)return link
 return ['文件：'+result.name,'链接：'+link,'提取码：'+(result.password||'无'),result.expiry].filter(Boolean).join('\n')
}
export function newShareCode(){const alphabet='ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789';const bytes=crypto.getRandomValues(new Uint8Array(4));return Array.from(bytes,b=>alphabet[b%alphabet.length]).join('')}
