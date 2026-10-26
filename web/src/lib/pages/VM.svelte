<script lang="ts">
import { onMount } from 'svelte'
import { onNavClick } from '../router.svelte'
import { getVM } from '../stores/vms.svelte'
import type { Detail } from '../stores/vms.svelte'
import { ApiError } from '../services/api'
import { formatBytes, normalizeEmptyValue } from '../utils'
import { stateBadge } from '../vmState'

let { name }: { name: string } = $props()

let vm = $state<Detail | null>(null)
let loadError = $state('')

async function load() {
    loadError = ''
    try {
        vm = await getVM(name)
    } catch (err) {
        loadError = err instanceof ApiError
            ? err.statusText ?? `Could not load ${name}`
            : 'Could not reach Jail Deck'
    }
}

onMount(load)

let consoles = $derived(Object.entries(vm?.consoles ?? {}).sort(([a], [b]) => a.localeCompare(b)))

function bytes(n: number | undefined): string {
    return n === undefined ? '—' : formatBytes(n)
}
</script>

<nav class="crumbs" aria-label="Breadcrumb">
    <a href="/vms" onclick={(e) => onNavClick(e, '/vms')}>Virtual machines</a>
    <span aria-hidden="true">/</span>
    <span class="crumbs__here">{name}</span>
</nav>

{#if loadError}
    <p class="notice notice--error" role="alert">
        {loadError}
        <button type="button" class="btn btn--small" onclick={load}>Retry</button>
    </p>
{:else if vm === null}
    <p class="notice">Loading {name}…</p>
{:else}
    <header class="page-head">
        <h1>{vm.name}</h1>
        <p class="page-sub">
            <span class="badge badge--{stateBadge[vm.state]}">{vm.state}</span>
            {#if vm.pid !== undefined}<span>pid {vm.pid}</span>{/if}
            {#if vm.locked_by}<span>held by {vm.locked_by}</span>{/if}
            <span>{vm.autostart ? `starts at boot, #${vm.autostart_order} in vm_list` : 'not started at boot'}</span>
        </p>
    </header>

    <section class="panel">
        <h2>Configuration</h2>
        <dl class="facts">
            <dt>CPU</dt>
            <dd>{normalizeEmptyValue(vm.cpu ?? '')}</dd>
            <dt>Memory</dt>
            <dd>
                {normalizeEmptyValue(vm.memory ?? '')}
                {#if vm.memory_resident !== undefined}
                    <span class="muted">{formatBytes(vm.memory_resident)} resident</span>
                {/if}
            </dd>
            <dt>Loader</dt>
            <dd>{normalizeEmptyValue(vm.loader ?? '')}</dd>
            <dt>Datastore</dt>
            <dd>{vm.datastore}</dd>
            {#if vm.uuid}
                <dt>UUID</dt>
                <dd><code>{vm.uuid}</code></dd>
            {/if}
            {#if vm.vnc}
                <dt>VNC</dt>
                <dd><code>{vm.vnc}</code></dd>
            {/if}
        </dl>
    </section>

    <section class="panel">
        <h2>Console</h2>
        {#if consoles.length > 0}
            <dl class="facts">
                {#each consoles as [port, device] (port)}
                    <dt>{port}</dt>
                    <dd><code>{device}</code></dd>
                {/each}
            </dl>
            <p class="hint">The host's end of a null-modem pair. From a root shell, <code>vm console {vm.name}</code> attaches to it.</p>
        {:else}
            <p class="notice">None while the guest is not running: the console belongs to its bhyve instance.</p>
        {/if}
    </section>

    <section class="panel">
        <h2>Network</h2>
        {#if vm.networks.length > 0}
            <div class="table-wrap">
                <table>
                    <thead>
                        <tr>
                            <th class="num">#</th>
                            <th>Switch</th>
                            <th>Emulation</th>
                            <th>MAC</th>
                            <th>Tap</th>
                            <th>Bridge</th>
                            <th class="num">In</th>
                            <th class="num">Out</th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each vm.networks as n (n.number)}
                            <tr>
                                <td class="num">{n.number}</td>
                                <td>{normalizeEmptyValue(n.switch ?? '')}</td>
                                <td>{normalizeEmptyValue(n.emulation ?? '')}</td>
                                <td><code>{normalizeEmptyValue(n.mac ?? '')}</code></td>
                                <td>{normalizeEmptyValue(n.device ?? '')}</td>
                                <td>{normalizeEmptyValue(n.bridge ?? '')}</td>
                                <td class="num">{bytes(n.bytes_in)}</td>
                                <td class="num">{bytes(n.bytes_out)}</td>
                            </tr>
                        {/each}
                    </tbody>
                </table>
            </div>
            <p class="hint">A running guest gets a new tap on every start, so the tap is this run's.</p>
        {:else}
            <p class="notice">No network interface is configured.</p>
        {/if}
    </section>

    <section class="panel">
        <h2>Disks</h2>
        {#if vm.disks.length > 0}
            <div class="table-wrap">
                <table>
                    <thead>
                        <tr>
                            <th class="num">#</th>
                            <th>Path</th>
                            <th>Type</th>
                            <th>Emulation</th>
                            <th class="num">Size</th>
                            <th class="num">Used</th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each vm.disks as d (d.number)}
                            <tr>
                                <td class="num">{d.number}</td>
                                <td>{#if d.path}<code>{d.path}</code>{:else}—{/if}</td>
                                <td>{normalizeEmptyValue(d.device_type ?? '')}</td>
                                <td>{normalizeEmptyValue(d.emulation ?? '')}</td>
                                <td class="num">{bytes(d.size)}</td>
                                <td class="num">{bytes(d.used)}</td>
                            </tr>
                        {/each}
                    </tbody>
                </table>
            </div>
        {:else}
            <p class="notice">No disk is configured.</p>
        {/if}
    </section>
{/if}

<style>
.crumbs {
    display: flex;
    gap: var(--space-2);
    align-items: baseline;
    font-size: 0.85rem;
    color: var(--text-muted);
    margin-bottom: var(--space-3);
}

.crumbs__here {
    color: var(--text-strong);
    font-weight: 600;
}

.page-sub {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
    align-items: center;
}

.panel { margin-top: var(--space-6); }
.panel h2 { margin-bottom: var(--space-3); }

.facts {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--space-2) var(--space-4);
    margin: 0;
}

.facts dt { margin: 0; color: var(--text-muted); }
.facts dd { margin: 0; }

.muted {
    margin-left: var(--space-2);
    color: var(--text-muted);
    font-size: 0.8rem;
}

.hint {
    margin-top: var(--space-3);
    font-size: 0.85rem;
    color: var(--text-muted);
}
</style>
