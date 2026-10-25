<script lang="ts">
import { onMount, onDestroy } from 'svelte'
import { onNavClick } from '../router.svelte'
import { getMetrics } from '../stores/metrics.svelte'
import type { MetricsReport, Memory, JailUsage } from '../stores/metrics.svelte'
import { ApiError } from '../services/api'
import { formatBytes } from '../utils'

let report = $state<MetricsReport | null>(null)
let loadError = $state('')
let polling = $state(true)
let timer: ReturnType<typeof setInterval> | undefined

// Five seconds. The host's CPU percentage is measured between two readings,
// so the poll interval *is* the measurement window — long enough that a busy
// moment is not averaged away, short enough to feel live.
const INTERVAL = 5000

async function load() {
    try {
        report = await getMetrics()
        loadError = ''
    } catch (err) {
        loadError = err instanceof ApiError
            ? err.statusText ?? 'Could not read the host'
            : 'Could not reach Jail Deck'
    }
}

onMount(() => {
    load()
    timer = setInterval(() => {
        if (polling) load()
    }, INTERVAL)
})

onDestroy(() => {
    if (timer) clearInterval(timer)
})

// Used is everything the kernel is not holding free. Wired is shown beside
// it rather than folded in, because on a ZFS host most of it is the ARC —
// reclaimable, and alarming if it reads as pressure.
function used(memory: Memory): number {
    return memory.physical - memory.free
}

function share(part: number, whole: number): number {
    return whole > 0 ? Math.min(100, (part / whole) * 100) : 0
}

// pcpu is percent of one core, so the host's capacity is 100 × cpus.
function cpuShare(jail: JailUsage, cpus: number): number {
    return share(jail.pcpu, cpus * 100)
}

function round(value: number, digits = 1): string {
    return value.toFixed(digits)
}

// The bar beside this is a share of every core, so the figure is cores too.
// pcpu's own unit is percent of ONE core, which reads as "175%" next to a
// bar that is 11% full — the same number twice on two different bases.
function cores(pcpu: number): string {
    const used = pcpu / 100
    return `${used.toFixed(used >= 10 ? 0 : 2)} ${used === 1 ? 'core' : 'cores'}`
}

let busiest = $derived(
    [...(report?.jails ?? [])].sort((a, b) => b.pcpu - a.pcpu),
)

// Every jail read the same way, or the report says which is which. Mixed
// sources are possible only across restarts, never within one report.
let source = $derived(report?.jails[0]?.source)
</script>

<header class="page-head page-head--action">
    <div>
        <h1>Dashboard</h1>
        <p class="page-sub">
            What this host and its jails are using, read now — nothing here is stored.
        </p>
    </div>
    <label class="check">
        <input type="checkbox" bind:checked={polling} />
        Keep refreshing
    </label>
</header>

{#if loadError}
    <p class="notice notice--error" role="alert">
        {loadError}
        <button type="button" class="btn btn--small" onclick={load}>Retry</button>
    </p>
{/if}

{#if report}
    <section class="cards">
        <article class="card">
            <h2>Load</h2>
            <p class="figure">{round(report.host.load.one, 2)}</p>
            <p class="under">
                {round(report.host.load.five, 2)} over 5m ·
                {round(report.host.load.fifteen, 2)} over 15m
            </p>
            <p class="under">{report.host.cpu.cpus} CPUs</p>
        </article>

        <article class="card">
            <h2>CPU</h2>
            {#if report.host.cpu.percent === undefined}
                <p class="figure figure--muted">—</p>
                <p class="under">
                    Measured between two readings. The first after a restart has nothing
                    to measure against.
                </p>
            {:else}
                <p class="figure">{round(report.host.cpu.percent)}<span class="unit">%</span></p>
                <div class="bar"><div class="bar__fill" style={`width:${report.host.cpu.percent}%`}></div></div>
                <p class="under">over the last {round(report.host.cpu.over_seconds ?? 0)}s</p>
            {/if}
        </article>

        <article class="card">
            <h2>Memory</h2>
            <p class="figure">
                {formatBytes(used(report.host.memory))}<span class="unit">
                    of {formatBytes(report.host.memory.physical)}</span>
            </p>
            <div class="bar">
                <div class="bar__fill"
                     style={`width:${share(used(report.host.memory), report.host.memory.physical)}%`}></div>
            </div>
            <p class="under">
                wired {formatBytes(report.host.memory.wired)} ·
                active {formatBytes(report.host.memory.active)} ·
                inactive {formatBytes(report.host.memory.inactive)}
            </p>
            <p class="under under--note">
                Most of wired is the ZFS ARC, which is reclaimable.
            </p>
        </article>

        <article class="card">
            <h2>Jails</h2>
            <p class="figure">
                {report.jails.length}<span class="unit">of {report.configured} running</span>
            </p>
            <p class="under">
                <a href="/jails" onclick={(e) => onNavClick(e, '/jails')}>See them all</a>
            </p>
        </article>
    </section>

    <section class="panel">
        <h2>Pools</h2>
        {#if report.host.pools.length === 0}
            <p class="notice">This host has no ZFS pools.</p>
        {:else}
            <div class="table-wrap">
                <table>
                    <thead>
                        <tr>
                            <th>Pool</th>
                            <th>Health</th>
                            <th class="num">Used</th>
                            <th class="num">Free</th>
                            <th class="num">Size</th>
                            <th class="fill-col">Capacity</th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each report.host.pools as pool (pool.name)}
                            <tr>
                                <td class="name">{pool.name}</td>
                                <td>
                                    <span class="badge badge--{pool.health === 'ONLINE' ? 'running' : 'failed'}"
                                    >{pool.health}</span>
                                </td>
                                <td class="num">{formatBytes(pool.allocated)}</td>
                                <td class="num">{formatBytes(pool.free)}</td>
                                <td class="num">{formatBytes(pool.size)}</td>
                                <td>
                                    <div class="meter">
                                        <div class="bar bar--row">
                                            <div class="bar__fill" style={`width:${pool.capacity}%`}></div>
                                        </div>
                                        <span class="meter__value">{pool.capacity}%</span>
                                    </div>
                                </td>
                            </tr>
                        {/each}
                    </tbody>
                </table>
            </div>
        {/if}
    </section>

    <section class="panel">
        <div class="panel__head">
            <h2>Jails <span class="count">{report.jails.length} running</span></h2>
            {#if source}
                <span class="under">
                    read with <code>{source}</code>{#if source === 'ps'}, because this host is not
                    accounting — CPU, memory and a process count, and nothing else{/if}
                </span>
            {/if}
        </div>

        {#if report.jails.length === 0}
            <p class="notice">
                No jail is running.
                <a href="/jails" onclick={(e) => onNavClick(e, '/jails')}>Start one</a>.
            </p>
        {:else}
            <div class="table-wrap">
                <table>
                    <thead>
                        <tr>
                            <th>Jail</th>
                            <th class="fill-col">CPU</th>
                            <th class="num">Memory</th>
                            <th class="num">Processes</th>
                            <th class="num">Open files</th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each busiest as jail (jail.jail)}
                            <tr>
                                <td class="name">
                                    <a href={`/jails/${encodeURIComponent(jail.jail)}`}
                                       onclick={(e) => onNavClick(e, `/jails/${encodeURIComponent(jail.jail)}`)}
                                    >{jail.jail}</a>
                                </td>
                                {#if jail.error}
                                    <td colspan="4" class="row-error">{jail.error}</td>
                                {:else}
                                    <td>
                                        <div class="meter">
                                            <div class="bar bar--row">
                                                <div class="bar__fill"
                                                     style={`width:${cpuShare(jail, report.host.cpu.cpus)}%`}></div>
                                            </div>
                                            <span class="meter__value"
                                                  title={`${round(jail.pcpu)}% of one core, as racct reports it`}
                                            >{cores(jail.pcpu)}</span>
                                        </div>
                                    </td>
                                    <td class="num" title={jail.vmemoryuse
                                        ? `${formatBytes(jail.vmemoryuse)} of address space`
                                        : undefined}>{formatBytes(jail.memoryuse)}</td>
                                    <td class="num">{jail.processes}</td>
                                    <td class="num">{jail.openfiles ?? '—'}</td>
                                {/if}
                            </tr>
                        {/each}
                    </tbody>
                </table>
            </div>
            <p class="hint">
                Busiest first, and the bar is a share of all {report.host.cpu.cpus} cores.
                The underlying figure is <code>pcpu</code>, percent of <strong>one</strong>
                core — hover a row to see it. Nothing here is kept: a chart over time is what
                the Prometheus endpoint will be for.
            </p>
        {/if}
    </section>
{:else if !loadError}
    <p class="notice">Reading the host…</p>
{/if}

<style>
.page-head--action {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--space-4);
    flex-wrap: wrap;
}

.check {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    text-transform: none;
    letter-spacing: normal;
    font-weight: 400;
    font-size: 0.9rem;
    color: var(--text);
}

.cards {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
    gap: var(--space-4);
    margin-top: var(--space-5);
}

.card {
    padding: var(--space-4);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
}

.card h2 {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    margin: 0 0 var(--space-2);
}

.figure {
    font-size: 1.9rem;
    font-weight: 600;
    color: var(--text-strong);
    line-height: 1.1;
    margin: 0;
}

/* An absent measurement, not a zero one. */
.figure--muted {
    color: var(--text-muted);
}

.unit {
    font-size: 0.85rem;
    font-weight: 400;
    color: var(--text-muted);
    margin-left: 0.35em;
}

.under {
    font-size: 0.78rem;
    color: var(--text-muted);
    margin: var(--space-2) 0 0;
}

.under--note {
    font-style: italic;
}

.bar {
    margin-top: var(--space-2);
    height: 6px;
    border-radius: 3px;
    background: var(--surface-sunken);
    border: 1px solid var(--border);
    overflow: hidden;
}

.bar--row {
    margin-top: 0;
    flex: 1;
}

/* Bar and figure on one line. Stacked, the label reads as a caption of an
   empty progress bar rather than as its value. */
.meter {
    display: flex;
    align-items: center;
    gap: var(--space-2);
}

.meter__value {
    font-size: 0.78rem;
    color: var(--text-muted);
    white-space: nowrap;
    min-width: 4.5rem;
    text-align: right;
}

.bar__fill {
    height: 100%;
    background: var(--brand);
}

.fill-col {
    width: 12rem;
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
    font-weight: 400;
    font-size: 0.85rem;
    color: var(--text-muted);
}

.name {
    font-weight: 600;
    color: var(--text-strong);
}

.row-error {
    color: var(--danger);
    font-size: 0.875rem;
}

.hint {
    margin-top: var(--space-3);
    font-size: 0.85rem;
    color: var(--text-muted);
    max-width: 68ch;
}
</style>
