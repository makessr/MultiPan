import {ref} from 'vue'
import * as api from '../bindings/multipan/internal/desktop/desktopservice'
import type {File as SourceFile,Target,UploadReview,UploadSkip} from '../bindings/multipan/internal/transfer/models'

// Both upload entry points share the same captured plan and cancellation rules.
// Cancelling a read ignores its late response; enqueue cannot be cancelled once
// submitted. No cloud write happens before the final enqueue call.
export function useUploadReview(queued:(count:number,skipped:number)=>Promise<void>,lock:(active:boolean)=>void){
 const pending=ref<{files:SourceFile[];targets:Target[];report:UploadReview|null}|null>(null)
 const checking=ref(false),submitting=ref(false),error=ref('')
 let epoch=0
 function cancel(){if(submitting.value)return;epoch++;pending.value=null;checking.value=false;error.value='';lock(false)}
 async function begin(files:SourceFile[],targets:Target[]){
  if(pending.value||checking.value||submitting.value)return
  pending.value={files:files.map(f=>({...f})),targets:targets.map(t=>({...t})),report:null}
  lock(true);await check()
 }
 async function check(){
  const plan=pending.value;if(!plan||checking.value||submitting.value)return
  const ticket=++epoch;checking.value=true;error.value='';plan.report=null
  try{const report=await api.CheckUploadPlan(plan.files,plan.targets);if(epoch!==ticket)return;if(!report?.items||!report.stamp||report.items.length!==plan.files.length*plan.targets.length)throw Error('上传检查结果不完整，请重新检查');plan.report=report;checking.value=false;if(report.items.every(row=>row.status==='ready'||row.status==='reuse'))await submit([])}
  catch(e){if(epoch===ticket)error.value=String(e)}
  finally{if(epoch===ticket)checking.value=false}
 }
 async function submit(skips:UploadSkip[]){
  const plan=pending.value;if(!plan?.report||checking.value||submitting.value)return
  const ticket=epoch;submitting.value=true;error.value=''
  try{const count=await api.EnqueueReviewedUploads(plan.files,plan.targets,skips,plan.report.stamp);if(epoch!==ticket)return;pending.value=null;lock(false);await queued(count,skips.length)}
  catch(e){if(epoch===ticket)error.value=String(e)}
  finally{if(epoch===ticket)submitting.value=false}
 }
 return {pending,checking,submitting,error,begin,check,submit,cancel}
}
