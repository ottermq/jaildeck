<script lang="ts">
import { onMount } from 'svelte'
import { getReclaimable, reclaim } from '../stores/host.svelte'
import type { Reclaimable, ReclaimableItem, ReclaimableKind } from '../stores/host.svelte'
import { ask } from '../stores/confirm.svelte'
import { ApiError } from '../services/api'
import { formatBytes } from '../utils'

// What storage nothing uses holds (#125, JD-049). The report says how each
// thing is removed; removing is the existing endpoints' work, one request
// each, so their refusals are the ones that apply.

const kinds: { kind: ReclaimableKind, title: string, note: string }[] = [
    { kind: 'template', title: 'Templates', note: 'Nothing is cloned from these and nothing holds them.' },
    { kind: 'image', title: 'VM images', note: "No guest's disk is cloned from these." },
    { kind: 'retained', title: 'Retained datasets', note: 'Each was kept past its jail on purpose, so each is removed on its own.' },
    { kind: 'snapshot', title: 'Snapshots', note: '' },
    { kind: 'archive', title: 'Downloaded archives', note: 'Preparing again downloads them again. Jail Deck does not remove files; delete one by hand if you want the space.' },
]

// Removing every retained dataset at once would be removing what someone
// chose to keep without looking at each.
const bulk: Record<ReclaimableKind, boolean> = {
    template: true, image: true, retained: false, snapshot: true, archive: false,
}

let report = $state<Reclaimable | null>(null)
let loadError = $state('')
let loading = $state(true)
let age = $state(30)
let busy = $state('')
// What each removal answered, by item: an error, or nothing once it worked.
let results = $state<Record<string, string>>({})

let groups = $derived(kinds
    .map(k => ({ ...k, items: (report?.items ?? []).filter(i => i.kind === k.kind) }))
    .filter(g => g.items.length > 0))
let skipped = $derived(Object.entries(report?.skipped_snapshots ?? {}))
let skippedCount = $derived(skipped.reduce((sum, [, n]) => sum + n, 0))

function key(item: ReclaimableItem): string {
    return `${item.kind} ${item.name}`
}

function describe(err: unknown): string {
    if (!(err instanceof ApiError)) return 'Could not reach Jail Deck'
    return err.statusText ?? 'That did not work'
}

async function load() {
    loadError = ''
    loading = true
    try {
        report = await getReclaimable(age)
    } catch (err) {
        loadError = describe(err)
    } finally {
        loading = false
    }
}

onMount(load)

// One request after another, never at once: a snapshot destroy and a
// template removal are each a zfs destroy, and the host does them in turn
// anyway. A refusal stops nothing else; each item says how it went.
async function remove(items: ReclaimableItem[], title: string) {
    const removable = items.filter(i => i.remove)
    if (busy || removable.length === 0) return
    const size = removable.reduce((sum, i) => sum + i.size, 0)
    const confirmed = await ask({
        title,
        lines: [
            `This gives back about ${formatBytes(size)}. It cannot be undone.`,
            'Each one is removed by its own endpoint, which still refuses anything that has come to depend on it since this list was read.',
        ],
        items: removable.map(i => i.name),
        verb: 'Remove',
    })
    if (!confirmed) return
    busy = title
    for (const item of removable) {
        try {
            await reclaim(item)
            results[key(item)] = ''
        } catch (err) {
            results[key(item)] = describe(err)
        }
    }
    busy = ''
    const failed = Object.fromEntries(Object.entries(results).filter(([, v]) => v))
    await load()
    results = failed
}
</script>

<section class="reclaimable" aria-labelledby="reclaimable-title">
    <header class="reclaimable__head">
        <div>
            <h2 id="reclaimable-title">Reclaimable storage</h2>
            <p class="reclaimable__sub">What storage nothing uses holds. Nothing here is removed until you say so.</p>
        </div>
        <div class="reclaimable__controls">
            <label>
                Snapshots older than
                <input type="number" min="1" max="3650" bind:value={age} onchange={load} aria-label="Snapshot age in days" />
                days
            </label>
            <button type="button" class="btn btn--small" disabled={loading} onclick={load}>
                {loading ? 'Reading…' : 'Refresh'}
            </button>
        </div>
    </header>

    {#if loadError}
        <p class="notice notice--error" role="alert">{loadError}</p>
    {/if}

    {#if report}
        {#each report.problems as problem (problem)}
            <p class="notice notice--error">Could not read {problem}</p>
        {/each}

        <p class="reclaimable__total">
            {#if report.items.length === 0}
                Nothing unused was found.
            {:else}
                About <strong>{formatBytes(report.total)}</strong> in {report.items.length}
                {report.items.length === 1 ? 'thing' : 'things'} nothing uses.
            {/if}
        </p>

        {#each groups as group (group.kind)}
            {@const removable = group.items.filter(i => i.remove)}
            <div class="group">
                <div class="group__head">
                    <h3>{group.title}</h3>
                    <span class="group__size">{formatBytes(report.kinds[group.kind] ?? 0)}</span>
                    {#if bulk[group.kind] && removable.length > 1}
                        <button type="button" class="btn btn--small btn--danger" disabled={!!busy}
                            onclick={() => remove(removable, `Remove these ${removable.length} ${group.title.toLowerCase()}?`)}>
                            Remove all {removable.length}
                        </button>
                    {/if}
                </div>
                {#if group.kind === 'snapshot'}
                    <p class="group__note">Snapshots of a jail's own dataset older than {report.snapshot_age_days} days, each with its children's of the same name.</p>
                {:else}
                    <p class="group__note">{group.note}</p>
                {/if}
                <div class="table-wrap">
                    <table>
                        <thead>
                            <tr><th>Name</th><th>Size</th><th>Why</th><th></th></tr>
                        </thead>
                        <tbody>
                            {#each group.items as item (key(item))}
                                <tr>
                                    <td><code>{item.name}</code></td>
                                    <td class="num">{formatBytes(item.size)}</td>
                                    <td>
                                        {item.why}
                                        {#if results[key(item)]}
                                            <span class="refusal" role="alert">{results[key(item)]}</span>
                                        {/if}
                                    </td>
                                    <td class="action">
                                        {#if item.remove}
                                            <button type="button" class="btn btn--small" disabled={!!busy}
                                                aria-label="Remove {item.name}"
                                                onclick={() => remove([item], `Remove ${item.name}?`)}>
                                                Remove
                                            </button>
                                        {/if}
                                    </td>
                                </tr>
                            {/each}
                        </tbody>
                    </table>
                </div>
            </div>
        {/each}

        {#if skipped.length > 0}
            <p class="group__note">
                Not listed: {skippedCount} old {skippedCount === 1 ? 'snapshot' : 'snapshots'} that another tool
                takes and prunes ({skipped.map(([prefix, n]) => `${n} ${prefix}…`).join(', ')}). Removing one of those breaks that tool.
            </p>
        {/if}
    {:else if loading}
        <p class="notice">Reading what is unused…</p>
    {/if}
</section>

<style>
.reclaimable {
    margin-top: var(--space-5);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    padding: var(--space-4);
}

.reclaimable__head {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    flex-wrap: wrap;
    gap: var(--space-3);
}

.reclaimable__sub, .group__note {
    margin-top: var(--space-1);
    font-size: 0.9rem;
    color: var(--text-muted);
}

.reclaimable__controls {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-3);
    font-size: 0.9rem;
}

.reclaimable__controls input {
    width: 5em;
    margin: 0 var(--space-1);
}

.reclaimable__total {
    margin-top: var(--space-3);
    color: var(--text-strong);
}

.group {
    margin-top: var(--space-4);
    padding-top: var(--space-3);
    border-top: 1px solid var(--border);
}

.group__head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-3);
}

.group__size {
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
}

.group__head .btn {
    margin-left: auto;
}

.table-wrap {
    margin-top: var(--space-2);
}

code {
    overflow-wrap: anywhere;
}

.num {
    text-align: right;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
}

.action {
    text-align: right;
}

.refusal {
    display: block;
    margin-top: var(--space-1);
    color: var(--danger);
}
</style>
