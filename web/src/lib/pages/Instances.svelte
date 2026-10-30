<script lang="ts">
import { onMount } from 'svelte'
import { SvelteSet } from 'svelte/reactivity'
import { onNavClick } from '../router.svelte'
import { createSnapshot, listJails, runJailOperation } from '../stores/jails.svelte'
import { listVMs, runVMOperation } from '../stores/vms.svelte'
import type { VMBhyveState } from '../stores/vms.svelte'
import { getMetrics } from '../stores/metrics.svelte'
import { ask } from '../stores/confirm.svelte'
import { ApiError } from '../services/api'
import { formatBytes } from '../utils'
import { stateBadge } from '../vmState'
import type { VMState } from '../stores/vms.svelte'
import {
    fromJail, fromVM, grouped, labelKeys, labelTerms, noFilters, readFilters, skipReason, visible, writeFilters,
} from '../instances'
import type { BulkOperation, Filters, Instance, SortKey } from '../instances'
import { runAll } from '../bulk'
import type { Outcome } from '../bulk'

let all = $state<Instance[] | null>(null)
let loadError = $state('')
// Said rather than hidden: a list without guests because vm-bhyve is not
// configured looks exactly like a host without guests.
let vmNote = $state('')
let usageNote = $state('')

async function load() {
    const [jails, vms, metrics] = await Promise.allSettled([listJails(), listVMs(), getMetrics()])
    loadError = ''
    vmNote = ''
    usageNote = ''
    const rows: Instance[] = []
    if (jails.status === 'fulfilled') {
        const usage = new Map(metrics.status === 'fulfilled' ? metrics.value.jails.map(u => [u.jail, u]) : [])
        rows.push(...jails.value.map(j => fromJail(j, usage.get(j.name))))
        if (metrics.status === 'rejected') usageNote = 'Jail usage could not be read, so CPU and memory are blank for jails.'
    } else {
        loadError = describeLoad(jails.reason, 'Could not load jails')
    }
    if (vms.status === 'fulfilled') {
        rows.push(...vms.value.vms.map(fromVM))
        vmNote = vmBhyveNote(vms.value.vm_bhyve, vms.value.problem)
    } else {
        vmNote = describeLoad(vms.reason, 'Could not load the guests')
    }
    all = rows
}

function describeLoad(err: unknown, fallback: string): string {
    return err instanceof ApiError ? err.statusText ?? fallback : 'Could not reach Jail Deck'
}

function vmBhyveNote(state: VMBhyveState, problem?: string): string {
    if (state === 'not_configured') return `vm-bhyve is not configured, so no guests are listed: ${problem ?? ''}`
    return ''
}

onMount(load)

// ---- filters, kept in the URL ------------------------------------------

let filters = $state<Filters>(readFilters(window.location.search))

// Replaced rather than pushed: every keystroke in the search would otherwise
// be a step for Back to walk through.
$effect(() => {
    const query = writeFilters(filters)
    if (window.location.pathname === '/instances' && window.location.search !== query) {
        history.replaceState(history.state, '', `/instances${query}`)
    }
})

let shown = $derived(all ? visible(all, filters) : [])
let groups = $derived(grouped(shown, filters.group))
let keys = $derived(labelKeys(all ?? []))
let states = $derived([...new Set([...(all ?? []).map(i => i.state), filters.state].filter(Boolean))].sort())
let filtered = $derived(JSON.stringify({ ...filters, sort: 'name', desc: false }) !== JSON.stringify(noFilters))

function sortBy(key: SortKey) {
    filters.desc = filters.sort === key ? !filters.desc : false
    filters.sort = key
}

function ariaSort(key: SortKey): 'ascending' | 'descending' | 'none' {
    if (filters.sort !== key) return 'none'
    return filters.desc ? 'descending' : 'ascending'
}

function addLabelFilter(key: string, value: string) {
    const term = `${key}=${value}`
    if (labelTerms(filters.labels).some(t => t.key === key && t.value === value)) return
    filters.labels = filters.labels.trim() ? `${filters.labels.trim()}, ${term}` : term
}

// ---- selection -------------------------------------------------------------

let selected = new SvelteSet<string>()

// What a bulk action acts on is what is selected and in view: a filter that
// hides an instance takes it out of the selection, so nothing is stopped that
// the operator could not see when they pressed Stop.
$effect(() => {
    const inView = new Set(shown.map(i => i.key))
    for (const key of [...selected]) {
        if (!inView.has(key)) selected.delete(key)
    }
})

let chosen = $derived(shown.filter(i => selected.has(i.key)))

function toggle(rows: Instance[], on: boolean) {
    for (const row of rows) {
        if (on) selected.add(row.key)
        else selected.delete(row.key)
    }
}

function allOf(rows: Instance[]): boolean {
    return rows.length > 0 && rows.every(r => selected.has(r.key))
}

function someOf(rows: Instance[]): boolean {
    return rows.some(r => selected.has(r.key)) && !allOf(rows)
}

// ---- bulk actions --------------------------------------------------------

let running = $state(false)
let resultOp = $state<BulkOperation | null>(null)
let resultRows = $state<Instance[]>([])
let outcomes = $state<Record<string, Outcome>>({})
let snapshotForm = $state(false)
let snapshotName = $state('')

const verbs: Record<BulkOperation, string> = { start: 'Start', stop: 'Stop', restart: 'Restart', snapshot: 'Snapshot' }

function kindWord(i: Instance): string {
    return i.kind === 'vm' ? 'guest' : 'jail'
}

function plural(n: number, word: string): string {
    return `${n} ${word}${n === 1 ? '' : 's'}`
}

function defaultSnapshotName(): string {
    const d = new Date()
    const pad = (n: number) => String(n).padStart(2, '0')
    return `jd-${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}`
}

function openSnapshot() {
    snapshotForm = true
    snapshotName = defaultSnapshotName()
}

function confirmLines(op: BulkOperation, acting: Instance[], skipped: Instance[], short: string): string[] {
    const lines: string[] = []
    const jails = acting.some(i => i.kind === 'jail')
    const guests = acting.some(i => i.kind === 'vm')
    if (op === 'stop' || op === 'restart') {
        if (jails) lines.push('Anything running inside a jail is interrupted.')
        if (guests) lines.push('A guest is sent the ACPI power button and given a minute; one that has not stopped by then is powered off, and loses what it had not written.')
    }
    if (op === 'snapshot') {
        lines.push(`Each jail's own dataset and its children, as @${short}, at one point in time per jail. A jail that already has @${short} is left as it is.`)
    }
    if (skipped.length > 0) {
        lines.push(`${plural(skipped.length, 'selected instance')} ${skipped.length === 1 ? 'is' : 'are'} left alone; the results say why.`)
    }
    lines.push('A few run at a time, one request each, and every one is audited. Each result is shown as it finishes.')
    return lines
}

class Blocked extends Error {}

async function work(i: Instance, op: BulkOperation, short: string): Promise<{ detail?: string, warn?: boolean } | void> {
    if (op === 'snapshot') {
        const r = await createSnapshot(i.name, '', short)
        if (r.blocked) throw new Blocked(r.blocked.reason)
        if (r.existed) return { detail: `${r.snapshot} was already there; nothing was taken.` }
        const children = r.created.length - 1
        return { detail: children > 0 ? `${r.snapshot}, and the same on ${plural(children, 'child dataset')}` : r.snapshot }
    }
    if (i.kind === 'jail') {
        await runJailOperation(i.name, op)
        return
    }
    const r = await runVMOperation(i.name, op)
    if (r.forced) return { detail: 'Did not answer the power button within a minute and was powered off.', warn: true }
}

function describe(err: unknown): string {
    if (err instanceof Blocked) return err.message
    if (!(err instanceof ApiError)) return 'Could not reach Jail Deck'
    if (err.statusCode === 504) return 'Timed out; it may still be under way.'
    return err.statusText ?? 'That did not work'
}

async function bulk(op: BulkOperation) {
    if (running || chosen.length === 0) return
    const short = snapshotName.trim()
    const targets = [...chosen]
    const acting = targets.filter(i => skipReason(i, op) === null)
    const skipped = targets.filter(i => skipReason(i, op) !== null)

    if (acting.length > 0) {
        const confirmed = await ask({
            title: op === 'snapshot'
                ? `Snapshot ${plural(acting.length, 'jail')} as @${short}?`
                : `${verbs[op]} ${plural(acting.length, 'instance')}?`,
            lines: confirmLines(op, acting, skipped, short),
            items: acting.map(i => `${i.name} (${kindWord(i)})`),
            verb: verbs[op],
        })
        if (!confirmed) return
    }

    snapshotForm = false
    resultOp = op
    resultRows = targets
    outcomes = Object.fromEntries(skipped.map(i => [i.key, { status: 'skipped', detail: skipReason(i, op)! } as Outcome]))
    if (acting.length === 0) return

    running = true
    try {
        await runAll(acting, i => work(i, op, short), (i, outcome) => { outcomes[i.key] = outcome }, describe)
    } finally {
        running = false
        // Read again however it went: a failed command can still have changed
        // an instance, and the host is the only answer that cannot disagree.
        await load()
    }
}

let tally = $derived.by(() => {
    const counts: Record<string, number> = {}
    for (const o of Object.values(outcomes)) counts[o.status] = (counts[o.status] ?? 0) + 1
    return counts
})

const outcomeBadge: Record<Outcome['status'], string> = {
    queued: 'stopped',
    running: 'timed-out',
    done: 'succeeded',
    failed: 'failed',
    skipped: 'stopped',
}

function badgeFor(i: Instance): string {
    return i.kind === 'vm' ? stateBadge[i.state as VMState] ?? 'stopped' : i.state
}

function labelEntries(i: Instance): [string, string][] {
    return Object.entries(i.labels).sort(([a], [b]) => a.localeCompare(b))
}
</script>

<header class="page-head">
    <h1>Instances</h1>
    <p class="page-sub">Every jail and guest on this host in one list, to find by name, state or label and act on several at once.</p>
</header>

{#if loadError}
    <p class="notice notice--error" role="alert">
        {loadError}
        <button type="button" class="btn btn--small" onclick={load}>Retry</button>
    </p>
{/if}
{#if vmNote}
    <p class="notice notice--warn">{vmNote}</p>
{/if}

<form class="filters" role="search" onsubmit={e => e.preventDefault()}>
    <div class="field field--grow">
        <label for="f-q">Name</label>
        <input id="f-q" type="search" bind:value={filters.q} placeholder="Search" autocomplete="off" spellcheck="false" />
    </div>
    <div class="field">
        <label for="f-kind">Kind</label>
        <select id="f-kind" bind:value={filters.kind}>
            <option value="">Jails and guests</option>
            <option value="jail">Jails</option>
            <option value="vm">Guests</option>
        </select>
    </div>
    <div class="field">
        <label for="f-state">State</label>
        <select id="f-state" bind:value={filters.state}>
            <option value="">Any</option>
            {#each states as state (state)}
                <option value={state}>{state}</option>
            {/each}
        </select>
    </div>
    <div class="field field--grow">
        <label for="f-labels">Labels</label>
        <input id="f-labels" bind:value={filters.labels} placeholder="otter:tenant=acme, otter:tier"
               autocomplete="off" spellcheck="false" />
    </div>
    <div class="field">
        <label for="f-group">Group by</label>
        <select id="f-group" bind:value={filters.group}>
            <option value="">Nothing</option>
            {#each keys as key (key)}
                <option value={key}>{key}</option>
            {/each}
            {#if filters.group && !keys.includes(filters.group)}
                <option value={filters.group}>{filters.group}</option>
            {/if}
        </select>
    </div>
    {#if filtered}
        <button type="button" class="btn btn--small filters__clear"
                onclick={() => (filters = { ...noFilters, sort: filters.sort, desc: filters.desc })}>Clear</button>
    {/if}
</form>
<p class="hint">
    Labels: comma-separated, every one must hold — <code>key=value</code> for that value, a bare
    <code>key</code> for any. Click a label in the list to add it. The filters are in the address, so a view can be bookmarked.
</p>

{#if chosen.length > 0 || running}
    <div class="bulk" role="region" aria-label="Selected instances">
        <span class="bulk__count">{chosen.length} selected</span>
        <div class="bulk__actions">
            <button type="button" class="btn btn--small btn--primary" disabled={running} onclick={() => bulk('start')}>Start</button>
            <button type="button" class="btn btn--small btn--danger" disabled={running} onclick={() => bulk('stop')}>Stop</button>
            <button type="button" class="btn btn--small btn--danger" disabled={running} onclick={() => bulk('restart')}>Restart</button>
            <button type="button" class="btn btn--small" disabled={running || snapshotForm} onclick={openSnapshot}>Snapshot…</button>
            <button type="button" class="btn btn--small" disabled={running} onclick={() => selected.clear()}>Clear selection</button>
        </div>
        {#if snapshotForm}
            <form class="bulk__snapshot" onsubmit={e => { e.preventDefault(); bulk('snapshot') }}>
                <label for="bulk-snapshot">Snapshot name</label>
                <input id="bulk-snapshot" bind:value={snapshotName} autocomplete="off" spellcheck="false" required />
                <button type="submit" class="btn btn--small btn--primary" disabled={running || !snapshotName.trim()}>Snapshot</button>
                <button type="button" class="btn btn--small" onclick={() => (snapshotForm = false)}>Cancel</button>
            </form>
        {/if}
    </div>
{/if}

{#if resultOp}
    <section class="panel results" aria-live="polite">
        <div class="panel__head">
            <h2>
                {verbs[resultOp]} of {plural(resultRows.length, 'instance')}
                <span class="count">
                    {#each Object.entries(tally) as [status, n], index (status)}{index > 0 ? ' · ' : ''}{n} {status}{/each}
                </span>
            </h2>
            <button type="button" class="btn btn--small" disabled={running} onclick={() => (resultOp = null)}>Dismiss</button>
        </div>
        <div class="table-wrap">
            <table>
                <thead>
                    <tr><th>Instance</th><th>Result</th><th>Detail</th></tr>
                </thead>
                <tbody>
                    {#each resultRows as i (i.key)}
                        {@const o = outcomes[i.key]}
                        <tr>
                            <td class="name">{i.name} <span class="kind">{kindWord(i)}</span></td>
                            <td><span class="badge badge--{o && 'warn' in o && o.warn ? 'warn' : outcomeBadge[o?.status ?? 'queued']}">{o?.status ?? 'queued'}</span></td>
                            <td class:detail--failed={o?.status === 'failed'}>{o && 'detail' in o ? o.detail ?? '' : ''}</td>
                        </tr>
                    {/each}
                </tbody>
            </table>
        </div>
    </section>
{/if}

{#if usageNote}
    <p class="notice">{usageNote}</p>
{/if}

{#if all === null && !loadError}
    <p class="notice">Loading instances…</p>
{:else if all !== null && all.length === 0}
    <p class="notice">
        No jails or guests on this host.
        <a href="/jails/new" onclick={e => onNavClick(e, '/jails/new')}>Create a jail</a>
        or <a href="/vms" onclick={e => onNavClick(e, '/vms')}>a guest</a>.
    </p>
{:else if all !== null && shown.length === 0}
    <p class="notice">None of the {plural(all.length, 'instance')} match these filters.</p>
{:else if all !== null}
    <p class="shown">Showing {shown.length} of {all.length}</p>
    {#each groups as group (group.value ?? '\u0000')}
        <section class="group">
            {#if filters.group}
                <h2 class="group__head">
                    {#if group.value !== null}
                        <code>{filters.group}={group.value}</code>
                    {:else}
                        No <code>{filters.group}</code>
                    {/if}
                    <span class="count">{group.rows.length}</span>
                </h2>
            {/if}
            <div class="table-wrap">
                <table>
                    <thead>
                        <tr>
                            <th class="check">
                                <input type="checkbox" checked={allOf(group.rows)} indeterminate={someOf(group.rows)}
                                       aria-label={filters.group ? `Select every instance in this group` : 'Select every instance shown'}
                                       onchange={e => toggle(group.rows, e.currentTarget.checked)} />
                            </th>
                            <th aria-sort={ariaSort('state')}><button type="button" class="sort" onclick={() => sortBy('state')}>State</button></th>
                            <th aria-sort={ariaSort('kind')}><button type="button" class="sort" onclick={() => sortBy('kind')}>Kind</button></th>
                            <th aria-sort={ariaSort('name')}><button type="button" class="sort" onclick={() => sortBy('name')}>Name</button></th>
                            <th>Labels</th>
                            <th class="num" title="A jail's is what it is using, in percent of one core; a guest's is its vCPU count">CPU</th>
                            <th class="num" title="A jail's is what it is using; a guest's is what it was given">Memory</th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each group.rows as i (i.key)}
                            <tr class:is-selected={selected.has(i.key)}>
                                <td class="check">
                                    <input type="checkbox" checked={selected.has(i.key)} aria-label={`Select ${i.name}`}
                                           onchange={e => toggle([i], e.currentTarget.checked)} />
                                </td>
                                <td><span class="badge badge--{badgeFor(i)}">{i.state}</span></td>
                                <td class="kind">{kindWord(i)}</td>
                                <td class="name">
                                    <a href={i.href} onclick={e => onNavClick(e, i.href)}>{i.name}</a>
                                </td>
                                <td>
                                    <div class="props">
                                        {#each labelEntries(i) as [key, value] (key)}
                                            <button type="button" class="prop" title={`Show only ${key}=${value}`}
                                                    onclick={() => addLabelFilter(key, value)}>{key}={value}</button>
                                        {/each}
                                    </div>
                                </td>
                                <td class="num nowrap">
                                    {#if i.usage}
                                        {i.usage.pcpu.toFixed(1)}%
                                    {:else if i.vcpus}
                                        {i.vcpus} <span class="unit">vCPU</span>
                                    {:else}—{/if}
                                </td>
                                <td class="num nowrap">
                                    {#if i.usage}
                                        {formatBytes(i.usage.memory)} <span class="unit">in use</span>
                                    {:else if i.memory}
                                        {i.memory} <span class="unit">given</span>
                                    {:else}—{/if}
                                </td>
                            </tr>
                        {/each}
                    </tbody>
                </table>
            </div>
        </section>
    {/each}
{/if}

<style>
.filters {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    align-items: flex-end;
}

.field {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
}

.field--grow {
    flex: 1 1 12rem;
}

.filters__clear {
    margin-bottom: 0.2rem;
}

.hint {
    margin-top: var(--space-2);
    font-size: 0.85rem;
    color: var(--text-muted);
}

.notice--warn {
    color: var(--warn);
    background: var(--warn-soft);
    border-color: color-mix(in srgb, var(--warn) 35%, transparent);
}

/* Stays in view while scrolling a long list: the selection is what the page
   is about once there is one. */
.bulk {
    position: sticky;
    top: 0;
    z-index: 2;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    align-items: center;
    margin-top: var(--space-4);
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    background: var(--surface-sunken);
}

.bulk__count {
    font-weight: 600;
    color: var(--text-strong);
}

.bulk__actions,
.bulk__snapshot {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    align-items: center;
}

.bulk__snapshot {
    flex-basis: 100%;
}

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

.count {
    margin-left: var(--space-2);
    font-weight: 400;
    font-size: 0.85rem;
    color: var(--text-muted);
}

.shown {
    margin-top: var(--space-4);
    font-size: 0.85rem;
    color: var(--text-muted);
}

.group {
    margin-top: var(--space-4);
}

.group__head {
    font-size: 1rem;
    margin-bottom: var(--space-2);
}

/* Fixed widths so the columns line up from one group to the next; each group
   is its own table. Wide enough to scroll inside its wrapper on a phone
   rather than squeeze. */
.group table {
    table-layout: fixed;
    min-width: 46rem;
}

.group th:nth-child(1) { width: 2.75rem; }
.group th:nth-child(2) { width: 8rem; }
.group th:nth-child(3) { width: 5rem; }
.group th:nth-child(4) { width: 28%; }
.group th:nth-child(6) { width: 6.5rem; }
.group th:nth-child(7) { width: 8.5rem; }

.name {
    overflow-wrap: anywhere;
}

.sort {
    padding: 0;
    border: 0;
    background: none;
    font: inherit;
    color: inherit;
    text-transform: inherit;
    letter-spacing: inherit;
    cursor: pointer;
}

th[aria-sort='ascending'] .sort::after { content: ' ↑'; }
th[aria-sort='descending'] .sort::after { content: ' ↓'; }

.name {
    font-weight: 600;
    color: var(--text-strong);
}

.kind,
.unit {
    font-size: 0.8rem;
    font-weight: 400;
    color: var(--text-muted);
}

.props {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1, 0.25rem);
}

.prop {
    font-family: var(--font-mono, monospace);
    font-size: 0.75rem;
    padding: 0 0.35em;
    border: 0;
    border-radius: 3px;
    background: var(--surface-2, rgba(127, 127, 127, 0.15));
    color: var(--text-muted, inherit);
    cursor: pointer;
}

.prop:hover {
    color: var(--text-strong);
}

tr.is-selected td {
    background: var(--surface-sunken);
}

.detail--failed {
    color: var(--danger);
}
</style>
