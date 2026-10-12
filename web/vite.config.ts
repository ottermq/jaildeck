import { svelte } from '@sveltejs/vite-plugin-svelte'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [svelte()],
  server: { proxy: { '/api': 'http://localhost:8888' } },
  build: { outDir: 'dist', emptyOutDir: true } ,
})
