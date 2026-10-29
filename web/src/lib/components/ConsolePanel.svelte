<script lang="ts">
import type { Snippet } from 'svelte'
import Console from './Console.svelte'

// The Console panel of a jail's or a guest's page (JD-042): a button that
// opens the terminal, the terminal while it lasts, and why it closed, in
// words. The page's own text about it comes in as children.
let { path, running, openLabel, onended, children }: {
    path: string
    // Whether there is something to attach to: a running jail or guest.
    running: boolean
    openLabel: string
    // Told why a console closed, so the page can read itself again when the
    // jail or guest stopped from somewhere else.
    onended?: (reason: string) => void
    children?: Snippet
} = $props()

let open = $state(false)
// Remounts the terminal for "Open again".
let run = $state(0)
let ended = $state<string | null>(null)

const reasons: Record<string, string> = {
    'client': 'Closed.',
    'shell-exited': 'The shell exited.',
    'jail-stopped': 'The jail stopped, and its console with it.',
    'guest-stopped': 'The guest stopped, and its console with it.',
    'console-closed': 'The console device closed.',
    'console-shared': 'Something else on the host opened this console too, a vm console most likely, and each reader would get only part of what the guest prints. Close it there (~. in cu), then open this one again.',
    'session-expired': 'Your login ended. Sign in again to open another console.',
    'idle': 'Closed: nothing was typed for a while.',
}

function start() {
    ended = null
    run += 1
    open = true
}

// detail, when the server gave one, says more than the reason's own words:
// which process shares the console, and how to close it (#153).
function closed(reason: string, detail: string) {
    ended = detail || (reasons[reason] ?? 'The connection ended, or could not be made.')
    onended?.(reason)
}
</script>

<section class="panel" aria-labelledby="console-heading">
    <div class="panel__head">
        <h2 id="console-heading">Console</h2>
        {#if !open}
            {#if running}
                <button type="button" class="btn btn--small" onclick={start}>{openLabel}</button>
            {/if}
        {:else if ended !== null}
            <div class="actions">
                {#if running}
                    <button type="button" class="btn btn--small" onclick={start}>Open again</button>
                {/if}
                <button type="button" class="btn btn--small" onclick={() => (open = false)}>Dismiss</button>
            </div>
        {:else}
            <button type="button" class="btn btn--small" onclick={() => (open = false)}>Close</button>
        {/if}
    </div>
    {#if open}
        {#key run}
            <Console {path} onclosed={closed} />
        {/key}
        {#if ended !== null}
            <p class="notice" role="status">{ended}</p>
        {/if}
    {/if}
    {@render children?.()}
</section>

<style>
/* A panel styles itself here, as VMNetwork's and VMImages' do: the pages'
   scoped styles do not reach into a component. */
.panel {
    margin-top: var(--space-6);
}

.panel__head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-4);
    margin-bottom: var(--space-3);
}

.panel__head h2 {
    margin-bottom: 0;
}

.notice {
    margin-top: var(--space-3);
}

.actions {
    display: flex;
    gap: var(--space-2);
}
</style>
