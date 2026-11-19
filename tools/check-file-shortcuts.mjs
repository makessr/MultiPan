// Exercise the production shortcut classifier without native key presses.
import fs from 'node:fs/promises';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
const root=path.resolve(import.meta.dirname,'../frontend');
const {default:ts}=await import(pathToFileURL(path.join(root,'node_modules/typescript/lib/typescript.js')));
const code=ts.transpileModule(await fs.readFile(path.join(root,'src/fileShortcuts.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const {fileShortcut}=await import('data:text/javascript;base64,'+Buffer.from(code).toString('base64'));
const event=(key,flags={})=>({key,ctrlKey:false,altKey:false,shiftKey:false,metaKey:false,isComposing:false,defaultPrevented:false,...flags});
for(const [e,action] of [[event('a',{ctrlKey:true}),'selectAll'],[event('A',{ctrlKey:true}),'selectAll'],[event('N',{ctrlKey:true,shiftKey:true}),'create'],[event('F2'),'rename'],[event('F5'),'refresh'],[event('ArrowUp',{altKey:true}),'up'],[event('ArrowLeft',{altKey:true}),'back'],[event('ArrowRight',{altKey:true}),'forward'],[event('F10',{shiftKey:true}),'menu'],[event('Escape'),'clear']]){
 assert.equal(fileShortcut(e,false),action);
 assert.equal(fileShortcut(e,true),null,'text editing must keep native shortcuts');
 assert.equal(fileShortcut({...e,isComposing:true},false),null,'IME input must be preserved');
 assert.equal(fileShortcut({...e,defaultPrevented:true},false),null,'handled dialog/menu keys must not run twice');
 assert.equal(fileShortcut({...e,metaKey:true},false),null,'Windows system keys must not be captured');
}
for(const e of [event('a'),event('n',{ctrlKey:true}),event('a',{ctrlKey:true,shiftKey:true}),event('n',{ctrlKey:true,shiftKey:true,altKey:true}),event('F2',{ctrlKey:true}),event('F5',{shiftKey:true}),event('F10'),event('ArrowUp'),event('ArrowUp',{altKey:true,ctrlKey:true}),event('Delete'),event('Enter')])assert.equal(fileShortcut(e,false),null);
console.log('PASS: file shortcut mappings and text/IME/dialog/system modifier guards; no delete/download side-effect shortcut.');
