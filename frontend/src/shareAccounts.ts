import type {Account} from '../bindings/multipan/internal/transfer/models'

const providerNames:Record<string,string> = {
 lanzou:'蓝奏云',
 quark:'夸克网盘',
 pan123:'123 云盘',
 baidu:'百度网盘',
 guangya:'光鸭云盘',
 ctdav:'诚通网盘',
 ctfile:'诚通 OpenAPI',
 webdav:'WebDAV',
}

export function shareProviderName(kind:string){
 return providerNames[kind]??kind
}

const shareWebsites=new Map([
 ['quark','https://pan.quark.cn/'],
 ['pan123','https://yun.123pan.cn/shareList'],
 ['baidu','https://pan.baidu.com/disk/main#/share/my'],
 ['guangya','https://www.guangyapan.com/'],
 ['lanzou','https://up.woozooo.com/'],
])

export function shareWebsite(kind:string){
 return shareWebsites.get(kind)
}

export function shareAccountLabel(account:Account,accounts:Account[]){
 const name=account.name?.trim()||'未命名账号'
 const matching=accounts.filter(row=>row.kind===account.kind&&(row.name?.trim()||'未命名账号')===name)
 const index=matching.findIndex(row=>row.id===account.id)
 const suffix=matching.length>1&&index>=0?'（'+(index+1)+'）':''
 return shareProviderName(account.kind)+' · '+name+suffix
}
