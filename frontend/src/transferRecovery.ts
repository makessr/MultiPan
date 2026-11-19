// Advice only: keep the original diagnostic, never replay a transfer or infer
// that a generic 403 means an expired account. Provider policy errors differ.
export function transferRecovery(direction:'upload'|'download',status:string,message:string){
 if(status!=='failed'&&status!=='paused')return null
 const m=message.toLowerCase()
 const advice=(kind:string,title:string,detail:string)=>({kind,title,detail})
 if(/账号来源或登录信息已变化|账号不存在|账号已删除|目标账号已删除/.test(m))return advice('rebuild','需要重新创建任务','确认目标网盘账号后重新选择文件创建任务；原任务不会切换到其他账号。')
 if(/未确认|核验.*失败|结果.*待确认|未能确认|尚未确认/.test(m)&&direction==='upload')return advice('verify','先核对网盘中的文件','上传可能已完成。刷新远端核对文件名和大小，确认结果后再决定是否重试，避免重复上传。')
 if(direction==='upload'&&/nosuchupload|expiredtoken|requestexpired|上传会话.*过期|分片会话.*过期|临时授权.*过期/.test(m))return advice('restart','上传会话可能已过期','先核对远端是否已生成完整文件；确认仍未完成后，使用“从头重新上传”重新申请会话。')
 if(/登录.*(过期|失效)|未登录|请重新登录|http[=\s:]401\b/.test(m))return advice('auth','需要检查登录状态',direction==='download'?'更新此网盘登录后，请重新创建下载任务；旧任务绑定原登录信息，不会自动切换。':'更新此网盘登录后可继续 / 重试；登录信息变化时，旧上传分片不会复用。')
 if(/md5.*(不一致|失败)|sha256.*(不一致|失败)|文件大小已变化|长度.*(不符|不一致)|校验失败/.test(m))return advice('verify','需要核对文件内容','刷新网盘文件信息，核对原文件或官方下载结果后重新创建任务。不要把未通过校验的临时内容当作正式文件。')
 if(/同名|已存在|已有同名|路径被.*占用/.test(m))return advice('conflict','需要处理同名项目','核对本地保存位置和网盘目标目录，选择其他名称或目录后重新创建任务。程序不会自动覆盖已有文件。')
 if(/源文件.*(修改|变化|不存在)|无法读取.*源文件/.test(m))return advice('source','需要重新选择源文件','本地源文件可能已修改或移动，请重新选择并创建上传任务。')
 if(/network=|dns|timeout|timed out|连接失败|网络|连接中断|unexpected eof|connection reset/.test(m))return advice('network','连接异常，可手动继续',direction==='upload'?'检查网络后继续 / 重试；先留意网盘是否已产生文件。仅复用已确认的分片，不支持的网盘从头上传。':'检查网络后继续 / 重试。节点及文件版本符合续传条件时恢复，否则安全重新下载。')
 if(/http[=\s:](403|429)\b|拒绝|风控|安全验证|验证码/.test(m))return advice('access','需要检查网盘权限或验证','到官网确认文件可访问、额度及安全验证状态；确认后再手动重试。访问被拒绝不一定是登录过期。')
 if(status==='paused')return advice('paused','任务已暂停',direction==='upload'?'可继续 / 重试；先核对远端是否已生成文件。不支持分片恢复的网盘会从头上传。':'可继续 / 重试；版本无法确认或节点不支持续传时会重新下载。')
 return advice('unknown','请先核对错误和目标状态',direction==='upload'?'保留下方原始错误。刷新网盘确认是否已产生文件后，再决定继续或从头上传，避免重复文件。':'保留下方原始错误，检查网络、网盘文件和保存目录后手动重试。')
}
