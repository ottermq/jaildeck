import { mount } from 'svelte'
import './app.css'
// @ts-expect-error Svelte components are resolved by the bundler.
import App from "./App.svelte";

const app = mount(App, {
  target: document.getElementById('app')!,
})

export default app
