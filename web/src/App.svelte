<script lang="ts">
  import { onMount } from 'svelte'
  import Login from './lib/pages/Login.svelte'
  import Shell from './lib/pages/Shell.svelte'
  import { loadPrincipal, auth } from './lib/stores/auth.svelte'

  // loadPrincipal resolves either way: a 401 is the ordinary unauthenticated
  // state, not a failure, and it leaves auth.principal null so the gate below
  // renders the login view.
  onMount(() => {
    loadPrincipal()
  })
</script>

{#if !auth.checked}
  <div class="boot">
    <p class="boot__text">Starting Jail Deck…</p>
  </div>
{:else if auth.principal}
  <Shell />
{:else}
  <Login />
{/if}

<style>
  .boot {
    display: grid;
    place-items: center;
    min-height: 100svh;
  }

  .boot__text {
    color: var(--text-muted);
  }
</style>
