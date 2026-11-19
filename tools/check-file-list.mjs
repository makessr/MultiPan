import fs from 'node:fs/promises';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
const root=path.resolve(import.meta.dirname,'../frontend');
const {default:ts}=await import(pathToFileURL(path.join(root,'node_modules/typescript/lib/typescript.js')));
const code=ts.transpileModule(await fs.readFile(path.join(root,'src/fileList.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const {fileList,fileType,displaySize}=await import('data:text/javascript;base64,'+Buffer.from(code).toString('base64'));
const f=(id,name,size,extra={})=>({id,name,folder:false,size,...extra});
const source=Object.freeze([
 {id:'z',name:'Z目录',folder:true},f('10','文件10.txt',10),f('2','文件2.txt',2),
 {id:'a-dir',name:'A目录',folder:true},f('zero','空文件',0),f('unknown','未知.zip',-1),
 f('approx','近似.bin',0,{sizeText:'1.5 M'}),f('text-unknown','显示未知.pdf',0,{sizeText:'未知'}),
 f('9007199254740993','README',null),
].map(Object.freeze));
const before=JSON.stringify(source),ids=rows=>rows.map(r=>r.id);
const asc=fileList(source,'','all','asc'),desc=fileList(source,'','all','desc');
assert.deepEqual(ids(asc).slice(0,2),['a-dir','z']);assert.deepEqual(ids(desc).slice(0,2),['z','a-dir']);
assert.ok(ids(asc).indexOf('2')<ids(asc).indexOf('10'));assert.ok(ids(desc).indexOf('2')>ids(desc).indexOf('10'));
for(const sort of ['size-asc','size-desc','type-asc','type-desc'])assert.deepEqual(ids(fileList(source,'','all',sort)).slice(0,2),['a-dir','z']);
const sizeAsc=fileList(source,'','files','size-asc'),sizeDesc=fileList(source,'','files','size-desc');
assert.deepEqual(ids(sizeAsc).slice(0,4),['zero','2','10','approx']);assert.deepEqual(ids(sizeDesc).slice(0,4),['approx','10','2','zero']);
assert.deepEqual(new Set(ids(sizeAsc).slice(4)),new Set(['unknown','text-unknown','9007199254740993']));
assert.deepEqual(new Set(ids(sizeDesc).slice(4)),new Set(['unknown','text-unknown','9007199254740993']));
assert.deepEqual(ids(fileList(source,' FILE ','files','asc')),[]);
assert.deepEqual(ids(fileList(source,' readme ','files','asc')),['9007199254740993']);
assert.deepEqual(ids(fileList(source,'目录','folders','size-desc')),['a-dir','z']);
assert.deepEqual(ids(fileList([f('x','2.txt',1),f('y','10.txt',1)],'','files','size-desc')),['x','y']);
assert.deepEqual(ids(fileList([f('z','z.ZIP',1),f('b','b.txt',1),f('a','a.PDF',1),f('n','README',1)],'','files','type-asc')),['n','a','b','z']);
assert.deepEqual(ids(fileList([f('z','z.ZIP',1),f('b','b.txt',1),f('a','a.PDF',1),f('n','README',1)],'','files','type-desc')),['z','b','a','n']);
assert.equal(fileType(f('x','.gitignore',1)),'文件');assert.equal(fileType(f('x','report.',1)),'文件');assert.equal(fileType(f('x','a.tar.gz',1)),'GZ 文件');assert.equal(fileType(source[0]),'文件夹');
for(const [text,size] of [['0 B',0],['12',12],['1.5 M',1572864],['1 KB',1024],['2 KiB',2048],['2 g',2147483648],['1 TB',1099511627776]])assert.equal(displaySize(f('s','file',0,{sizeText:text})),size,text);
for(const text of ['未知','-1 MB','1 XB','1 IB','NaN','Infinity','999999999999999999 TB'])assert.equal(displaySize(f('s','file',0,{sizeText:text})),null,text);
assert.equal(displaySize(f('s','file',NaN)),null);assert.equal(displaySize(f('s','file',Number.MAX_SAFE_INTEGER+1)),null);
assert.equal(JSON.stringify(source),before); // view logic must not mutate source entries or large IDs
console.log('PASS: file list natural name/size/type sorting, folder priority, unknown/approximate sizes, filtering, type labels, source immutability and precise IDs.');
