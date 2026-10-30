<script lang="ts">
import { onMount } from 'svelte'
import { navigate, onNavClick } from '../router.svelte'
import { destroyVM, getVM, getVMConfiguration, setVMTags } from '../stores/vms.svelte'
import { ask } from '../stores/confirm.svelte'
import type { Detail } from '../stores/vms.svelte'
import { ApiError } from '../services/api'
import { formatBytes, normalizeEmptyValue } from '../utils'
import { stateBadge } from '../vmState'
import VMActions from '../components/VMActions.svelte'
import ConsolePanel from '../components/ConsolePanel.svelte'
import TagsPanel from '../components/TagsPanel.svelte'
import InspectPanel from '../components/InspectPanel.svelte'
import type { OperationResult } from '../stores/vms.svelte'

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

let note = $state<{ text: string, kind: 'error' | 'warn' } | null>(null)

async function done(outcome: { result?: OperationResult, error?: string }) {
    note = null
    if (outcome.error) {
        note = { text: outcome.error, kind: 'error' }
    } else if (outcome.result?.forced) {
        note = { text: `${name} did not answer the power button within a minute and was powered off.`, kind: 'warn' }
    }
    // The console, the tap and resident memory come and go with the guest's
    // bhyve instance, so the whole page is read again.
    await load()
}

// ---- destroying ----------------------------------------------------------
//
// Only a stopped guest: stopping is its own decision, with its own deadline
// (JD-036), and the server refuses a running one anyway (JD-038).
let destroyable = $derived(vm?.state === 'stopped' || vm?.state === 'suspended')
let destroying = $state(false)

async function destroy() {
    if (!vm || destroying) return
    const confirmed = await ask({
        title: `Destroy ${vm.name}?`,
        lines: [
            'Its disk is destroyed with it, and everything on it: what the guest wrote since it was created is gone.',
            'The image it was made from is not touched.',
        ],
        verb: 'Destroy',
        typed: vm.name,
    })
    if (!confirmed) return
    destroying = true
    note = null
    try {
        await destroyVM(vm.name)
        navigate('/vms')
    } catch (err) {
        note = {
            text: err instanceof ApiError ? err.statusText ?? `Could not destroy ${name}` : 'Could not reach Jail Deck',
            kind: 'error',
        }
        await load()
    } finally {
        destroying = false
    }
}

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
    <header class="page-head page-head--row">
        <div>
        <h1>{vm.name}</h1>
        <p class="page-sub">
            <span class="badge badge--{stateBadge[vm.state]}">{vm.state}</span>
            {#if vm.pid !== undefined}<span>pid {vm.pid}</span>{/if}
            {#if vm.locked_by}<span>held by {vm.locked_by}</span>{/if}
            <span>{vm.autostart ? `starts at boot, #${vm.autostart_order} in vm_list` : 'not started at boot'}</span>
        </p>
        </div>
        <div class="head-actions">
            <VMActions {vm} ondone={done} />
            {#if destroyable}
                <button type="button" class="btn btn--small btn--danger" onclick={destroy} disabled={destroying}>
                    {destroying ? 'Destroying…' : 'Destroy'}
                </button>
            {/if}
        </div>
    </header>

    {#if note}
        <p class="notice notice--{note.kind}" role={note.kind === 'error' ? 'alert' : 'status'}>{note.text}</p>
    {/if}

    <section class="panel">
        <h2>Configuration</h2>
        <dl class="facts">
            <dt>CPU</dt>
            <dd>
                {normalizeEmptyValue(vm.cpu ?? '')}
                {#if vm.cpuset?.length}
                    <span class="muted">pinned to host CPU{vm.cpuset.length > 1 ? 's' : ''} {vm.cpuset.join(', ')}</span>
                {/if}
            </dd>
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
            {#if vm.address}
                <dt>Connect to</dt>
                <dd><code>{vm.address.split('/')[0]}</code> <span class="muted">its address, {vm.address}</span></dd>
            {/if}
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

    <ConsolePanel path={`/vms/${encodeURIComponent(vm.name)}/console`} running={vm.state === 'running'}
                  openLabel="Open the console" onended={reason => { if (reason === 'guest-stopped') void load() }}>
        {#if consoles.length > 0}
            <dl class="facts">
                {#each consoles as [port, device] (port)}
                    <dt>{port}</dt>
                    <dd><code>{device}</code></dd>
                {/each}
            </dl>
            <p class="hint">
                The guest's first serial port, the host's end of a null-modem pair. One console at a
                time here; <code>vm console {vm.name}</code> from a root shell can still attach
                alongside it. <strong>Closing it does not log anyone out of the guest.</strong> It's
                audited, and nothing typed or shown is recorded.
            </p>
        {:else}
            <p class="notice">None while the guest is not running: the console belongs to its bhyve instance.</p>
        {/if}
    </ConsolePanel>

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

    <TagsPanel
        {name}
        noun="guest"
        properties={vm.properties}
        save={tags => setVMTags(name, tags)}
        changed={load}
    >
        {#snippet empty()}
            No ZFS user properties on this guest's dataset, <code>&lt;datastore&gt;/{vm.name}</code>.
            An automation client sets these — <code>otter:instance</code> and the like — and
            finds its guests again from the listing alone. A guest in a datastore that is not
            on ZFS has nowhere to keep one.
        {/snippet}
        {#snippet hint()}
            Kept on the guest's dataset, so they go with it through a rename and are gone with
            a destroy. Its disk volumes inherit them; the guest reports its own dataset's only.
        {/snippet}
    </TagsPanel>

    <InspectPanel noun="guest" read={() => getVMConfiguration(name)}>
        {#snippet absent(file)}
            {#if file.name === 'network-config'}
                Not there: the guest takes its address by DHCP, or has no network.
            {:else}
                Not there: the guest was made without a cloud-init seed.
            {/if}
        {/snippet}
    </InspectPanel>
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

.page-head--row {
    display: flex;
    flex-direction: row;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--space-4);
    flex-wrap: wrap;
}

.head-actions {
    display: flex;
    gap: var(--space-2);
    align-items: center;
}

.notice--warn {
    color: var(--warn);
    background: var(--warn-soft);
    border-color: color-mix(in srgb, var(--warn) 35%, transparent);
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
