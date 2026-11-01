import {build} from 'vite';
import {readFile,writeFile,readdir} from 'node:fs/promises';
await build();
const dir=new URL('./build/assets/',import.meta.url),files=await readdir(dir);
const css=(await Promise.all(files.filter(f=>f.endsWith('.css')).map(f=>readFile(new URL(f,dir),'utf8')))).join('\n').replace(/[ \t]+$/gm,'');
const js=(await Promise.all(files.filter(f=>f.endsWith('.js')).map(f=>readFile(new URL(f,dir),'utf8')))).join('\n');
await writeFile(new URL('../kraken-ui.html',import.meta.url),'<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Kraken — прототип UI</title><style>'+css+'</style></head><body><div id="root"></div><script type="module">'+js.replace(/^[ \t]+$/gm,'').replaceAll('</script','<\\/script')+'</script></body></html>');
console.log('Standalone prototype: ../kraken-ui.html');
