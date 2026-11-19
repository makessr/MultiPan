import type {CloudFolder,FolderPage} from '../bindings/multipan/internal/transfer/models'

export type UploadFolderMatch =
 {state:'found'; choices:{id:string; path:string; trail:CloudFolder[]}[]} |
 {state:'missing'|'ambiguous'|'error'|'searching'}

function normalized(name:string){
 return name.normalize('NFKC').toLowerCase().replace(/[\s\p{P}\p{S}]/gu,'')
}

function oneEdit(left:string,right:string){
 const a=Array.from(left),b=Array.from(right)
 if(Math.min(a.length,b.length)<5||Math.abs(a.length-b.length)>1)return false
 let i=0
 while(i<a.length&&i<b.length&&a[i]===b[i])i++
 if(a.length===b.length)return a.slice(i+1).join('')===b.slice(i+1).join('')
 return a.slice(i+(a.length>b.length?1:0)).join('')===b.slice(i+(b.length>a.length?1:0)).join('')
}

function matchRank(name:string,wanted:string,canonical:string){
 if(name===wanted)return 3
 if(!canonical)return 0
 const candidate=normalized(name)
 if(candidate===canonical)return 2
 const numbers=(value:string)=>value.match(/\p{N}+/gu)?.join('/')??''
 return numbers(candidate)===numbers(canonical)&&oneEdit(candidate,canonical)?1:0
}

export async function matchUploadFolder(root:string,names:string[],read:(parent:string)=>Promise<FolderPage>,current:()=>boolean):Promise<UploadFolderMatch>{
 let parent=root
 const parents=new Set([root])
 const trail:CloudFolder[]=[{id:root,name:'根目录'}]
 for(const [index,name] of names.entries()){
  if(!current())return {state:'error'}
  const page=await read(parent)
  if(!current()||page.currentId!==parent||!Array.isArray(page.folders))return {state:'error'}
  const canonical=normalized(name)
  let rank=0
  let matches:CloudFolder[]=[]
  const seen=new Set<string>()
  for(const folder of page.folders){
   if(!folder.id||seen.has(folder.id)||parents.has(folder.id))return {state:'error'}
   seen.add(folder.id)
   const score=matchRank(folder.name,name,canonical)
   if(!score||score<rank)continue
   if(score>rank){rank=score;matches=[]}
   matches.push(folder)
  }
  if(!matches.length)return {state:'missing'}
  if(index===names.length-1){
   if(matches.length>5)return {state:'ambiguous'}
   return {state:'found',choices:matches.map(folder=>({id:folder.id,path:[...trail,folder].map(item=>item.name).join(' / '),trail:[...trail,folder]}))}
  }
  if(matches.length!==1)return {state:'ambiguous'}
  const [folder]=matches
  parent=folder.id
  parents.add(parent)
  trail.push(folder)
 }
 return {state:'missing'}
}