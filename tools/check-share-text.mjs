import fs from 'node:fs/promises';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
const root=path.resolve(import.meta.dirname,'../frontend');
const {default:ts}=await import(pathToFileURL(path.join(root,'node_modules/typescript/lib/typescript.js')));
const code=ts.transpileModule(await fs.readFile(path.join(root,'src/shareText.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const {shareText,newShareCode,shareLinkSearchKey,searchShareLinks}=await import('data:text/javascript;base64,'+Buffer.from(code).toString('base64'));
const r={state:'ready',name:'资料.zip',url:'https://pan.quark.cn/s/fixture',password:'A1b2',kind:'quark',days:7,secret:'never-secret',accountId:'never-account',scope:'never-scope',endpoint:'never-endpoint',message:'never-diagnostic'};
assert.match(shareText(r),/提取码：A1b2/);assert.match(shareText(r),/申请有效期：7天/);assert.doesNotMatch(shareText(r),/never-/);assert.equal(shareText(r,true),r.url);
assert.deepEqual([...searchShareLinks('链接：https://pan.baidu.com/s/1Fixture?pwd=A1b2。\n提取码：A1b2')],['pan.baidu.com/s/1Fixture']);
assert.equal(shareLinkSearchKey('https://pan.baidu.com/share/init?surl=Fixture&pwd=A1b2'),'pan.baidu.com/s/1Fixture');
assert.notEqual(shareLinkSearchKey('https://www.lanzoui.com/AbC'),shareLinkSearchKey('https://www.lanzoui.com/abc'));
assert.equal(shareLinkSearchKey('https://pan.baidu.com/share/init?surl=A&surl=B'),'');
assert.equal(shareLinkSearchKey('https://pan.baidu.com@elsewhere.invalid:8443/s/Fixture'),'');
assert.doesNotMatch(shareText({...r,password:''}),/提取码/);assert.match(shareText({...r,kind:'lanzou'}),/沿用网盘现有设置/);
for(const state of ['working','failed','unknown'])assert.equal(shareText({...r,state}),'');
assert.equal(shareText({...r,url:'http://pan.quark.cn/s/fixture'}),'');
for(let i=0;i<32;i++)assert.match(newShareCode(),/^[A-Za-z0-9]{4}$/);
console.log('PASS: explicit share-text whitelist, link-only copying, no copy before ready, requested expiry labels and generated 4-character access codes.');

assert.match(shareText({...r,kind:'pan123',days:0}),/申请有效期：永久（至2099年）/);assert.match(shareText({...r,kind:'pan123',days:30}),/申请有效期：30天/);

assert.match(shareText({...r,kind:'baidu',days:7}),/申请有效期：7天/);assert.match(shareText({...r,kind:'baidu',days:0}),/申请有效期：永久/);

const bd={...r,kind:'baidu',url:'https://pan.baidu.com/s/1fixture',password:'A1b2',days:0};
const confirmedBaiduExpiry=shareText({...bd,days:0,expiry:'2026-11-20 12:00 到期'});
assert.match(confirmedBaiduExpiry,/当前有效期 · 2026-11-20 12:00 到期/);
assert.doesNotMatch(confirmedBaiduExpiry,/永久/);
assert.equal(shareText(bd,true),'https://pan.baidu.com/s/1fixture?pwd=A1b2');assert.match(shareText(bd),/链接：https:\/\/pan\.baidu\.com\/s\/1fixture\?pwd=A1b2/);assert.equal(shareText(bd,true,false),bd.url);assert.match(shareText(bd,false,false),/提取码：A1b2/);assert.equal(bd.url,'https://pan.baidu.com/s/1fixture');
for(const state of ['working','unknown','failed'])assert.equal(shareText({...bd,state},true),'');
for(const password of ['', 'bad!', 'abcde'])assert.equal(shareText({...bd,password},true),bd.url);
assert.equal(shareText({...bd,url:'https://pan.baidu.com.evil.example/s/1fixture'},true),'https://pan.baidu.com.evil.example/s/1fixture');

const gy={...bd,kind:'guangya',url:'https://www.guangyapan.com/s/fixture'};assert.equal(shareText(gy,true),'https://www.guangyapan.com/s/fixture?code=A1b2');assert.equal(shareText(gy,true,false),gy.url);assert.match(shareText(gy),/申请有效期：永久/);assert.equal(shareText({...gy,password:''},true),gy.url);
