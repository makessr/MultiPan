import type {CloudFolder,FolderPage} from '../bindings/multipan/internal/transfer/models'

export type UploadFolderChoice={id:string;path:string;trail:CloudFolder[];remaining:string[]}
export type UploadFolderMatch =
 {state:'found';choices:UploadFolderChoice[]} |
 {state:'missing'|'error'|'searching'}

const maxChoices=8, maxBranches=3, maxWrappers=5, maxReads=24, maxWrapperDepth=2

function normalized(name:string){
 return name.normalize('NFKC').toLowerCase().replace(/[\s\p{P}\p{S}]/gu,'')
}

// 数字串按段比较，忽略前导 0，避免「002」与「2」被判成不同目录。
function numbers(value:string){
 return (value.match(/\p{N}+/gu)??[]).map(run=>run.replace(/^0+(?=\d)/u,'')).join('/')
}

function oneEdit(left:string,right:string){
 const a=Array.from(left),b=Array.from(right)
 if(Math.abs(a.length-b.length)>1)return false
 let i=0
 while(i<a.length&&i<b.length&&a[i]===b[i])i++
 if(a.length===b.length)return a.slice(i+1).join('')===b.slice(i+1).join('')
 return a.slice(i+(a.length>b.length?1:0)).join('')===b.slice(i+(b.length>a.length?1:0)).join('')
}

// 长名称允许两处差异，短名称只认一处，降低把无关目录拉进来的概率。
function twoEdits(left:string,right:string){
 const a=Array.from(left),b=Array.from(right)
 if(Math.abs(a.length-b.length)>2)return false
 let row=Array.from({length:b.length+1},(_,index)=>index)
 for(let i=1;i<=a.length;i++){
  const next=[i]
  for(let j=1;j<=b.length;j++){
   const cost=a[i-1]===b[j-1]?0:1
   next[j]=Math.min(row[j]+1,next[j-1]+1,row[j-1]+cost)
  }
  row=next
 }
 return row[b.length]<=2
}

function stripped(value:string){
 return value.replace(/\p{N}+/gu,run=>run.replace(/^0+(?=\d)/u,''))
}

function matchRank(name:string,wanted:string,canonical:string,numeric:string){
 if(name===wanted)return 4
 if(!canonical)return 0
 const candidate=normalized(name)
 if(!candidate)return 0
 if(candidate===canonical)return 3
 if(numbers(candidate)!==numeric)return 0
 if(stripped(candidate)===stripped(canonical))return 2
 if(oneEdit(candidate,canonical))return 2
 if(Math.min(candidate.length,canonical.length)>=8&&twoEdits(candidate,canonical))return 2
 if(Math.min(candidate.length,canonical.length)>=3&&(candidate.startsWith(canonical)||canonical.startsWith(candidate)))return 1
 return 0
}

export async function matchUploadFolder(root:string,names:string[],read:(parent:string)=>Promise<FolderPage>,current:()=>boolean,start:CloudFolder[]=[{id:root,name:'根目录'}]):Promise<UploadFolderMatch>{
 if(!names.length)return {state:'missing'}
 const cache=new Map<string,CloudFolder[]>()
 const seen=new Set<string>(start.map(folder=>folder.id))
 let reads=0
 class Stopped extends Error{}
 async function children(parent:string){
  const cached=cache.get(parent)
  if(cached)return cached
  if(reads>=maxReads||!current())throw new Stopped()
  reads++
  const page=await read(parent)
  if(!current()||page.currentId!==parent||!Array.isArray(page.folders))throw new Stopped()
  const folders=page.folders.filter(folder=>folder.id)
  cache.set(parent,folders)
  return folders
 }
 const choice=(trail:CloudFolder[],folder:CloudFolder,index:number):UploadFolderChoice=>{
  const path=[...trail,folder]
  return {id:folder.id,path:path.map(item=>item.name).join(' / '),trail:path,remaining:names.slice(index+1)}
 }
 type Progress={complete:UploadFolderChoice[];partial:UploadFolderChoice[]}
 async function visit(parent:string,trail:CloudFolder[],index:number,wrappers:number):Promise<Progress>{
  const folders=await children(parent)
  const name=names[index],canonical=normalized(name),numeric=numbers(canonical)
  const scored=folders.filter(folder=>!seen.has(folder.id)).map(folder=>({folder,rank:matchRank(folder.name,name,canonical,numeric)})).filter(item=>item.rank>0)
  if(!scored.length){
   if(wrappers>=maxWrapperDepth)return {complete:[],partial:[]}
   // 目标可能多套了一层目录：向下多找一级，命中后路径会带上被跳过的目录名。
   for(const folder of folders.slice(0,maxWrappers)){
    if(seen.has(folder.id))continue
    seen.add(folder.id)
    const found=await visit(folder.id,[...trail,folder],index,wrappers+1)
    seen.delete(folder.id)
    if(found.complete.length||found.partial.length)return found
   }
   return {complete:[],partial:[]}
  }
  const best=Math.max(...scored.map(item=>item.rank))
  const matches=scored.filter(item=>item.rank===best).map(item=>item.folder)
  const group=index===names.length-1?matches:matches.slice(0,maxBranches)
  const complete:UploadFolderChoice[]=[],partial:UploadFolderChoice[]=[]
  for(const folder of group){
   if(index===names.length-1){complete.push(choice(trail,folder,index));continue}
   seen.add(folder.id)
   const found=await visit(folder.id,[...trail,folder],index+1,wrappers)
   seen.delete(folder.id)
   complete.push(...found.complete)
   partial.push(...found.partial)
   if(!found.complete.length&&!found.partial.length)partial.push(choice(trail,folder,index))
  }
  return {complete:complete.slice(0,maxChoices),partial}
 }
 try{
  const progress=await visit(start[start.length-1].id,start,0,0)
  const choices=(progress.complete.length?progress.complete:progress.partial).filter((item,index,rows)=>rows.findIndex(row=>row.id===item.id&&row.path===item.path)===index).slice(0,maxChoices)
  return choices.length?{state:'found',choices}:{state:'missing'}
 }catch{
  return {state:'error'}
 }
}
