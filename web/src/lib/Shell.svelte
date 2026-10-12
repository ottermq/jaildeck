<script lang="ts">
import { auth, logout } from "./auth.svelte";
let formatted =  $derived(
    auth.principal?.expires_at === undefined 
    ?  '-' 
    : new Date(auth.principal.expires_at).toLocaleString() 
)

</script>

<div>
    <h1>Shell</h1>
    <div class="principal">
        <h2>Principal</h2>
        <p><span>Kind: {auth.principal?.kind}</span></p>
        <p><span>Name: {auth.principal?.name}</span></p>
        <p><span>Expires at: {formatted}</span></p>        
        <button onclick={logout}>Logout</button>
    </div>
</div>