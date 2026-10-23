<script lang="ts">
import { onMount } from 'svelte'
import { listTemplates, listRetained, lineage } from '../stores/templates.svelte'
import type { Template, RetainedDataset } from '../stores/templates.svelte'
import { ApiError } from '../services/api'
import { onNavClick } from '../router.svelte'
import { formatBytes } from '../utils'

let templates = $state<Template[] | null>(null)
let retained = $state<RetainedDataset[] | null>(null)
let loadError = $state('')

async function load() {
    loadError = ''
    try {
        templates = await listTemplates()
        retained = await listRetained()
    } catch (err) {
        loadError = err instanceof ApiError
            ? err.statusText ?? 'Could not load templates'
            : 'Could not reach Jail Deck'
    }
}

onMount(load)

// Parent before child, with a depth to indent by. The indentation is the tree:
// a host has a handful of templates two or three deep, which an indented table
// carries without the expand, collapse and selection state a tree widget adds.
let rows = $derived(templates ? lineage(templates) : [])

function href(name: string): string {
    return `/templates/${encodeURIComponent(name)}`
}
</script>

<header class="page-head">
    <h1>Templates</h1>
    <p class="page-sub">The userlands a jail is cloned from, and what each one came from.</p>
</header>

{#if loadError}
    <p class="notice notice--error" role="alert">
        {loadError}
        <button type="button" class="btn btn--small" onclick={load}>Retry</button>
    </p>
{/if}

{#if templates !== null && templates.length > 0}
    <div class="table-wrap">
        <table>
            <thead>
                <tr>
                    <th>Template</th>
                    <th>Ready</th>
                    <th>Derived from</th>
                    <th class="num">Used</th>
                    <th class="num">Referenced</th>
                </tr>
            </thead>
            <tbody>
                {#each rows as { template, depth } (template.name)}
                    <tr>
                        <td class="name" style="padding-left: calc(var(--space-3) + {depth} * 1.25rem)">
                            {#if depth > 0}<span class="lineage" aria-hidden="true">└</span>{/if}
                            <a href={href(template.name)} onclick={(e) => onNavClick(e, href(template.name))}>
                                {template.name}
                            </a>
                        </td>
                        <td>
                            {#if template.ready}
                                <span class="badge badge--running">ready</span>
                            {:else}
                                <span class="badge badge--stopped">not ready</span>
                            {/if}
                        </td>
                        <td>{#if template.parent}<code>{template.parent}</code>{:else}—{/if}</td>
                        <td class="num">{formatBytes(template.used)}</td>
                        <td class="num">{formatBytes(template.referenced)}</td>
                    </tr>
                {/each}
            </tbody>
        </table>
    </div>
    <p class="hint">
        A template is usable only once its <code>@base</code> snapshot exists. One that is
        <em>not ready</em> is a derive that failed part way through — it can be removed and
        tried again. <code>Used</code> is what a derived template costs; the userland it
        shares with its parent is in <code>Referenced</code>.
    </p>
{:else if templates !== null && !loadError}
    <p class="notice">
        No templates on this host. One arrives by preparing a release, or by configuring a
        jail and cloning it.
    </p>
{:else if !loadError}
    <p class="notice">Loading templates…</p>
{/if}

<section class="panel">
    <h2>Retained datasets</h2>
    {#if retained !== null && retained.length > 0}
        <div class="table-wrap">
            <table>
                <thead>
                    <tr>
                        <th>Name</th>
                        <th>Dataset</th>
                        <th class="num">Used</th>
                    </tr>
                </thead>
                <tbody>
                    {#each retained as dataset (dataset.name)}
                        <tr>
                            <td class="name">{dataset.short}</td>
                            <td><code>{dataset.name}</code></td>
                            <td class="num">{formatBytes(dataset.used)}</td>
                        </tr>
                    {/each}
                </tbody>
            </table>
        </div>
        <p class="hint">
            Datasets kept past the jails that held them. Destroying a jail takes its children
            with it, so anything worth keeping is moved out first.
        </p>
    {:else if retained !== null}
        <p class="notice">Nothing retained. Datasets appear here when they outlive their jail.</p>
    {/if}
</section>

<style>
.name {
    font-weight: 600;
    color: var(--text-strong);
}

.lineage {
    color: var(--text-muted);
    margin-right: var(--space-2);
    font-weight: 400;
}

.panel {
    margin-top: var(--space-6);
}

.panel h2 {
    margin-bottom: var(--space-3);
}

.hint {
    margin-top: var(--space-3);
    font-size: 0.85rem;
    color: var(--text-muted);
}
</style>
