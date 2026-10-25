import { svelte } from '@sveltejs/vite-plugin-svelte'
import { svelteTesting } from '@testing-library/svelte/vite'
import { defineConfig } from 'vitest/config'

const apiPort = process.env.JAILDECK_PORT ?? '8888'

export default defineConfig({
  // svelteTesting puts the `browser` export condition first under test, so a
  // component mounts with Svelte's client runtime rather than its server
  // one, and cleans up what each test rendered.
  plugins: [svelte(), svelteTesting()],
  server: { proxy: { '/api': `http://localhost:${apiPort}` } },
  build: { outDir: 'dist', emptyOutDir: true } ,
  test: {
    // A DOM, for the component tests. jsdom rather than a real browser:
    // what these check is what a control binds and what the form sends,
    // which needs no layout. Layout is checked by looking (docs/UI.md).
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
  },
})
