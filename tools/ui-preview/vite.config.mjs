import path from 'node:path';
import {createRequire} from 'node:module';
const root=path.resolve(import.meta.dirname,'../../frontend');
const require=createRequire(path.join(root,'package.json'));
const vue=require('@vitejs/plugin-vue').default;
export default {root,server:{host:'127.0.0.1',port:9246,strictPort:true,fs:{allow:[path.resolve(root,'..')]}},plugins:[vue()],resolve:{alias:[{find:/^\.\.\/bindings\/multipan\/desktopservice$/,replacement:path.join(import.meta.dirname,'api.ts')},{find:/^@wailsio\/runtime$/,replacement:path.join(import.meta.dirname,'events.ts')}]}};
