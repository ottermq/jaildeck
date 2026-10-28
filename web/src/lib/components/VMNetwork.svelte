<script lang="ts">
import { onMount } from 'svelte'
import { ensureDefaultNetwork, getDefaultNetwork } from '../stores/networks.svelte'
import type { DefaultNetwork, Part } from '../stores/networks.svelte'
import { ApiError } from '../services/api'

// The default VM network (JD-040), part by part. Jail Deck's own parts are put
// in place by one button; the operator's -- PF and pf.conf -- are shown with
// exactly what to do, because Jail Deck never touches them (JD-020).
let { onchanged }: { onchanged?: () => void } = $props()

let net = $state<DefaultNetwork | null>(null)
let loadError = $state('')
let busy = $state(false)
let actionError = $state('')
let note = $state('')
let open = $state(false)
let copied = $state('')

async function load() {
    loadError = ''
    try {
        net = await getDefaultNetwork()
    } catch (err) {
        loadError = describe(err, 'Could not read the default network')
    }
}

onMount(load)

function describe(err: unknown, fallback: string): string {
    if (!(err instanceof ApiError)) return 'Could not reach Jail Deck'
    return err.statusText ?? fallback
}

let pending = $derived((net?.parts ?? []).filter(p => p.status !== 'pass'))
let ours = $derived(pending.some(p => p.owner === 'jaildeck' && p.status === 'fail'))

async function setUp() {
    if (busy) return
    busy = true
    actionError = ''
    note = ''
    try {
        const ensured = await ensureDefaultNetwork()
        note = ensured.changed
            ? `Done: ${ensured.done.join(', ')}.${ensured.note ? ' ' + ensured.note : ''}`
            : 'Nothing of Jail Deck’s was missing.'
        net = ensured
        onchanged?.()
    } catch (err) {
        actionError = describe(err, 'Could not set up the network')
        await load()
    } finally {
        busy = false
    }
}

const canCopy = typeof navigator !== 'undefined' && !!navigator.clipboard && window.isSecureContext

async function copy(part: Part) {
    try {
        await navigator.clipboard.writeText(part.remedy ?? '')
        copied = part.id
        setTimeout(() => { if (copied === part.id) copied = '' }, 2000)
    } catch {
        // The text stays selectable.
    }
}

const statusBadge = { pass: 'running', fail: 'failed', unknown: 'timed-out' } as const
</script>

<section class="panel" aria-labelledby="network-heading">
    <div class="panel__head">
        <h2 id="network-heading">Default network</h2>
        {#if ours}
            <button type="button" class="btn btn--small btn--primary" onclick={setUp} disabled={busy}>
                {busy ? 'Setting up…' : 'Set up'}
            </button>
        {/if}
    </div>

    {#if loadError}
        <p class="notice notice--error" role="alert">{loadError}</p>
    {:else if net === null}
        <p class="notice">Reading the network…</p>
    {:else}
        <p class="summary">
            {#if net.ready}
                <span class="badge badge--running">ready</span>
            {:else if net.usable}
                <span class="badge badge--timed-out">host only</span>
            {:else}
                <span class="badge badge--stopped">not set up</span>
            {/if}
            Switch <code>{net.switch}</code>, <code>{net.subnet}</code>, gateway <code>{net.gateway}</code>{#if net.egress}, out through <code>{net.egress}</code>{/if}.
            {#if net.ready}
                Guests on it reach the outside and not each other.
            {:else if net.usable}
                Guests on it reach the host and not each other; the outside needs what is below.
            {/if}
        </p>
        {#if note}
            <p class="notice" aria-live="polite">{note}</p>
        {/if}
        {#if actionError}
            <p class="notice notice--error" role="alert">{actionError}</p>
        {/if}

        {#if pending.length > 0 || open}
            <ul class="parts">
                {#each (open ? net.parts : pending) as part (part.id)}
                    <li class="part">
                        <div class="part__head">
                            <span class="badge badge--{statusBadge[part.status]}">{part.status}</span>
                            <span class="part__title">{part.title}</span>
                            <span class="owner">{part.owner === 'jaildeck' ? 'Jail Deck' : 'you'}</span>
                            {#if part.observed}<code class="observed">{part.observed}</code>{/if}
                        </div>
                        {#if part.detail}<p class="detail">{part.detail}</p>{/if}
                        {#if part.remedy}
                            <div class="remedy">
                                <code>{part.remedy}</code>
                                {#if canCopy}
                                    <button type="button" class="btn btn--small" onclick={() => copy(part)}>
                                        {copied === part.id ? 'Copied' : 'Copy'}
                                    </button>
                                {/if}
                            </div>
                        {/if}
                    </li>
                {/each}
            </ul>
        {/if}
        {#if pending.length === 0}
            <button type="button" class="link-button" onclick={() => (open = !open)}>
                {open ? 'Hide the parts' : 'Show the parts'}
            </button>
        {:else if ours}
            <p class="hint">
                <strong>Set up</strong> does Jail Deck’s parts: the switch with private ports and the host’s
                address, IP forwarding now and at boot, and the NAT rules. Yours — PF and three lines in
                <code>pf.conf</code> — it never touches.
            </p>
        {/if}
    {/if}
</section>

<style>
.panel {
    margin-top: var(--space-6);
}

.panel__head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-4);
    margin-bottom: var(--space-2);
}

/* Inline text, not flex: the commas belong to the words before them. */
.summary {
    margin: 0 0 var(--space-3);
    line-height: 1.9;
    color: var(--text-strong);
}

.summary .badge {
    margin-right: var(--space-2);
}

.notice {
    margin-bottom: var(--space-3);
}

.parts {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    padding: var(--space-3) var(--space-4);
}

.part + .part {
    padding-top: var(--space-3);
    border-top: 1px solid var(--border);
}

.part__head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
}

.part__title {
    font-weight: 600;
    color: var(--text-strong);
}

.owner {
    font-size: 0.75rem;
    color: var(--text-muted);
}

.observed {
    white-space: normal;
    overflow-wrap: anywhere;
}

.detail {
    margin: var(--space-2) 0 0;
    font-size: 0.9rem;
    color: var(--text-muted);
}

.remedy {
    display: flex;
    align-items: flex-start;
    gap: var(--space-2);
    margin-top: var(--space-2);
}

.remedy code {
    display: block;
    flex: 1;
    min-width: 0;
    padding: var(--space-2) var(--space-3);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
}

.hint {
    margin-top: var(--space-3);
    font-size: 0.85rem;
    color: var(--text-muted);
}

.link-button {
    background: none;
    border: 0;
    padding: 0;
    font: inherit;
    font-size: 0.85rem;
    color: var(--text-muted);
    text-decoration: underline;
    cursor: pointer;
}
</style>
