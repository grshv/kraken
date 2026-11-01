import {defineConfig} from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
export default defineConfig({plugins:[react(),tailwindcss()],resolve:{alias:{'@':new URL('.',import.meta.url).pathname}},build:{outDir:'build',emptyOutDir:true,assetsInlineLimit:10000000,rollupOptions:{output:{inlineDynamicImports:true}}}});
