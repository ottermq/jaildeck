<script lang="ts">
import { onMount } from 'svelte'
import { about, loadAbout, releaseNotes } from '../stores/about.svelte'

// Which Jail Deck this is, under the logo (#136): a release links to its
// notes, any other build is only named.
let notes = $derived(about.info ? releaseNotes(about.info.version) : null)

onMount(loadAbout)
</script>

{#if about.info}
    {#if notes}
        <a class="version" href={notes} target="_blank" rel="noopener noreferrer"
           title="Release notes for {about.info.version}">{about.info.version}</a>
    {:else}
        <span class="version" title="A build that is not a release">{about.info.version}</span>
    {/if}
{/if}

<style>
.version {
    font-size: 0.75rem;
    color: var(--rail-text);
    text-decoration: none;
    font-variant-numeric: tabular-nums;
    overflow-wrap: anywhere;
}

a.version:hover {
    color: var(--rail-text-strong);
    text-decoration: underline;
}
</style>
