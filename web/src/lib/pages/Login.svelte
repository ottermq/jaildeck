<script lang="ts">
import { ApiError } from '../services/api'
import { login } from '../stores/auth.svelte'

let error = $state('')
let password = $state('')
let pending = $state(false)

async function handleSubmit(e: SubmitEvent) {
    e.preventDefault()
    error = ''
    pending = true
    try {
        await login(password)
    } catch (err) {
        if (err instanceof ApiError && err.statusCode === 429) {
            // argon2id verification is serialised (JD-003), so concurrent
            // attempts are shed rather than queued. Not a wrong password.
            error = 'The server is busy verifying another login. Try again in a moment.'
        } else if (err instanceof ApiError && err.statusCode === 401) {
            error = 'Incorrect password.'
        } else {
            error = 'Could not reach Jail Deck.'
        }
    } finally {
        pending = false
    }
}
</script>

<main class="screen">
    <form class="card" onsubmit={handleSubmit}>
        <div class="card__head">
            <span class="brand">
                <span class="brand__accent">J</span>ail<span class="brand__accent">D</span>eck
            </span>
            <p class="page-sub">Sign in to manage this host's jails.</p>
        </div>

        <div class="field">
            <label for="password">Password</label>
            <!-- svelte-ignore a11y_autofocus -->
            <input
                id="password"
                name="password"
                type="password"
                autocomplete="current-password"
                autofocus
                bind:value={password}
            />
        </div>

        {#if error}
            <p class="notice notice--error" role="alert">{error}</p>
        {/if}

        <button type="submit" class="btn btn--primary btn--block" disabled={pending}>
            {pending ? 'Signing in…' : 'Sign in'}
        </button>
    </form>
</main>

<style>
.screen {
    display: grid;
    place-items: center;
    min-height: 100svh;
    padding: var(--space-4);
}

.card {
    width: 100%;
    max-width: 22rem;
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
    padding: var(--space-6);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
}

.card__head {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
}

.brand {
    font-size: 1.5rem;
    font-weight: 700;
    letter-spacing: -0.02em;
    color: var(--text-strong);
}

.brand__accent {
    color: var(--brand);
}

.field {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
}

.field input {
    width: 100%;
}

.notice--error {
    font-size: 0.875rem;
    padding: var(--space-3);
}

:global(.btn--block) {
    justify-content: center;
    width: 100%;
    padding-block: var(--space-3);
}
</style>
