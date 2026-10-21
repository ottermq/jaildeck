<script lang="ts">
import { onMount } from 'svelte'
import { listJails, runJailOperation } from '../stores/jails.svelte'
import type { JailData, Operation } from '../stores/jails.svelte'
import { ApiError } from '../services/api'
import { capitalizeFirstLetter, normalizeEmptyValue } from '../utils'

let jails = $state<JailData[] | null>(null)
let loadError = $state('')

let pending = $state<Record<string, Operation>>({})
let rowError = $state<Record<string, string>>({})

async function load() {
    loadError = ''
    try {
        jails = await listJails()
    } catch (err) {
        loadError = err instanceof ApiError
            ? err.statusText ?? 'Could not load jails'
            : 'Could not reach Jail Deck'
    }
}

onMount(() => {
    load()
})

async function run(j: JailData, op: Operation) {
    if (pending[j.name]) return

    if (op !== 'start' && !confirm(`${capitalizeFirstLetter(op)} jail "${j.name}"? Anything running inside it will be interrupted.`)) return

    pending[j.name] = op
    delete rowError[j.name]
    try {
        await runJailOperation(j.name, op)
    } catch (err) {
        rowError[j.name] = describe(err, op, j.name)
    } finally {
        delete pending[j.name]
        // Refetched whichever way it went: a failed command can still have
        // changed the jail's state, so the list is the only source of truth.
        await load()
    }
}

function describe(err: unknown, op: Operation, name: string): string {
    if (!(err instanceof ApiError)) return 'Could not reach Jail Deck'
    if (err.statusCode === 504) {
        return `${capitalizeFirstLetter(op)} of ${name} timed out. It may still be running — the list has been refreshed.`
    }
    return err.statusText ?? `Could not ${op} ${name}.`
}

const labels: Record<Operation, string> = {
    start: 'Starting…',
    stop: 'Stopping…',
    restart: 'Restarting…',
}
</script>

<header class="page-head">
    <h1>Jails</h1>
    <p class="page-sub">Every jail this host knows about, configured or running.</p>
</header>

{#if loadError}
    <p class="notice notice--error" role="alert">
        {loadError}
        <button type="button" class="btn btn--small" onclick={load}>Retry</button>
    </p>
{/if}

{#if jails !== null && jails.length > 0}
    <div class="table-wrap">
        <table>
            <thead>
                <tr>
                    <th>Status</th>
                    <th>Name</th>
                    <th class="num">JID</th>
                    <th>Hostname</th>
                    <th>IP</th>
                    <th>Path</th>
                    <th class="actions-col">Actions</th>
                </tr>
            </thead>
            <tbody>
                {#each jails as j (j.name)}
                    <tr>
                        <td>
                            <span class="badge badge--{j.status}">{j.status}</span>
                        </td>
                        <td class="name">{j.name}</td>
                        <td class="num">{normalizeEmptyValue(j.jid)}</td>
                        <td>{normalizeEmptyValue(j.hostname)}</td>
                        <td class="nowrap">{normalizeEmptyValue(j.ip)}</td>
                        <td>{#if j.path}<code>{j.path}</code>{:else}—{/if}</td>
                        <td>
                            <div class="actions">
                                {#if j.status === 'running'}
                                    <button
                                        type="button"
                                        class="btn btn--small btn--danger"
                                        disabled={!!pending[j.name]}
                                        aria-busy={!!pending[j.name]}
                                        aria-label={`Stop ${j.name}`}
                                        onclick={() => run(j, 'stop')}
                                    >
                                        {pending[j.name] === 'stop' ? labels.stop : 'Stop'}
                                    </button>
                                    <button
                                        type="button"
                                        class="btn btn--small btn--danger"
                                        disabled={!!pending[j.name]}
                                        aria-busy={!!pending[j.name]}
                                        aria-label={`Restart ${j.name}`}
                                        onclick={() => run(j, 'restart')}
                                    >
                                        {pending[j.name] === 'restart' ? labels.restart : 'Restart'}
                                    </button>
                                {:else}
                                    <button
                                        type="button"
                                        class="btn btn--small btn--primary"
                                        disabled={!!pending[j.name]}
                                        aria-busy={!!pending[j.name]}
                                        aria-label={`Start ${j.name}`}
                                        onclick={() => run(j, 'start')}
                                    >
                                        {pending[j.name] === 'start' ? labels.start : 'Start'}
                                    </button>
                                {/if}
                            </div>
                        </td>
                    </tr>
                    {#if rowError[j.name]}
                        <tr class="row-error">
                            <td colspan="7" aria-live="polite">{rowError[j.name]}</td>
                        </tr>
                    {/if}
                {/each}
            </tbody>
        </table>
    </div>
{:else if jails === null && !loadError}
    <p class="notice">Loading jails…</p>
{:else if !loadError}
    <p class="notice">No jails are configured on this host.</p>
{/if}

<style>
.name {
    font-weight: 600;
    color: var(--text-strong);
}

.actions-col {
    text-align: right;
}

.actions {
    display: flex;
    gap: var(--space-2);
    justify-content: flex-end;
}

.row-error td {
    color: var(--danger);
    background: var(--danger-soft);
    font-size: 0.875rem;
}
</style>
