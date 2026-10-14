import { svelte } from '@sveltejs/vite-plugin-svelte'
import { defineConfig } from 'vite'

const apiPort = process.env.JAILDECK_PORT ?? '8888'

export default defineConfig({
  plugins: [svelte()],
  server: { proxy: { '/api': `http://localhost:${apiPort}` } },
  build: { outDir: 'dist', emptyOutDir: true } ,
})
