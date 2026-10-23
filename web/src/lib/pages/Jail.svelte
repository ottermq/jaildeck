<script lang="ts">
import { onMount } from 'svelte'
import { getJail, listSnapshots } from '../stores/jails.svelte'
import type { JailData, Snapshot } from '../stores/jails.svelte'
import { ApiError } from '../services/api'
import { onNavClick } from '../router.svelte'
import { formatBytes, normalizeEmptyValue } from '../utils'

let { name }: { name: string } = $props()

let jail = $state<JailData | null>(null)
let snapshots = $state<Snapshot[] | null>(null)
let loadError = $state('')
let snapshotError = $state('')

async function load() {
    loadError = ''
    try {
        jail = await getJail(name)
    } catch (err) {
        jail = null
        loadError = err instanceof ApiError
            ? err.statusText ?? `Could not load ${name}`
            : 'Could not reach Jail Deck'
        return
    }
    // Asked separately, and allowed to fail on its own: a jail with an
    // unreadable snapshot listing is still a jail worth showing.
    try {
        snapshots = await listSnapshots(name)
    } catch (err) {
        snapshots = []
        snapshotError = err instanceof ApiError
            ? err.statusText ?? 'Could not load snapshots'
            : 'Could not reach Jail Deck'
    }
}

onMount(load)

// The jail's own dataset is the one mounted exactly at its path; the rest are
// children. Shown apart because their sizes are not the jail's — a child with
// a refquota is counted separately on purpose (JD-007).
let root = $derived(jail?.datasets?.find(d => d.mountpoint === jail?.path))
let children = $derived(jail?.datasets?.filter(d => d !== root) ?? [])
let tags = $derived(Object.entries(jail?.properties ?? {}).sort(([a], [b]) => a.localeCompare(b)))

// Newest first: the one you are looking for is almost always the last one
// taken. The server returns them oldest first, ordered by transaction group.
let newestFirst = $derived([...(snapshots ?? [])].reverse())

function when(iso: string): string {
    return new Date(iso).toLocaleString()
}
</script>

<nav class="crumbs" aria-label="Breadcrumb">
    <a href="/jails" onclick={(e) => onNavClick(e, '/jails')}>Jails</a>
    <span aria-hidden="true">/</span>
    <span class="crumbs__here">{name}</span>
</nav>

{#if loadError}
    <p class="notice notice--error" role="alert">
        {loadError}
        <button type="button" class="btn btn--small" onclick={load}>Retry</button>
    </p>
{:else if jail === null}
    <p class="notice">Loading {name}…</p>
{:else}
    <header class="page-head">
        <h1>{jail.name}</h1>
        <p class="page-sub">
            <span class="badge badge--{jail.status}">{jail.status}</span>
            <span>JID {normalizeEmptyValue(jail.jid)}</span>
            <span>{normalizeEmptyValue(jail.hostname)}</span>
            <span>{normalizeEmptyValue(jail.ip)}</span>
        </p>
    </header>

    <section class="panel">
        <h2>Datasets</h2>
        {#if !jail.path}
            <p class="notice">
                This jail's stanza sets no <code>path</code>, so nothing ties it to ZFS.
            </p>
        {:else if !jail.datasets || jail.datasets.length === 0}
            <p class="notice">
                No dataset is mounted at <code>{jail.path}</code>. That also reads this way
                when the pool could not be read — the reason is in the service log.
            </p>
        {:else}
            <div class="table-wrap">
                <table>
                    <thead>
                        <tr>
                            <th>Dataset</th>
                            <th>Mounted at</th>
                            <th class="num">Used</th>
                            <th class="num">Referenced</th>
                            <th class="num">Available</th>
                        </tr>
                    </thead>
                    <tbody>
                        {#if root}
                            <tr>
                                <td class="name"><code>{root.name}</code> <span class="tag-note">the jail's own</span></td>
                                <td><code>{root.mountpoint}</code></td>
                                <td class="num">{formatBytes(root.used)}</td>
                                <td class="num">{formatBytes(root.referenced)}</td>
                                <td class="num">{formatBytes(root.available)}</td>
                            </tr>
                        {/if}
                        {#each children as child (child.name)}
                            <tr>
                                <td class="name"><code>{child.name}</code></td>
                                <td><code>{child.mountpoint}</code></td>
                                <td class="num">{formatBytes(child.used)}</td>
                                <td class="num">{formatBytes(child.referenced)}</td>
                                <td class="num">{formatBytes(child.available)}</td>
                            </tr>
                        {/each}
                    </tbody>
                </table>
            </div>
            {#if root && root.referenced > root.used}
                <p class="hint">
                    This jail refers to more than it uses, because it is a clone and still
                    shares its template's blocks. That is why a quota belongs on a child
                    dataset rather than here.
                </p>
            {/if}
        {/if}
    </section>

    <section class="panel">
        <h2>Tags</h2>
        {#if tags.length === 0}
            <p class="notice">
                No ZFS user properties on this jail's own dataset. An automation client sets
                these — <code>otter:instance</code> and the like — and finds its jails again
                from the listing alone.
            </p>
        {:else}
            <dl class="tags">
                {#each tags as [key, value] (key)}
                    <dt><code>{key}</code></dt>
                    <dd>{value}</dd>
                {/each}
            </dl>
            <p class="hint">
                A jail reports its own dataset's properties, never a child's — otherwise one
                jail with three children would look like four instances.
            </p>
        {/if}
    </section>

    <section class="panel">
        <h2>Snapshots {#if snapshots}<span class="count">{snapshots.length}</span>{/if}</h2>
        {#if snapshotError}
            <p class="notice notice--error" role="alert">{snapshotError}</p>
        {:else if snapshots === null}
            <p class="notice">Loading snapshots…</p>
        {:else if snapshots.length === 0}
            <p class="notice">No snapshots of this jail's datasets.</p>
        {:else}
            <div class="table-wrap">
                <table>
                    <thead>
                        <tr>
                            <th>Snapshot</th>
                            <th>Dataset</th>
                            <th>Taken</th>
                            <th class="num">Used</th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each newestFirst as snapshot (snapshot.name)}
                            <tr>
                                <td class="name"><code>@{snapshot.short}</code></td>
                                <td><code>{snapshot.dataset}</code></td>
                                <td class="nowrap">{when(snapshot.created)}</td>
                                <td class="num">{formatBytes(snapshot.used)}</td>
                            </tr>
                        {/each}
                    </tbody>
                </table>
            </div>
            <p class="hint">
                Newest first. The server orders them by transaction group rather than by
                time, because two snapshots taken in the same second share a timestamp.
            </p>
        {/if}
    </section>
{/if}

<style>
.crumbs {
    display: flex;
    gap: var(--space-2);
    align-items: baseline;
    font-size: 0.85rem;
    color: var(--text-muted);
    margin-bottom: var(--space-3);
}

.crumbs__here {
    color: var(--text-strong);
    font-weight: 600;
}

.page-sub {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
    align-items: center;
}

.panel {
    margin-top: var(--space-6);
}

.panel h2 {
    margin-bottom: var(--space-3);
}

.count {
    font-weight: 400;
    font-size: 0.85rem;
    color: var(--text-muted);
}

.name {
    font-weight: 600;
    color: var(--text-strong);
}

.tag-note {
    font-weight: 400;
    font-size: 0.75rem;
    color: var(--text-muted);
}

.tags {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--space-2) var(--space-4);
    margin: 0;
}

.tags dt { margin: 0; }
.tags dd { margin: 0; color: var(--text-strong); }

.hint {
    margin-top: var(--space-3);
    font-size: 0.85rem;
    color: var(--text-muted);
}
</style>
