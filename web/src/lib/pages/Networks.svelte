<script lang="ts">
import { onMount } from 'svelte'
import { onNavClick } from '../router.svelte'
import { getTopology } from '../stores/networks.svelte'
import type { Topology, TopologyBridge, TopologyMember } from '../stores/networks.svelte'
import { ApiError } from '../services/api'

// Every network an instance can be on, and what is on it (#128): read off
// the host on every visit and kept nowhere (JD-011).
let topology = $state<Topology | null>(null)
let loadError = $state('')

async function load() {
    loadError = ''
    try {
        topology = await getTopology()
    } catch (err) {
        loadError = err instanceof ApiError
            ? err.statusText ?? 'Could not load the networks'
            : 'Could not reach Jail Deck'
    }
}

onMount(load)

const kindLabel: Record<TopologyBridge['kind'], string> = {
    default: 'default network',
    tenant: 'tenant network',
    operator: "operator's",
}

const natText: Record<TopologyBridge['nat'], { label: string, badge: string, title: string }> = {
    'loaded': { label: 'NAT', badge: 'running', title: "Jail Deck's rules translate this subnet out of the host" },
    'not-loaded': { label: 'no NAT', badge: 'stopped', title: "Jail Deck's rules do not translate this subnet; the operator's own pf.conf is not read" },
    'unknown': { label: 'NAT unknown', badge: 'stopped', title: 'PF is not enabled, or this bridge has no address' },
}

function title(b: TopologyBridge): string {
    return b.network ?? b.switch ?? b.name
}

function instanceHref(m: TopologyMember): string | null {
    if (!m.instance) return null
    return m.kind === 'jail' ? `/jails/${m.instance}` : `/vms/${m.instance}`
}
</script>

<header class="page-head">
    <h1>Networks</h1>
    <p class="page-sub">
        Every bridge and vm-bhyve switch on this host, and the jails and guests on it.
        Jail Deck reads this; creating a network is on the API (<code>POST /api/networks</code>).
    </p>
</header>

{#if loadError}
    <p class="notice notice--error" role="alert">
        {loadError}
        <button type="button" class="btn btn--small" onclick={load}>Retry</button>
    </p>
{:else if topology === null}
    <p class="notice">Loading networks…</p>
{:else if topology.bridges.length === 0}
    <p class="notice">This host has no bridges and no vm-bhyve switches: nothing is networked through one.</p>
{:else}
    <div class="networks">
        {#each topology.bridges as b (b.name + '/' + (b.switch ?? ''))}
            {@const nat = natText[b.nat]}
            <section class="network" aria-label={title(b)}>
                <header class="network__head">
                    <h2>{title(b)}</h2>
                    <span class="badge badge--stopped">{kindLabel[b.kind]}</span>
                    <span class="badge badge--{nat.badge}" title={nat.title}>{nat.label}</span>
                </header>
                <dl class="facts">
                    <dt>Bridge</dt>
                    <dd>
                        {#if b.exists}<code>{b.name}</code>{:else}<span class="muted">not made yet: vm-bhyve makes it at <code>vm init</code>, at boot</span>{/if}
                    </dd>
                    {#if b.switch}
                        <dt>Switch</dt>
                        <dd><code>{b.switch}</code> <span class="muted">{b.switch_type}</span></dd>
                    {/if}
                    {#if b.tenant}
                        <dt>Tenant</dt>
                        <dd>{b.tenant}</dd>
                    {/if}
                    <dt>Address</dt>
                    <dd>{#if b.addresses.length}<code>{b.addresses.join(', ')}</code>{:else}<span class="muted">none</span>{/if}</dd>
                    {#if b.in_use?.length}
                        <dt>In use</dt>
                        <dd>
                            <ul class="in-use">
                                {#each b.in_use as u (u.address)}
                                    {@const to = u.kind === 'jail' ? `/jails/${encodeURIComponent(u.name ?? '')}`
                                        : u.kind === 'guest' ? `/vms/${encodeURIComponent(u.name ?? '')}` : ''}
                                    <li>
                                        <code>{u.address}</code>
                                        {#if to && u.name && !u.name.startsWith('/')}
                                            {u.kind} <a href={to} onclick={(e) => onNavClick(e, to)}>{u.name}</a>
                                        {:else if u.name}
                                            {u.kind} <code>{u.name}</code>
                                        {:else}
                                            <span class="muted">seen on the bridge, in no configuration</span>
                                        {/if}
                                    </li>
                                {/each}
                            </ul>
                        </dd>
                    {/if}
                </dl>
                {#if b.members.length === 0}
                    <p class="empty">{b.exists ? 'Nothing is on it.' : 'Nothing can be on it until its bridge exists.'}</p>
                {:else}
                    <div class="table-wrap">
                        <table>
                            <thead>
                                <tr><th>Interface</th><th>On it</th><th>Port</th></tr>
                            </thead>
                            <tbody>
                                {#each b.members as m (m.interface)}
                                    {@const href = instanceHref(m)}
                                    <tr>
                                        <td><code>{m.interface}</code></td>
                                        <td>
                                            {#if href}
                                                {m.kind === 'jail' ? 'jail' : 'guest'}
                                                <a {href} onclick={(e) => onNavClick(e, href)}>{m.instance}</a>
                                                {#if m.nic !== undefined}<span class="muted">NIC {m.nic}</span>{/if}
                                            {:else}
                                                <span class="muted">not a jail's or a guest's</span>
                                            {/if}
                                        </td>
                                        <td>{m.private ? 'private' : 'shared'}</td>
                                    </tr>
                                {/each}
                            </tbody>
                        </table>
                    </div>
                {/if}
            </section>
        {/each}
    </div>
{/if}

<style>
.in-use {
    margin: 0;
    padding: 0;
    list-style: none;
    display: grid;
    gap: var(--space-1);
}

.networks {
    display: grid;
    gap: var(--space-5);
}

.network__head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-2) var(--space-3);
    margin-bottom: var(--space-3);
}

.network__head h2 {
    margin: 0;
    overflow-wrap: anywhere;
}

.facts {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--space-2) var(--space-4);
    margin: 0 0 var(--space-3);
}

.facts dt { margin: 0; color: var(--text-muted); }
.facts dd { margin: 0; overflow-wrap: anywhere; }

.muted {
    color: var(--text-muted);
    font-size: 0.85rem;
}

.empty {
    color: var(--text-muted);
    margin: 0;
}
</style>
