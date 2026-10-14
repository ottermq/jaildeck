<script lang="ts">
import {onMount} from 'svelte'
import { route, navigate, onNavClick } from "../router.svelte";
import { auth, logout } from "../stores/auth.svelte";
import Jails from "./Jails.svelte";
import NotFound from "./NotFound.svelte"

let formatted =  $derived(
    auth.principal?.expires_at === undefined 
    ?  '-' 
    : new Date(auth.principal.expires_at).toLocaleString() 
)

const menu = [
    {label: 'JAILS', href:'/jails'},
]

onMount(()=>{
    if (route.path==='/') navigate('/jails', true)
})

</script>
<div class="shell">
    <aside class="rail">
        <div class="jd-logo">
            <span class="jd-logo__j">J</span><span>ail</span><span class="jd-logo__d">D</span><span>eck</span>
        </div>
        <hr>
        <div >
            <nav class="rail-nav">
                {#each menu as {label, href} (href)}
                <a {href} class:active={route.path === href} onclick={(e) => onNavClick(e, href)}>{label}</a>
                {/each}
            </nav>
        </div>
        <hr>
        <div class="principal">
            <p>Expires at:</p>
            <p>{formatted}</p>
        </div>
        <button onclick={logout}>Logout</button>
    </aside>
    <main>
        {#if route.path === '/jails'}
            <Jails />
        {:else}
            <NotFound />
        {/if}
    </main>
</div>

<style>
.shell { display: flex; min-height: 100vh; }
main    { flex: 1; min-width: 0; padding: 24px; }
.rail {
    width: 220px;
    flex-shrink: 0;
    background: var(--color-surface-raised);  
    color: var(--color-surface-raised-contrast);
    display: flex;
    flex-direction: column;
    padding: 18px 0;
}

.jd-logo {
    display: inline-flex;
    align-items: baseline;
    font-family: 'DejaVu-Sans', sans-serif;
    font-weight: bold;
    font-size: 48px;
    line-height: 1;
    letter-spacing: -0.04em;
    margin-bottom: 10px;
    color: var(--jd-gray);
}

.jd-logo__j {
  color: var(--jd-white);
}

.jd-logo__d {
  color: var(--jd-red);
}

.rail-nav {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 1px;
    padding: 10px;
    border-top: 1px solid color-mix(in srgb, var(--color-surface-raised-contrast) 12%, transparent);
    border-bottom: 1px solid
        color-mix(in srgb, var(--color-surface-raised-contrast) 12%, transparent);
}
</style>