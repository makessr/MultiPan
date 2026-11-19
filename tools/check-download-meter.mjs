// Deterministic checks of the production TypeScript meter. No real waits,
// network, native UI, downloaded content or user state.
import fs from 'node:fs/promises';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
const root=path.resolve(import.meta.dirname,'../frontend');
const {default:ts}=await import(pathToFileURL(path.join(root,'node_modules/typescript/lib/typescript.js')));
const source=await fs.readFile(path.join(root,'src/downloadMeter.ts'),'utf8');
const code=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const {DownloadMeter,remainingTime}=await import('data:text/javascript;base64,'+Buffer.from(code).toString('base64'));
const meter=new DownloadMeter();
assert.equal(meter.read(0).phase,'waiting');
meter.push('one',0,10000,0);meter.push('one',1000,10000,1000);
assert.equal(meter.read(1000).bytesPerSecond,1000);
assert.equal(meter.read(1000).remainingSeconds,9);
assert.equal(meter.read(4000).phase,'stalled');
assert.equal(meter.read(4000).bytesPerSecond,0);
assert.equal(meter.read(4000).remainingSeconds,null);
meter.push('one',5000,10000,5000);assert.equal(meter.read(5000).phase,'transferring');
meter.push('one',10000,10000,6000);assert.equal(meter.read(6000).phase,'checking');
assert.equal(meter.read(20000).phase,'checking'); // hashing/sync is not a network stall
meter.finish('one');assert.equal(meter.read(20000).phase,'complete');
assert.equal(meter.push('one',10000,10000,20001),false);
meter.push('two',0,-1,21000);assert.equal(meter.read(21000).phase,'waiting');
meter.push('two',2048,-1,22000);assert.equal(meter.read(22000).bytesPerSecond,2048);
assert.equal(meter.read(22000).remainingSeconds,null); // unknown length must not invent ETA
meter.push('two',0,10000,23000);assert.equal(meter.read(23000).phase,'waiting');
meter.push('two',100,10000,23100);assert.equal(meter.read(23100).bytesPerSecond,null); // short sample
meter.push('two',1000,10000,24000);assert.equal(meter.read(24000).bytesPerSecond,1000);
// Invalid samples cannot replace the last good progress or produce NaN/Infinity.
for(const [received,total,time] of [[NaN,1,1],[Infinity,1,1],[-1,1,1],[1,NaN,1],[1,-2,1],[1,1,NaN],[1,1,-1],[Number.MAX_SAFE_INTEGER+1,1,1]]){
 assert.equal(meter.push('two',received,total,time),false);
}
assert.equal(meter.read(24000).bytesPerSecond,1000);
meter.reset();meter.push('empty',0,0,0);assert.equal(meter.read(0).phase,'checking');
meter.reset();meter.push('waiting',0,10000,0);meter.push('waiting',0,10000,20000);meter.push('waiting',1000,10000,21000);
assert.equal(meter.read(21000).bytesPerSecond,1000); // metadata delay excluded
meter.push('waiting',2000,10000,10000);assert.equal(meter.read(10000).bytesPerSecond,null); // defensive clock reset
meter.reset();meter.push('long',0,100000,0);
for(let i=1;i<=50;i++)meter.push('long',i*1000,100000,i*1000);
assert.equal(meter.read(50000).bytesPerSecond,1000); // old samples evicted
assert.equal(meter.read(50000).remainingSeconds,50);
meter.finish('wrong-key');assert.equal(meter.read(50000).phase,'transferring');
for(const [seconds,expected] of [[null,'暂无法估算'],[NaN,'暂无法估算'],[Infinity,'暂无法估算'],[-1,'暂无法估算'],[0.5,'不到1秒'],[5.1,'6秒'],[60,'1分0秒'],[3661,'1小时1分'],[90000,'1天1小时']])assert.equal(remainingTime(seconds),expected);
console.log('PASS: download rates/ETA, per-file and retry reset, unknown/zero length, stale/short/invalid samples, long stream, metadata delay, clock reset and completion.');
