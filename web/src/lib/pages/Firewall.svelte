<script lang="ts">
import { onMount } from 'svelte'
import { getFirewall, getFirewallTable, getFirewallLog } from '../stores/firewall.svelte'
import type { FirewallReport, Rule, TableContents, LogReport } from '../stores/firewall.svelte'
import { ApiError } from '../services/api'
import { formatBytes } from '../utils'

let report = $state<FirewallReport | null>(null)
let log = $state<LogReport | null>(null)
let loadError = $state('')
let logError = $state('')
let loading = $state(false)

// Opened tables, keyed by name. Read when opened rather than with the
// report: a blocklist table can hold a hundred thousand entries.
let opened = $state<Record<string, TableContents | 'loading' | { error: string }>>({})

function message(err: unknown, fallback: string): string {
    return err instanceof ApiError ? (err.statusText ?? fallback) : 'Could not reach Jail Deck'
}

async function load() {
    loading = true
    try {
        report = await getFirewall()
        loadError = ''
    } catch (err) {
        loadError = message(err, 'Could not read the firewall')
    }
    // The log is its own read, and failing it costs the log and nothing else.
    if (report && report.state !== 'not_loaded') {
        try {
            log = await getFirewallLog()
            logError = ''
        } catch (err) {
            logError = message(err, 'Could not read pflog')
        }
    } else {
        log = null
    }
    loading = false
}

onMount(load)

async function toggleTable(name: string) {
    if (opened[name] && opened[name] !== 'loading') {
        delete opened[name]
        return
    }
    opened[name] = 'loading'
    try {
        opened[name] = await getFirewallTable(name)
    } catch (err) {
        opened[name] = { error: message(err, 'Could not read the table') }
    }
}

// "Enabled for 4 d 17 h": pfctl's own duration, made readable. Seconds are
// noise past the first hour.
function duration(seconds: number): string {
    const d = Math.floor(seconds / 86400)
    const h = Math.floor((seconds % 86400) / 3600)
    const m = Math.floor((seconds % 3600) / 60)
    if (d > 0) return `${d} d ${h} h`
    if (h > 0) return `${h} h ${m} min`
    if (m > 0) return `${m} min`
    return `${seconds} s`
}

function count(n: number): string {
    return n.toLocaleString()
}

// Every rule the page shows, by number, so a pflog line's rule number can be
// shown as the rule it refers to. Main ruleset only: a logged anchor rule
// names its anchor by the last component alone, and the main number -- the
// anchor rule -- is the unambiguous part (JD-034).
let byNumber = $derived(new Map((report?.rules ?? []).map((r) => [r.number, r])))

let hasNAT = $derived(
    (report?.nat.length ?? 0) > 0 || (report?.anchors ?? []).some((a) => a.nat.length > 0),
)

// Anchors with something in them first; an empty anchor -- the wildcard's
// parent, usually -- is still listed, below.
let anchors = $derived(
    [...(report?.anchors ?? [])].sort((a, b) => {
        const full = (x: typeof a) => (x.rules.length + x.nat.length > 0 || x.error ? 0 : 1)
        return full(a) - full(b) || a.name.localeCompare(b.name)
    }),
)

function actionClass(rule: Rule): string {
    switch (rule.action) {
        case 'pass': return 'act act--pass'
        case 'block': return 'act act--block'
        case 'anchor': return 'act act--anchor'
        default: return 'act'
    }
}
</script>

<header class="page-head page-head--action">
    <div>
        <h1>Firewall</h1>
        <p class="page-sub">
            What PF is doing, read now. Nothing on this page can change it.
        </p>
    </div>
    <button class="btn" onclick={load} disabled={loading}>
        {loading ? 'Reading…' : 'Refresh'}
    </button>
</header>

{#if loadError}
    <p class="notice notice--error" role="alert">{loadError}</p>
{/if}

{#if report}
    {#if report.state === 'not_loaded'}
        <section class="state state--off">
            <span class="badge badge--stopped">Not loaded</span>
            <div>
                <p><strong>PF is not loaded on this host</strong>, so it is filtering nothing.</p>
                <p class="under">
                    Turning it on is a policy in <code>/etc/pf.conf</code> and <code>pf_enable="YES"</code>
                    in <code>rc.conf</code>, and both are the operator's to write. Jail Deck never touches
                    either (JD-020).
                </p>
            </div>
        </section>
    {:else}
        {#if report.state === 'disabled'}
            <section class="state state--warn" role="status">
                <span class="badge badge--warn">Disabled</span>
                <div>
                    <p>
                        <strong>A ruleset is loaded and PF is disabled — none of these rules is being
                        applied.</strong>
                    </p>
                    <p class="under">
                        They read exactly as they would if they were, which is why this is said first.
                        {#if report.for_seconds !== undefined}
                            pfctl's clock says {duration(report.for_seconds)} — since it was last switched
                            off, or since boot if it has never been on.
                        {/if}
                    </p>
                </div>
            </section>
        {:else}
            <section class="state">
                <span class="badge badge--running">Enabled</span>
                <p>
                    {#if report.for_seconds !== undefined}for {duration(report.for_seconds)}{/if}
                </p>
            </section>
        {/if}

        <section class="stats">
            <div><span class="stat">{count(report.state_table?.current ?? 0)}</span> states</div>
            <div><span class="stat">{count(report.state_table?.inserts ?? 0)}</span> created</div>
            <div><span class="stat">{count(report.counters?.match ?? 0)}</span> rule matches</div>
            <div><span class="stat">{count(report.counters?.['state-mismatch'] ?? 0)}</span> state mismatches</div>
        </section>

        {#if report.notices?.length}
            <section class="panel">
                <p class="notice">pfctl said, while succeeding: {report.notices.join('; ')}</p>
            </section>
        {/if}

        <section class="panel">
            <div class="panel__head">
                <h2>Rules <span class="count">{report.rules.length}</span></h2>
                <span class="under">last match wins, unless <code>quick</code></span>
            </div>
            {#if report.rules.length === 0}
                <p class="notice">No filter rules are loaded.</p>
            {:else}
                {@render rules(report.rules)}
            {/if}
        </section>

        {#if hasNAT}
            <section class="panel">
                <div class="panel__head">
                    <h2>NAT <span class="count">{report.nat.length}</span></h2>
                    <span class="under">listed apart because <code>pfctl -s rules</code> does not show it</span>
                </div>
                {#if report.nat.length === 0}
                    <p class="notice">No NAT rules in the main ruleset.</p>
                {:else}
                    {@render rules(report.nat)}
                {/if}
            </section>
        {/if}

        {#if anchors.length > 0}
            <section class="panel">
                <div class="panel__head">
                    <h2>Anchors <span class="count">{anchors.length}</span></h2>
                    <span class="under">their rules are not part of the listing above</span>
                </div>
                {#each anchors as anchor (anchor.name)}
                    <div class="anchor">
                        <h3><code>{anchor.name}</code></h3>
                        {#if anchor.error}
                            <p class="row-error">{anchor.error}</p>
                        {:else if anchor.rules.length + anchor.nat.length === 0}
                            <p class="under">Empty.</p>
                        {:else}
                            {#if anchor.rules.length}{@render rules(anchor.rules)}{/if}
                            {#if anchor.nat.length}{@render rules(anchor.nat)}{/if}
                        {/if}
                    </div>
                {/each}
            </section>
        {/if}

        <section class="panel">
            <div class="panel__head">
                <h2>Tables <span class="count">{report.tables.length}</span></h2>
                <span class="under">addresses are read when a table is opened</span>
            </div>
            {#if report.tables.length === 0}
                <p class="notice">No tables.</p>
            {:else}
                <div class="table-wrap">
                    <table>
                        <thead>
                            <tr><th>Table</th><th>Flags</th><th></th></tr>
                        </thead>
                        <tbody>
                            {#each report.tables as table (table.name)}
                                {@const contents = opened[table.name]}
                                <tr>
                                    <td class="name"><code>&lt;{table.name}&gt;</code></td>
                                    <td>
                                        <code title="p persist · a active · r referenced by a rule">{table.flags}</code>
                                        <span class="under under--inline">
                                            {[table.persistent && 'persistent', table.active && 'active',
                                              table.referenced ? 'used by a rule' : 'unused']
                                                .filter(Boolean).join(' · ')}
                                        </span>
                                    </td>
                                    <td class="num">
                                        <button class="btn btn--small" onclick={() => toggleTable(table.name)}>
                                            {contents && contents !== 'loading' ? 'Close' : 'Show addresses'}
                                        </button>
                                    </td>
                                </tr>
                                {#if contents}
                                    <tr class="detail">
                                        <td colspan="3">
                                            {#if contents === 'loading'}
                                                <span class="under">Reading…</span>
                                            {:else if 'error' in contents}
                                                <span class="row-error">{contents.error}</span>
                                            {:else if contents.addresses.length === 0}
                                                <span class="under">Empty.</span>
                                            {:else}
                                                <p class="under">{count(contents.addresses.length)} entries</p>
                                                <ul class="addresses">
                                                    {#each contents.addresses as address (address)}
                                                        <li><code>{address}</code></li>
                                                    {/each}
                                                </ul>
                                            {/if}
                                        </td>
                                    </tr>
                                {/if}
                            {/each}
                        </tbody>
                    </table>
                </div>
            {/if}
        </section>

        <section class="panel">
            <div class="panel__head">
                <h2>Recent log</h2>
                {#if log}<span class="under"><code>{log.file}</code></span>{/if}
            </div>
            {#if logError}
                <p class="notice notice--error">{logError}</p>
            {:else if log && !log.found}
                <p class="notice">
                    There is no <code>{log.file}</code>: nothing has been logged, or pflogd is not running.
                </p>
            {:else if log?.truncated}
                <p class="notice">
                    The log is too large to read to its end, so no lines are shown — the ones that were
                    read are the oldest, not the most recent.
                </p>
            {:else if log && log.lines.length === 0}
                <p class="notice">The log is empty.</p>
            {:else if log}
                <div class="table-wrap">
                    <table class="log">
                        <thead>
                            <tr><th>Time</th><th>Rule</th><th>Action</th><th>Packet</th></tr>
                        </thead>
                        <tbody>
                            {#each [...log.lines].reverse() as line, i (i)}
                                {@const rule = line.rule_number !== undefined ? byNumber.get(line.rule_number) : undefined}
                                <tr>
                                    {#if line.time}
                                        <td class="when">{line.time.replace(/\.\d+$/, '')}</td>
                                        <td>
                                            <code title={rule?.text ?? ''}>{line.rule}</code>
                                            {#if line.anchor}<span class="under under--inline">in an anchor named <code>{line.anchor}</code></span>{/if}
                                            {#if rule?.label && !line.anchor}<span class="under under--inline">{rule.label}</span>{/if}
                                        </td>
                                        <td><span class={line.action === 'block' ? 'act act--block' : 'act act--pass'}>{line.action}</span> {line.direction} on {line.interface}</td>
                                        <td class="packet"><code>{line.packet}</code></td>
                                    {:else}
                                        <td colspan="4" class="packet"><code>{line.raw}</code></td>
                                    {/if}
                                </tr>
                            {/each}
                        </tbody>
                    </table>
                </div>
            {/if}
            {#if log}
                <p class="panel-note">
                    Newest first. pflogd writes the file every {log.flush_delay_seconds} s, so the newest
                    line can be that far behind, and Jail Deck does not ask it to flush sooner. A rule
                    with <code>log</code> records the packet that opens a connection, not every packet,
                    so a rule's counters can say thousands where the log has one line.
                </p>
            {/if}
        </section>
    {/if}
{/if}

{#snippet rules(list: Rule[])}
    <div class="table-wrap">
        <table>
            <thead>
                <tr>
                    <th class="num">#</th>
                    <th>Rule</th>
                    <th class="num">Evaluated</th>
                    <th class="num">Packets</th>
                    <th class="num">Bytes</th>
                    <th class="num">States</th>
                </tr>
            </thead>
            <tbody>
                {#each list as rule (rule.number)}
                    <tr class:idle={rule.packets === 0 && rule.action !== 'anchor'}>
                        <td class="num">{rule.number}</td>
                        <td class="rule">
                            <span class={actionClass(rule)}>{rule.action}</span>
                            <code>{rule.text.slice(rule.action.length).trimStart()}</code>
                            {#if rule.last_active}<span class="under under--inline" title="pfctl's Last Active Time">last {rule.last_active}</span>{/if}
                        </td>
                        <td class="num">{count(rule.evaluations)}</td>
                        <td class="num">{count(rule.packets)}</td>
                        <td class="num">{formatBytes(rule.bytes)}</td>
                        <td class="num">{count(rule.states)}</td>
                    </tr>
                {/each}
            </tbody>
        </table>
    </div>
{/snippet}

<style>
.page-head--action {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--space-4);
}

.state {
    display: flex;
    align-items: flex-start;
    gap: var(--space-3);
    padding: var(--space-4);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
}

.state p {
    margin: 0;
}

.state--warn {
    border-color: color-mix(in srgb, var(--warn) 45%, transparent);
    background: var(--warn-soft);
}

.state--off {
    color: var(--text-muted);
}

.stats {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-6);
    margin-top: var(--space-4);
    color: var(--text-muted);
    font-size: 0.875rem;
}

.stat {
    font-size: 1.25rem;
    font-weight: 600;
    color: var(--text-strong);
    margin-right: 0.25em;
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

.under {
    font-size: 0.78rem;
    color: var(--text-muted);
}

.under--inline {
    margin-left: 0.5em;
}

.panel-note {
    font-size: 0.78rem;
    color: var(--text-muted);
    margin: var(--space-3) 0 0;
}

.row-error {
    color: var(--danger);
    font-size: 0.875rem;
}

.anchor + .anchor {
    margin-top: var(--space-4);
}

.anchor h3 {
    font-size: 0.9rem;
    margin: 0 0 var(--space-2);
}

/* The rule text is pfctl's, so it is set as code; the action is pulled out
   in front of it only to be coloured. */
.rule code {
    white-space: pre-wrap;
    word-break: break-word;
    font-size: 0.82rem;
}

.act {
    display: inline-block;
    min-width: 3.6em;
    font-weight: 600;
    font-size: 0.82rem;
}

.act--pass { color: var(--ok); }
.act--block { color: var(--danger); }
.act--anchor { color: var(--text-muted); }

/* A rule nothing has matched is not wrong, just quiet. */
tr.idle td {
    color: var(--text-muted);
}

.detail td {
    background: var(--surface-sunken);
}

.addresses {
    list-style: none;
    margin: var(--space-2) 0 0;
    padding: 0;
    columns: 14em;
}

.log .when {
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
}

.packet code {
    font-size: 0.8rem;
    word-break: break-word;
}

.btn--small {
    padding: 0.25rem 0.6rem;
    font-size: 0.8rem;
}
</style>
