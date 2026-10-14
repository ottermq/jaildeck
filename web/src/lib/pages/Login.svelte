<script lang="ts">
import { ApiError } from "../services/api";
import { login } from "../stores/auth.svelte";

let error = $state('')
let password = $state('')
let pending = $state(false)


async function handleSubmit(e:SubmitEvent){
    e.preventDefault()
    error = ''
    try {
        pending = true
        await login(password)        
    } catch (err) {
        if (err instanceof ApiError && err.statusCode === 429) {
            error = 'The server is busy verifying another login. Try again in a moment.'
        } else if (err instanceof ApiError && err.statusCode === 401) {
            error = 'Incorrect password'
        } else {
            error = 'Could not reach Jail Deck'
        }
    }
    finally
    {
        pending = false
    }
}
</script>

<div class="login_container">
    <form onsubmit={handleSubmit}>
        <label>
            Password
            <input name="password" type="password" bind:value={password}>
        </label>
        <button disabled={pending}>{pending?'Signing in…':'Login'}</button>
        <div>
            {#if error} 
            <p class="error">{error}</p>          
            {/if}
        </div>
    </form>
</div>

<style>
    .error {
        color: red;
    }
</style>