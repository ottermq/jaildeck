<script lang="ts">
import { onMount } from 'svelte'
import { getReadiness } from '../stores/host.svelte'
import type { Readiness, Check, Capability, CheckStatus, CapabilityStatus } from '../stores/host.svelte'
import { ApiError } from '../services/api'
import AboutJaildeck from '../components/AboutJaildeck.svelte'
import Reclaimable from '../components/Reclaimable.svelte'

let report = $state<Readiness | null>(null)
let loadError = $state('')
let loading = $state(true)
let checkedAt = $state<Date | null>(null)
let copied = $state('')

// Three check states, three affordances, reusing the outcome badges: unknown
// is amber like a timeout, never red, because a check that could not look is
// not a host that failed it.
const checkBadge: Record<CheckStatus, { badge: string, label: string }> = {
    pass: { badge: 'running', label: 'Pass' },
    fail: { badge: 'failed', label: 'Fail' },
    unknown: { badge: 'timed-out', label: 'Unknown' },
}

// An unavailable capability is neutral rather than red: a host without VT-x
// is perfectly healthy for jails, and the page must not say otherwise.
const capabilityBadge: Record<CapabilityStatus, { badge: string, label: string }> = {
    ready: { badge: 'running', label: 'Ready' },
    unknown: { badge: 'timed-out', label: 'Unknown' },
    unavailable: { badge: 'stopped', label: 'Unavailable' },
}

const order: Record<CapabilityStatus, number> = { ready: 0, unknown: 1, unavailable: 2 }

// Lead with what works. The sort is stable, so within a status the server's
// order is kept.
let capabilities = $derived(
    report ? [...report.capabilities].sort((a, b) => order[a.status] - order[b.status]) : []
)
let ready = $derived(capabilities.filter(c => c.status === 'ready'))
let checksByID = $derived(new Map((report?.checks ?? []).map(c => [c.id, c])))

function gatingChecks(cap: Capability): Check[] {
    return cap.gated_by.map(id => checksByID.get(id)).filter((c): c is Check => c !== undefined)
}

// Nothing is kept between loads: the report is derived state (JD-011), and
// a refresh asks the host again.
async function load() {
    loadError = ''
    loading = true
    try {
        report = await getReadiness()
        checkedAt = new Date()
    } catch (err) {
        loadError = err instanceof ApiError
            ? err.statusText ?? 'Could not load the readiness report'
            : 'Could not reach Jail Deck'
    } finally {
        loading = false
    }
}

onMount(load)

// The clipboard API exists only in a secure context — loopback or TLS. Without
// it the remedy is still plain selectable text.
const canCopy = typeof navigator !== 'undefined' && !!navigator.clipboard && window.isSecureContext

async function copy(check: Check) {
    try {
        await navigator.clipboard.writeText(check.remedy)
        copied = check.id
        setTimeout(() => { if (copied === check.id) copied = '' }, 2000)
    } catch {
        // Selecting the text still works; a failed copy is not worth an error.
    }
}
</script>

<header class="page-head host-head">
    <div>
        <h1>Host</h1>
        <p class="page-sub">
            What this host is ready for, and what would make it ready for the rest.
            Jail Deck reports this; it changes none of it.
        </p>
    </div>
    <div class="host-head__refresh">
        {#if checkedAt}
            <span class="checked-at">Checked {checkedAt.toLocaleTimeString()}</span>
        {/if}
        <button type="button" class="btn btn--small" disabled={loading} onclick={load}>
            {loading ? 'Checking…' : 'Refresh'}
        </button>
    </div>
</header>

{#if loadError}
    <p class="notice notice--error" role="alert">
        {loadError}
        <button type="button" class="btn btn--small" onclick={load}>Retry</button>
    </p>
{/if}

{#if report}
    <p class="summary">
        {#if ready.length > 0}
            Ready for {ready.map(c => c.title).join(', ')}.
        {:else}
            Nothing is ready yet; each section below says what it needs.
        {/if}
    </p>

    <div class="capabilities">
        {#each capabilities as cap (cap.id)}
            {@const badge = capabilityBadge[cap.status]}
            <section class="capability" aria-labelledby="cap-{cap.id}">
                <header class="capability__head">
                    <h2 id="cap-{cap.id}">{cap.title}</h2>
                    <span class="badge badge--{badge.badge}">{badge.label}</span>
                </header>

                {#if cap.status === 'unknown'}
                    <p class="capability__note">Jail Deck could not tell. The check below says why.</p>
                {:else if cap.status === 'unavailable'}
                    <p class="capability__note">Only needed if you use it. The checks below say what it takes.</p>
                {/if}

                <ul class="checks">
                    {#each gatingChecks(cap) as check (check.id)}
                        {@const cb = checkBadge[check.status]}
                        <li class="check">
                            <div class="check__head">
                                <span class="badge badge--{cb.badge}">{cb.label}</span>
                                <span class="check__title">{check.title}</span>
                                {#if check.observed}
                                    <code class="check__observed">{check.observed}</code>
                                {/if}
                            </div>
                            {#if check.detail}
                                <p class="check__detail">{check.detail}</p>
                            {/if}
                            {#if check.remedy}
                                <div class="remedy">
                                    <code>{check.remedy}</code>
                                    {#if canCopy}
                                        <button type="button" class="btn btn--small" onclick={() => copy(check)}>
                                            {copied === check.id ? 'Copied' : 'Copy'}
                                        </button>
                                    {/if}
                                </div>
                            {/if}
                        </li>
                    {/each}
                </ul>
            </section>
        {/each}
    </div>
{:else if loading}
    <p class="notice">Checking the host…</p>
{/if}

<Reclaimable />

<AboutJaildeck />

<style>
.host-head {
    flex-direction: row;
    justify-content: space-between;
    align-items: flex-start;
    gap: var(--space-4);
}

.host-head__refresh {
    display: flex;
    align-items: center;
    gap: var(--space-3);
}

.checked-at {
    font-size: 0.85rem;
    color: var(--text-muted);
    white-space: nowrap;
}

.summary {
    margin-bottom: var(--space-5);
    color: var(--text-strong);
}

.capabilities {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
}

.capability {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    padding: var(--space-4);
}

.capability__head {
    display: flex;
    align-items: center;
    gap: var(--space-3);
}

.capability__note {
    margin-top: var(--space-1);
    font-size: 0.9rem;
    color: var(--text-muted);
}

.checks {
    list-style: none;
    margin: var(--space-3) 0 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
}

.check {
    padding-top: var(--space-3);
    border-top: 1px solid var(--border);
}

.check__head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
}

.check__title {
    font-weight: 600;
    color: var(--text-strong);
}

/* Observed values can be long lists; let them wrap rather than overflow. */
.check__observed {
    white-space: normal;
    overflow-wrap: anywhere;
}

.check__detail {
    margin-top: var(--space-2);
    font-size: 0.9rem;
    color: var(--text-muted);
}

.remedy {
    display: flex;
    align-items: flex-start;
    gap: var(--space-2);
    margin-top: var(--space-2);
}

/*
 * The remedy is meant for a root shell, so it is shown exactly as sent:
 * whitespace kept, wrapped only where the line would overflow.
 */
.remedy code {
    display: block;
    flex: 1;
    min-width: 0;
    padding: var(--space-2) var(--space-3);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
}

@media (max-width: 760px) {
    .host-head {
        flex-direction: column;
    }
}
</style>
