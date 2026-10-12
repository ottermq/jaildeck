<script lang="ts">
  import { onMount } from 'svelte';
  import Login from './lib/Login.svelte';
  import Shell from './lib/Shell.svelte';
  import { loadPrincipal, auth } from "./lib/auth.svelte";
  import { ApiError } from './lib/api';
  


  async function getPrincipal(){
    try {
      await loadPrincipal();      
    } catch (error) {
      if (error instanceof ApiError) {
        console.log("Status code:" + error.statusCode + " " + error.statusText)
      }
    }
  }

  onMount(()=>{
    getPrincipal()
  });

</script>

<section id="center">
  {#if auth.checked === false }
  <p>placeholder for a spinner</p>
  {:else if auth.principal}
  <Shell/>
  {:else}
  <Login/>
{/if}
</section>

