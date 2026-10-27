<script lang="ts">
import { onMount } from 'svelte'
import { onNavClick } from '../router.svelte'
import { listVMs } from '../stores/vms.svelte'
import type { Listing } from '../stores/vms.svelte'
import { ApiError } from '../services/api'
import { normalizeEmptyValue } from '../utils'
import { stateBadge } from '../vmState'
import VMActions from '../components/VMActions.svelte'
import VMImages from '../components/VMImages.svelte'
import NewVM from '../components/NewVM.svelte'
import type { Created } from '../stores/vms.svelte'
import type { OperationResult } from '../stores/vms.svelte'

let listing = $state<Listing | null>(null)
let loadError = $state('')

// Read on every visit and kept nowhere: vm-bhyve is the record (JD-011).
async function load() {
    loadError = ''
    try {
        listing = await listVMs()
    } catch (err) {
        loadError = err instanceof ApiError
            ? err.statusText ?? 'Could not load virtual machines'
            : 'Could not reach Jail Deck'
    }
}

onMount(load)

// What the last operation on a guest came to, shown under its row until the
// next one: a failure, or a stop that had to pull the plug.
let notes = $state<Record<string, { text: string, kind: 'error' | 'warn' }>>({})

async function done(name: string, outcome: { result?: OperationResult, error?: string }) {
    delete notes[name]
    if (outcome.error) {
        notes[name] = { text: outcome.error, kind: 'error' }
    } else if (outcome.result?.forced) {
        notes[name] = { text: forcedNote(name), kind: 'warn' }
    }
    // Reloaded whichever way it went: a failure can still have changed the
    // guest, and vm-bhyve's listing is the only record (JD-011).
    await load()
}

function forcedNote(name: string): string {
    return `${name} did not answer the power button within a minute and was powered off.`
}

// ---- creating ----------------------------------------------------------

let creating = $state(false)
let createdNote = $state('')
// The images panel reads its own listing on mount; a guest made or destroyed
// changes which guests an image is the disk of, so it is mounted again.
let imagesKey = $state(0)

async function created(vm: Created) {
    creating = false
    createdNote = vm.state === 'running'
        ? `${vm.name} was made from ${vm.image} and is running. A cloud image takes about twenty seconds to boot.`
        : `${vm.name} was made from ${vm.image}.`
    imagesKey++
    await load()
}

function href(name: string): string {
    return `/vms/${encodeURIComponent(name)}`
}
</script>

<header class="page-head">
    <h1>Virtual machines</h1>
    <p class="page-sub">Every guest vm-bhyve knows about on this host, and what it is doing.</p>
</header>

{#if loadError}
    <p class="notice notice--error" role="alert">
        {loadError}
        <button type="button" class="btn btn--small" onclick={load}>Retry</button>
    </p>
{:else if listing === null}
    <p class="notice">Loading virtual machines…</p>
{:else if listing.vm_bhyve === 'not_installed'}
    <!-- The common case, and an answer: not an error (JD-035). -->
    <p class="notice">
        vm-bhyve is not installed, so this host runs no virtual machines through Jail Deck.
        <a href="/host" onclick={(e) => onNavClick(e, '/host')}>Host Setup</a> says what it needs.
    </p>
{:else if listing.vm_bhyve === 'not_configured'}
    <div class="notice notice--warn" role="status">
        <p>vm-bhyve is installed and will not list guests until it is configured. In its own words:</p>
        <code>{listing.problem}</code>
        <p><a href="/host" onclick={(e) => onNavClick(e, '/host')}>Host Setup</a> has the remedy.</p>
    </div>
{:else}
    <div class="section-head">
        <h2>Guests</h2>
        <button type="button" class="btn btn--small" onclick={() => { creating = true; createdNote = '' }}
                disabled={creating}>Create a VM</button>
    </div>
    {#if createdNote}
        <p class="notice" aria-live="polite">{createdNote}</p>
    {/if}
    {#if creating}
        <NewVM switches={listing.switches} oncreated={created} oncancel={() => (creating = false)} />
    {/if}
    {#if listing.vms.length === 0}
        <p class="notice">vm-bhyve is ready, and has no guests.</p>
    {:else}
    <div class="table-wrap">
        <table>
            <thead>
                <tr>
                    <th>State</th>
                    <th>Name</th>
                    <th class="num">CPU</th>
                    <th class="num">Memory</th>
                    <th>Loader</th>
                    <th>Datastore</th>
                    <th>At boot</th>
                    <th class="actions-col">Actions</th>
                </tr>
            </thead>
            <tbody>
                {#each listing.vms as vm (vm.name)}
                    <tr>
                        <td>
                            <span class="badge badge--{stateBadge[vm.state]}">{vm.state}</span>
                            {#if vm.locked_by}<span class="muted">by {vm.locked_by}</span>{/if}
                        </td>
                        <td class="name">
                            <a href={href(vm.name)} onclick={(e) => onNavClick(e, href(vm.name))}>{vm.name}</a>
                        </td>
                        <td class="num">{normalizeEmptyValue(vm.cpu ?? '')}</td>
                        <td class="num">{normalizeEmptyValue(vm.memory ?? '')}</td>
                        <td>{normalizeEmptyValue(vm.loader ?? '')}</td>
                        <td>{vm.datastore}</td>
                        <td>{vm.autostart ? `Yes, #${vm.autostart_order}` : 'No'}</td>
                        <td><VMActions {vm} ondone={(outcome) => done(vm.name, outcome)} /></td>
                    </tr>
                    {#if notes[vm.name]}
                        <tr class="row-note row-note--{notes[vm.name].kind}">
                            <td colspan="8" aria-live="polite">{notes[vm.name].text}</td>
                        </tr>
                    {/if}
                {/each}
            </tbody>
        </table>
    </div>
    {/if}
{/if}

{#if listing?.vm_bhyve === 'ready'}
    {#key imagesKey}
        <VMImages />
    {/key}
{/if}

<style>
.section-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-4);
    margin-bottom: var(--space-3);
}

.notice {
    margin-bottom: var(--space-3);
}

.name {
    font-weight: 600;
    color: var(--text-strong);
}

.muted {
    margin-left: var(--space-2);
    color: var(--text-muted);
    font-size: 0.8rem;
}

.actions-col {
    text-align: right;
}

.row-note td {
    font-size: 0.875rem;
}

.row-note--error td {
    color: var(--danger);
    background: var(--danger-soft);
}

.row-note--warn td {
    color: var(--warn);
    background: var(--warn-soft);
}

.notice--warn {
    flex-direction: column;
    align-items: flex-start;
    color: var(--warn);
    background: var(--warn-soft);
    border-color: color-mix(in srgb, var(--warn) 35%, transparent);
}

.notice--warn p {
    margin: 0;
}
</style>
