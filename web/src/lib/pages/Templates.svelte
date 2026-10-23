<script lang="ts">
import { onMount } from 'svelte'
import { onDestroy } from 'svelte'
import {
    listTemplates, listRetained, lineage, deriveTemplate, removeTemplate, prepareRelease,
} from '../stores/templates.svelte'
import type { Template, RetainedDataset, Blocked } from '../stores/templates.svelte'
import { listJails, restoreDataset } from '../stores/jails.svelte'
import type { JailData } from '../stores/jails.svelte'
import { ApiError } from '../services/api'
import { onNavClick } from '../router.svelte'
import { ask } from '../stores/confirm.svelte'
import { formatBytes } from '../utils'

let templates = $state<Template[] | null>(null)
let retained = $state<RetainedDataset[] | null>(null)
let jails = $state<JailData[]>([])
let loadError = $state('')

async function load() {
    loadError = ''
    try {
        templates = await listTemplates()
        retained = await listRetained()
    } catch (err) {
        loadError = describe(err, 'Could not load templates')
        return
    }
    // Only needed to restore a dataset, so a failure costs that and nothing
    // else: the templates are what this page is for.
    try {
        jails = await listJails()
    } catch {
        jails = []
    }
}

onMount(load)

// ---- watching work in flight ------------------------------------------
//
// There is no job to ask about: the template is the resource, and its state
// comes back with every listing (JD-008). So the page reloads while anything
// is running, and stops when nothing is.
let working = $derived((templates ?? []).some(t => t.running))
let poll: ReturnType<typeof setInterval> | null = null

$effect(() => {
    if (working && poll === null) {
        poll = setInterval(load, 2000)
    } else if (!working && poll !== null) {
        clearInterval(poll)
        poll = null
    }
})

onDestroy(() => {
    if (poll !== null) clearInterval(poll)
})

function elapsed(since: string): string {
    const seconds = Math.max(0, Math.round((Date.now() - new Date(since).getTime()) / 1000))
    if (seconds < 60) return `${seconds}s`
    return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
}

function describe(err: unknown, fallback: string): string {
    if (!(err instanceof ApiError)) return 'Could not reach Jail Deck'
    return err.statusText ?? fallback
}

// Parent before child, with a depth to indent by. The indentation is the tree:
// a host has a handful of templates two or three deep, which an indented table
// carries without the expand, collapse and selection state a tree widget adds.
let rows = $derived(templates ? lineage(templates) : [])
// Only a ready template can be cloned from: without its @base snapshot there
// is nothing to clone (JD-007).
let parents = $derived((templates ?? []).filter(t => t.ready))

function href(name: string): string {
    return `/templates/${encodeURIComponent(name)}`
}

// ---- writing ----------------------------------------------------------
//
// Every action reloads when it is done, however it went: the server reads ZFS
// rather than remembering what it was told (JD-011), and a command that failed
// can still have changed something.

let busy = $state('')
let actionError = $state<Record<string, string>>({})
let note = $state('')
let blocked = $state<Record<string, Blocked>>({})

async function run(key: string, call: () => Promise<unknown>): Promise<boolean> {
    if (busy) return false
    busy = key
    delete actionError[key]
    try {
        await call()
        return true
    } catch (err) {
        actionError[key] = describe(err, 'That did not work')
        return false
    } finally {
        busy = ''
        await load()
    }
}

// ---- prepare a release --------------------------------------------------

let preparing = $state(false)
let releaseName = $state('')

function startPrepare() {
    preparing = true
    releaseName = ''
    note = ''
    delete actionError['prepare']
}

async function prepare(event: Event) {
    event.preventDefault()
    const release = releaseName.trim()
    const ok = await run('prepare', async () => {
        const template = await prepareRelease(release, release)
        note = template.state === 'ready'
            ? `${template.name} was already ready.`
            : `Preparing ${template.name}. It keeps going if you leave this page.`
    })
    if (ok) preparing = false
}

// ---- derive -----------------------------------------------------------

let deriving = $state(false)
let newName = $state('')
let newParent = $state('')
let newPackages = $state('')

function startDerive() {
    deriving = true
    newName = ''
    newParent = parents[0]?.name ?? ''
    newPackages = ''
    note = ''
    delete actionError['derive']
}

async function derive(event: Event) {
    event.preventDefault()
    // Split on whitespace and commas, because both are how someone would write
    // a package list and neither is a name pkg would accept.
    const packages = newPackages.split(/[\s,]+/).filter(p => p !== '')
    const ok = await run('derive', async () => {
        const template = await deriveTemplate(newName.trim(), newParent, packages)
        note = `${template.name} is ${template.ready ? 'ready' : 'not ready'}.`
    })
    if (ok) deriving = false
}

async function remove(template: Template) {
    const confirmed = await ask({
        title: `Remove the template ${template.name}?`,
        lines: [
            'The dataset and its snapshots are destroyed. Anything already cloned from it keeps running — this refuses rather than taking them along.',
            'Nothing cloned from it afterwards can exist, because the snapshot it would have cloned is gone.',
        ],
        verb: 'Remove',
        typed: template.name,
    })
    if (!confirmed) return

    await run(`remove-${template.name}`, async () => {
        const removal = await removeTemplate(template.name)
        if (removal.blocked) {
            blocked[template.name] = removal.blocked
            return
        }
        delete blocked[template.name]
        note = `Removed ${removal.template}.`
    })
}

// ---- restore ----------------------------------------------------------

let restoring = $state('')
let restoreTo = $state('')
let restoreAs = $state('')

function startRestore(dataset: RetainedDataset) {
    restoring = dataset.name
    restoreTo = jails[0]?.name ?? ''
    restoreAs = dataset.short
    delete actionError[`restore-${dataset.name}`]
}

async function restore(event: Event, dataset: RetainedDataset) {
    event.preventDefault()
    const ok = await run(`restore-${dataset.name}`, async () => {
        const moved = await restoreDataset(restoreTo, restoreAs.trim(), dataset.short)
        note = `${moved.from} is now ${moved.to}.`
    })
    if (ok) restoring = ''
}
</script>

<header class="page-head">
    <h1>Templates</h1>
    <p class="page-sub">The userlands a jail is cloned from, and what each one came from.</p>
</header>

{#if loadError}
    <p class="notice notice--error" role="alert">
        {loadError}
        <button type="button" class="btn btn--small" onclick={load}>Retry</button>
    </p>
{/if}

{#if note}
    <p class="notice" aria-live="polite">{note}</p>
{/if}

<section class="panel panel--first">
    <div class="panel__head">
        <h2 class="visually-hidden">Templates</h2>
        <div class="actions">
            <button type="button" class="btn btn--small" onclick={startPrepare} disabled={preparing}>
                Prepare a release
            </button>
            {#if parents.length > 0}
                <button type="button" class="btn btn--small" onclick={startDerive} disabled={deriving}>
                    Derive a template
                </button>
            {/if}
        </div>
    </div>

    {#if preparing}
        <form class="form" onsubmit={prepare}>
            <div class="form__row">
                <div class="field field--grow">
                    <label for="prepare-release">Release</label>
                    <input id="prepare-release" bind:value={releaseName} placeholder="15.1-RELEASE"
                           autocomplete="off" spellcheck="false" required />
                </div>
            </div>
            <p class="hint">
                Fetches that release's <code>base.txz</code> from the FreeBSD mirror and verifies it
                against the release's own MANIFEST. <strong>The release, never a patch level</strong> —
                the mirror publishes no <code>15.1-RELEASE-p5</code> directory. A userland must be the
                same as or older than the host's kernel, so the host's own release is the ceiling.
                This takes minutes and keeps going whether or not you stay here.
            </p>
            {#if actionError['prepare']}
                <p class="notice notice--error" role="alert">{actionError['prepare']}</p>
            {/if}
            <div class="form__actions">
                <button type="button" class="btn btn--small" onclick={() => (preparing = false)}>Cancel</button>
                <button type="submit" class="btn btn--small btn--primary" disabled={busy === 'prepare'}>
                    {busy === 'prepare' ? 'Starting…' : 'Prepare'}
                </button>
            </div>
        </form>
    {/if}

    {#if deriving}
        <form class="form" onsubmit={derive}>
            <div class="form__row">
                <div class="field">
                    <label for="derive-name">Name</label>
                    <input id="derive-name" bind:value={newName} placeholder="postgres-16"
                           autocomplete="off" spellcheck="false" required />
                </div>
                <div class="field">
                    <label for="derive-from">From</label>
                    <select id="derive-from" bind:value={newParent}>
                        {#each parents as parent (parent.name)}
                            <option value={parent.name}>{parent.name}</option>
                        {/each}
                    </select>
                </div>
                <div class="field field--grow">
                    <label for="derive-packages">Packages</label>
                    <input id="derive-packages" bind:value={newPackages} placeholder="postgresql16-server"
                           autocomplete="off" spellcheck="false" />
                </div>
            </div>
            <p class="hint">
                Three steps: clone the parent's <code>@base</code>, <code>pkg -c</code> the packages
                into the clone, snapshot the result. Nothing is started — no jail, no stanza, no
                networking. <strong>It installs packages and nothing else</strong>: for anything
                further, configure a jail and clone it as a template instead.
            </p>
            {#if actionError['derive']}
                <p class="notice notice--error" role="alert">{actionError['derive']}</p>
            {/if}
            <div class="form__actions">
                <button type="button" class="btn btn--small" onclick={() => (deriving = false)}>Cancel</button>
                <button type="submit" class="btn btn--small btn--primary" disabled={busy === 'derive'}>
                    {busy === 'derive' ? 'Building…' : 'Derive'}
                </button>
            </div>
        </form>
    {/if}

    {#if templates !== null && templates.length > 0}
        <div class="table-wrap">
            <table>
                <thead>
                    <tr>
                        <th>Template</th>
                        <th>State</th>
                        <th>Derived from</th>
                        <th class="num">Used</th>
                        <th class="num">Referenced</th>
                        <th class="actions-col">Actions</th>
                    </tr>
                </thead>
                <tbody>
                    {#each rows as { template, depth } (template.name)}
                        <tr>
                            <td class="name" style="padding-left: calc(var(--space-3) + {depth} * 1.25rem)">
                                {#if depth > 0}<span class="lineage" aria-hidden="true">└</span>{/if}
                                <a href={href(template.name)} onclick={(e) => onNavClick(e, href(template.name))}>
                                    {template.name}
                                </a>
                            </td>
                            <td>
                                {#if template.running}
                                    <span class="badge badge--timed-out">{template.state}</span>
                                    <div class="step">{template.running.step} · {elapsed(template.running.since)}</div>
                                {:else if template.ready}
                                    <span class="badge badge--running">ready</span>
                                {:else}
                                    <span class="badge badge--stopped">{template.state}</span>
                                {/if}
                            </td>
                            <td>{#if template.parent}<code>{template.parent}</code>{:else}—{/if}</td>
                            <td class="num">{formatBytes(template.used)}</td>
                            <td class="num">{formatBytes(template.referenced)}</td>
                            <td class="actions-col">
                                <button
                                    type="button"
                                    class="btn btn--small btn--danger"
                                    disabled={!!busy || !!template.running}
                                    title={template.running ? 'Something is working on this template' : undefined}
                                    onclick={() => remove(template)}
                                >Remove</button>
                            </td>
                        </tr>
                        {#if actionError[`remove-${template.name}`]}
                            <tr class="row-form">
                                <td colspan="6">
                                    <p class="notice notice--error" role="alert">
                                        {actionError[`remove-${template.name}`]}
                                    </p>
                                </td>
                            </tr>
                        {:else if blocked[template.name]}
                            {@const block = blocked[template.name]}
                            <tr class="row-form">
                                <td colspan="6">
                                    <div class="refusal" role="alert">
                                        <p class="refusal__reason">{block.reason}</p>
                                        <ul class="items">
                                            {#each [...(block.clones ?? []), ...(block.holds ?? [])] as item (item)}
                                                <li><code>{item}</code></li>
                                            {/each}
                                        </ul>
                                        <p class="hint">
                                            A jail that should outlive its template is not stuck:
                                            <code>zfs promote</code> on the jail moves the dependency the
                                            other way, which is cloning a jail as a template run in reverse.
                                        </p>
                                        <div class="form__actions">
                                            <button type="button" class="btn btn--small"
                                                    onclick={() => delete blocked[template.name]}>Dismiss</button>
                                        </div>
                                    </div>
                                </td>
                            </tr>
                        {/if}
                    {/each}
                </tbody>
            </table>
        </div>
        <p class="hint">
            A template is usable only once its <code>@base</code> snapshot exists. The state is
            read off the host — the archive is in <code>media/</code> or it is not, the dataset
            exists or it does not — so a reload, or a restart of Jail Deck, shows where the work
            actually got to rather than what it was last told. <em>extracted</em> is a template that
            exists and is not usable, which is also what a failed derive leaves behind.
            <code>Used</code> is what a derived template costs; the userland it shares with its
            parent is in <code>Referenced</code>.
        </p>
    {:else if templates !== null && !loadError}
        <p class="notice">
            No templates on this host. One arrives by preparing a release, or by configuring a
            jail and cloning it.
        </p>
    {:else if !loadError}
        <p class="notice">Loading templates…</p>
    {/if}
</section>

<section class="panel">
    <h2>Retained datasets</h2>
    {#if retained !== null && retained.length > 0}
        <div class="table-wrap">
            <table>
                <thead>
                    <tr>
                        <th>Name</th>
                        <th>Dataset</th>
                        <th class="num">Used</th>
                        <th class="actions-col">Actions</th>
                    </tr>
                </thead>
                <tbody>
                    {#each retained as dataset (dataset.name)}
                        <tr>
                            <td class="name">{dataset.short}</td>
                            <td><code>{dataset.name}</code></td>
                            <td class="num">{formatBytes(dataset.used)}</td>
                            <td class="actions-col">
                                <button
                                    type="button"
                                    class="btn btn--small"
                                    disabled={!!busy || jails.length === 0 || restoring === dataset.name}
                                    onclick={() => startRestore(dataset)}
                                >Restore</button>
                            </td>
                        </tr>
                        {#if restoring === dataset.name}
                            <tr class="row-form">
                                <td colspan="4">
                                    <form class="form" onsubmit={(e) => restore(e, dataset)}>
                                        <div class="form__row">
                                            <div class="field">
                                                <label for="restore-to">Under</label>
                                                <select id="restore-to" bind:value={restoreTo}>
                                                    {#each jails as jail (jail.name)}
                                                        <option value={jail.name}>{jail.name}</option>
                                                    {/each}
                                                </select>
                                            </div>
                                            <div class="field">
                                                <label for="restore-as">As</label>
                                                <input id="restore-as" bind:value={restoreAs}
                                                       autocomplete="off" spellcheck="false" required />
                                            </div>
                                        </div>
                                        <p class="hint">
                                            It need not be the jail it left, nor the name it left with. It
                                            unmounts and remounts, so a running jail gains that path
                                            mid-flight.
                                        </p>
                                        {#if actionError[`restore-${dataset.name}`]}
                                            <p class="notice notice--error" role="alert">
                                                {actionError[`restore-${dataset.name}`]}
                                            </p>
                                        {/if}
                                        <div class="form__actions">
                                            <button type="button" class="btn btn--small"
                                                    onclick={() => (restoring = '')}>Cancel</button>
                                            <button type="submit" class="btn btn--small btn--primary"
                                                    disabled={busy === `restore-${dataset.name}`}>
                                                {busy === `restore-${dataset.name}` ? 'Moving…' : 'Restore'}
                                            </button>
                                        </div>
                                    </form>
                                </td>
                            </tr>
                        {/if}
                    {/each}
                </tbody>
            </table>
        </div>
        <p class="hint">
            Datasets kept past the jails that held them. Destroying a jail takes its children
            with it, so anything worth keeping is moved out first.
        </p>
    {:else if retained !== null}
        <p class="notice">Nothing retained. Datasets appear here when they outlive their jail.</p>
    {/if}
</section>

<style>
.name {
    font-weight: 600;
    color: var(--text-strong);
}

.lineage {
    color: var(--text-muted);
    margin-right: var(--space-2);
    font-weight: 400;
}

.panel {
    margin-top: var(--space-6);
}

.panel--first {
    margin-top: 0;
}

.panel h2 {
    margin-bottom: var(--space-3);
}

.panel__head {
    display: flex;
    align-items: baseline;
    justify-content: flex-end;
    gap: var(--space-4);
    margin-bottom: var(--space-3);
}

.actions {
    display: flex;
    gap: var(--space-2);
}

.step {
    margin-top: var(--space-1);
    font-size: 0.75rem;
    color: var(--text-muted);
}

/* The table's own header says "Template", so a visible heading above it would
   only repeat itself; screen readers still get the section's name. */
.visually-hidden {
    position: absolute;
    width: 1px;
    height: 1px;
    margin: -1px;
    padding: 0;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
}

.form {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding: var(--space-4);
    margin-bottom: var(--space-4);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    background: var(--surface-sunken);
}

.form__row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
}

.form__actions {
    display: flex;
    gap: var(--space-2);
    justify-content: flex-end;
}

.field {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
}

.field--grow {
    flex: 1 1 16rem;
}

.row-form td {
    background: var(--surface-sunken);
}

.actions-col {
    text-align: right;
}

/* A refusal is not an error: nothing ran, and what it names is the answer. */
.refusal {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding: var(--space-3);
    border: 1px solid color-mix(in srgb, var(--warn) 35%, transparent);
    border-radius: var(--radius);
    background: var(--warn-soft);
}

.refusal__reason {
    color: var(--warn);
    font-size: 0.9rem;
}

.items {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    font-size: 0.85rem;
}

.hint {
    margin-top: var(--space-3);
    font-size: 0.85rem;
    color: var(--text-muted);
}

.form .hint,
.refusal .hint {
    margin-top: 0;
}
</style>
