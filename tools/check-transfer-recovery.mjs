import fs from 'node:fs/promises';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
const root=path.resolve(import.meta.dirname,'../frontend');
const {default:ts}=await import(pathToFileURL(path.join(root,'node_modules/typescript/lib/typescript.js')));
const code=ts.transpileModule(await fs.readFile(path.join(root,'src/transferRecovery.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const {transferRecovery:recover}=await import('data:text/javascript;base64,'+Buffer.from(code).toString('base64'));
for(const status of ['queued','running','success','saved'])assert.equal(recover('download',status,'HTTP=401'),null);
const cases=[
 ['upload','登录已过期，请重新登录','auth'],
 ['download','下载接口 HTTP=401','auth'],
 ['download','百度文件节点拒绝下载 HTTP=403 reason=account-policy','access'],
 ['download','HTTP=429','access'],
 ['upload','NoSuchUpload','restart'],
 ['upload','ExpiredToken','restart'],
 ['upload','上传会话已过期','restart'],
 ['upload','上传结果尚未确认 HTTP=403','verify'],
 ['upload','123 尚未确认目标文件的名称、大小和 MD5','verify'],
 ['download','账号来源或登录信息已变化，请重新创建下载任务','rebuild'],
 ['download','账号不存在','rebuild'],
 ['download','下载文件MD5不一致','verify'],
 ['upload','目标目录已有同名文件','conflict'],
 ['upload','源文件已修改，请重新选择','source'],
 ['download','request=POST network=dns','network'],
 ['download','unexpected EOF','network'],
 ['upload','接口数据格式不兼容','unknown'],
];
for(const [direction,message,kind]of cases){const before=message;assert.equal(recover(direction,'failed',message).kind,kind,message);assert.equal(message,before)}
assert.match(recover('download','failed','登录已失效').detail,/重新创建下载任务/);
assert.match(recover('upload','failed','ExpiredToken').detail,/先核对远端/);
assert.equal(recover('download','paused','已暂停').kind,'paused');
console.log('PASS: recovery advice distinguishes account expiry, node policy, upload session, uncertain completion, conflicts, source changes and network; no advice on successful/active tasks and no automatic retries.');
