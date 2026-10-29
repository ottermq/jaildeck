<script lang="ts">
import { onMount } from 'svelte'
import { about, loadAbout, releaseNotes } from '../stores/about.svelte'

// The Host page's "what exactly is running": the badge says which version,
// this says which build, since when, and with which state (#136).
let notes = $derived(about.info ? releaseNotes(about.info.version) : null)

onMount(loadAbout)
</script>

{#if about.info}
    <!-- What exactly is running, for when something needs debugging (#136). -->
    <section class="jaildeck" aria-labelledby="jaildeck-heading">
        <h2 id="jaildeck-heading">Jail Deck</h2>
        <dl class="facts">
            <dt>Version</dt>
            <dd>
                {#if notes}
                    <a href={notes} target="_blank" rel="noopener noreferrer">{about.info.version}</a>
                {:else}
                    <code>{about.info.version}</code>
                {/if}
            </dd>
            <dt>Commit</dt>
            <dd>
                {#if about.info.commit}
                    <code>{about.info.commit.slice(0, 12)}</code>
                    {#if about.info.modified}<span class="muted">with uncommitted changes</span>{/if}
                {:else}
                    <span class="muted">not stamped in this build</span>
                {/if}
            </dd>
            <dt>Started</dt>
            <dd>{new Date(about.info.started).toLocaleString()}</dd>
            <dt>State directory</dt>
            <dd><code>{about.info.state_dir}</code></dd>
        </dl>
    </section>
{/if}

<style>
.jaildeck {
    margin-top: var(--space-6);
}

.facts {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--space-2) var(--space-4);
    margin: 0;
}

.facts dt { margin: 0; color: var(--text-muted); }
.facts dd { margin: 0; overflow-wrap: anywhere; }

.muted {
    margin-left: var(--space-2);
    color: var(--text-muted);
    font-size: 0.85rem;
}
</style>
