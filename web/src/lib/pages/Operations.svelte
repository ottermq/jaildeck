<script lang="ts">
import { onMount } from 'svelte'
import { listOperations, getAuditOutcome, outcomeToQuery } from '../stores/operations.svelte'
import type { AuditEntry, AuditQuery, Outcome } from '../stores/operations.svelte'
import type { Operation } from '../stores/jails.svelte'
import { ApiError } from '../services/api'
import { capitalizeFirstLetter, normalizeEmptyValue } from '../utils'

const LIMITS = [50, 100, 200]

let ops = $state<AuditEntry[]>([])
let loadError = $state('')
let loading = $state(true)

let targetOptions = $state<string[]>([])
let selectedTarget = $state<string | undefined>(undefined)
let selectedOperation = $state<Operation | undefined>(undefined)
let selectedOutcome = $state<Outcome | undefined>(undefined)
let selectedLimit = $state<number>(LIMITS[0])

// The query that produced the rows on screen, as opposed to the current state
// of the controls: the empty state has to say whether a filter is responsible,
// and the controls may have been changed without being applied.
let applied = $state<AuditQuery | null>(null)

// The limit is not a filter — it bounds the window, it does not exclude
// anything — so an empty result under a limit alone is still "nothing recorded".
let isFiltered = $derived(
    applied !== null && (
        (applied.targets?.length ?? 0) > 0 ||
        applied.operation !== undefined ||
        applied.success !== undefined ||
        applied.timedOut !== undefined
    )
)

// Whether the most recent request filled its window. Once pages are appended
// ops.length no longer says anything about whether more remain, so this tracks
// the last page rather than the total. A full page may be exactly the last one,
// in which case "Load older" returns nothing and hides itself — one wasted
// request, which is the price of the API not reporting a total.
let lastPageFull = $state(false)
let loadingMore = $state(false)

async function load() {
    loadError = ''
    loading = true
    try {
        const page = await listOperations(applied)
        ops = page
        lastPageFull = page.length >= (applied?.limit ?? selectedLimit)
    } catch (err) {
        loadError = err instanceof ApiError
            ? err.statusText ?? 'Could not load operations'
            : 'Could not reach Jail Deck'
    } finally {
        loading = false
    }
}

// Targets come from a wider window than the table shows, so the picker still
// offers a jail whose last operation has scrolled past the current limit.
async function loadTargetOptions() {
    try {
        const recent = await listOperations({ limit: 200 })
        targetOptions = [...new Set(recent.map(e => e.target))].sort()
    } catch {
        // A failed picker is not worth its own error: load() reports the same
        // outage, and the table is the part that matters.
    }
}

onMount(() => {
    loadTargetOptions()
    applied = { limit: selectedLimit }
    load()
})

async function applyFilters(e: SubmitEvent) {
    e.preventDefault()
    applied = {
        targets: selectedTarget ? [selectedTarget] : undefined,
        operation: selectedOperation,
        limit: selectedLimit,
        ...outcomeToQuery(selectedOutcome),
    }
    await load()
}

async function clearFilters() {
    selectedTarget = undefined
    selectedOperation = undefined
    selectedOutcome = undefined
    selectedLimit = LIMITS[0]
    applied = { limit: selectedLimit }
    await load()
}

// The cursor is the oldest row on screen, which is the last one: the API
// returns newest first.
async function loadOlder() {
    if (loadingMore || ops.length === 0) return
    loadingMore = true
    loadError = ''
    try {
        const page = await listOperations({ ...applied, before: ops[ops.length - 1].timestamp })
        ops = [...ops, ...page]
        lastPageFull = page.length >= (applied?.limit ?? selectedLimit)
    } catch (err) {
        loadError = err instanceof ApiError
            ? err.statusText ?? 'Could not load older operations'
            : 'Could not reach Jail Deck'
    } finally {
        loadingMore = false
    }
}

function formatTime(iso: string): string {
    const d = new Date(iso)
    return isNaN(d.getTime()) ? iso : d.toLocaleString()
}

// -1 is the sentinel for "no exit code": the command never ran, or Jail Deck
// stopped waiting for it.
function formatExitCode(code: number): string {
    return code === -1 ? '—' : String(code)
}
</script>

<header class="page-head">
    <h1>Operations</h1>
    <p class="page-sub">What Jail Deck has run, newest first.</p>
</header>

<form class="filters" onsubmit={applyFilters}>
    <div class="field">
        <label for="f-target">Target</label>
        <select id="f-target" bind:value={selectedTarget}>
            <option value={undefined}>Any</option>
            {#each targetOptions as t (t)}
                <option value={t}>{t}</option>
            {/each}
        </select>
    </div>

    <div class="field">
        <label for="f-operation">Operation</label>
        <select id="f-operation" bind:value={selectedOperation}>
            <option value={undefined}>Any</option>
            <option value="start">Start</option>
            <option value="stop">Stop</option>
            <option value="restart">Restart</option>
        </select>
    </div>

    <div class="field">
        <label for="f-outcome">Outcome</label>
        <select id="f-outcome" bind:value={selectedOutcome}>
            <option value={undefined}>Any</option>
            <option value="succeeded">Succeeded</option>
            <option value="failed">Failed</option>
            <option value="timed out">Timed out</option>
        </select>
    </div>

    <div class="field">
        <label for="f-limit">Show</label>
        <select id="f-limit" bind:value={selectedLimit}>
            {#each LIMITS as n (n)}
                <option value={n}>{n}</option>
            {/each}
        </select>
    </div>

    <div class="filters__actions">
        <button type="submit" class="btn btn--primary">Filter</button>
        <button type="button" class="btn" onclick={clearFilters}>Clear</button>
    </div>
</form>

{#if loadError}
    <p class="notice notice--error" role="alert">
        {loadError}
        <button type="button" class="btn btn--small" onclick={load}>Retry</button>
    </p>
{/if}

{#if ops.length > 0}
    <div class="table-wrap">
        <table>
            <thead>
                <tr>
                    <th>Time</th>
                    <th>Target</th>
                    <th>Operation</th>
                    <th>Outcome</th>
                    <th class="num">Exit</th>
                    <th>Command</th>
                    <th>Detail</th>
                </tr>
            </thead>
            <tbody>
                {#each ops as op (op.timestamp + op.target + op.operation)}
                    {@const result = getAuditOutcome(op)}
                    <tr>
                        <td class="nowrap">{formatTime(op.timestamp)}</td>
                        <td>{op.target}</td>
                        <td>{capitalizeFirstLetter(op.operation)}</td>
                        <td>
                            <span class="badge badge--{result.replace(' ', '-')}">
                                {capitalizeFirstLetter(result)}
                            </span>
                        </td>
                        <td class="num">{formatExitCode(op.exit_code)}</td>
                        <td>{#if op.command}<code>{op.command}</code>{:else}—{/if}</td>
                        <td class="detail">{normalizeEmptyValue(op.error ?? '')}</td>
                    </tr>
                {/each}
            </tbody>
        </table>
    </div>

    <div class="table-foot">
        <span>{ops.length} operation{ops.length === 1 ? '' : 's'}</span>
        {#if lastPageFull}
            <button type="button" class="btn btn--small" disabled={loadingMore} onclick={loadOlder}>
                {loadingMore ? 'Loading…' : 'Load older'}
            </button>
        {/if}
    </div>
{:else if loading}
    <p class="notice">Loading operations…</p>
{:else if !loadError}
    {#if isFiltered}
        <p class="notice">No operations match these filters.</p>
    {:else}
        <p class="notice">No operations have been recorded yet.</p>
    {/if}
{/if}

<style>
.filters {
    display: flex;
    flex-wrap: wrap;
    align-items: end;
    gap: var(--space-3);
    padding: var(--space-3) var(--space-4);
    margin-bottom: var(--space-5);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
}

.field {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
}

.field select {
    min-width: 9rem;
}

.filters__actions {
    display: flex;
    gap: var(--space-2);
}

.detail {
    max-width: 28rem;
    color: var(--text-muted);
}

.table-foot {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    margin-top: var(--space-3);
    font-size: 0.85rem;
    color: var(--text-muted);
}
</style>
