<script lang="ts">
import { onMount } from 'svelte'
import { onNavClick } from '../router.svelte'
import { listVMs } from '../stores/vms.svelte'
import type { Listing } from '../stores/vms.svelte'
import { ApiError } from '../services/api'
import { normalizeEmptyValue } from '../utils'
import { stateBadge } from '../vmState'

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

function href(name: string): string {
    return `/vms/${encodeURIComponent(name)}`
}
</script>

<header class="page-head">
    <h1>Virtual machines</h1>
    <p class="page-sub">Every guest vm-bhyve knows about on this host, and what it is doing. Read-only for now.</p>
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
{:else if listing.vms.length === 0}
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
                    </tr>
                {/each}
            </tbody>
        </table>
    </div>
{/if}

<style>
.name {
    font-weight: 600;
    color: var(--text-strong);
}

.muted {
    margin-left: var(--space-2);
    color: var(--text-muted);
    font-size: 0.8rem;
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
