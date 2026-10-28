<script lang="ts">
import { onMount } from 'svelte'
import {
    getJail, listSnapshots, setTags, ensureDataset, retainDataset,
    createSnapshot, destroySnapshot, rollbackSnapshot, makeTemplate, recloneJail,
    destroyJail,
} from '../stores/jails.svelte'
import type {
    JailData, Snapshot, Dataset, PropertyConflict, SnapshotBlock, Reclone, Destroyed,
} from '../stores/jails.svelte'
import { listTemplates } from '../stores/templates.svelte'
import type { Template } from '../stores/templates.svelte'
import { ApiError } from '../services/api'
import { onNavClick, navigate } from '../router.svelte'
import { ask } from '../stores/confirm.svelte'
import { formatBytes, normalizeEmptyValue } from '../utils'
import Console from '../components/Console.svelte'

let { name }: { name: string } = $props()

// The console (JD-042): a shell in the jail, open until something ends it.
// consoleRun remounts the terminal for "Open again".
let consoleOpen = $state(false)
let consoleRun = $state(0)
let consoleEnded = $state<string | null>(null)

const consoleReasons: Record<string, string> = {
    'client': 'Closed.',
    'shell-exited': 'The shell exited.',
    'jail-stopped': 'The jail stopped, and its console with it.',
    'session-expired': 'Your login ended. Sign in again to open another console.',
    'idle': 'Closed: nothing was typed for a while.',
}

function consoleClosed(reason: string) {
    consoleEnded = consoleReasons[reason] ?? 'The connection ended, or could not be made.'
    // The jail stopped from somewhere else -- another tab, the API: the page
    // still says running until it reads the jail again.
    if (reason === 'jail-stopped') void load()
}

function openConsole() {
    consoleEnded = null
    consoleRun += 1
    consoleOpen = true
}

let jail = $state<JailData | null>(null)
let snapshots = $state<Snapshot[] | null>(null)
let loadError = $state('')
let snapshotError = $state('')
// Ready templates only: without its @base snapshot there is nothing to clone
// from (JD-007).
let templates = $state<Template[]>([])

async function load() {
    loadError = ''
    try {
        jail = await getJail(name)
    } catch (err) {
        jail = null
        loadError = describe(err, `Could not load ${name}`)
        return
    }
    // Asked separately, and allowed to fail on its own: a jail with an
    // unreadable snapshot listing is still a jail worth showing.
    try {
        snapshots = await listSnapshots(name)
    } catch (err) {
        snapshots = []
        snapshotError = describe(err, 'Could not load snapshots')
    }
    // Only needed to offer a root to re-clone from. A failure costs that
    // button and nothing else.
    try {
        templates = (await listTemplates()).filter(t => t.ready)
    } catch {
        templates = []
    }
}

onMount(load)

function describe(err: unknown, fallback: string): string {
    if (!(err instanceof ApiError)) return 'Could not reach Jail Deck'
    return err.statusText ?? fallback
}

// The jail's own dataset is the one mounted exactly at its path; the rest are
// children. Shown apart because their sizes are not the jail's — a child with
// a refquota is counted separately on purpose (JD-007).
let root = $derived(jail?.datasets?.find(d => d.mountpoint === jail?.path))
let children = $derived(jail?.datasets?.filter(d => d !== root) ?? [])
let tags = $derived(Object.entries(jail?.properties ?? {}).sort(([a], [b]) => a.localeCompare(b)))

// Newest first: the one you are looking for is almost always the last one
// taken. The server returns them oldest first, ordered by transaction group.
let newestFirst = $derived([...(snapshots ?? [])].reverse())

function when(iso: string): string {
    return new Date(iso).toLocaleString()
}

// The name the API wants for a child is the part after the jail's own dataset,
// which is one level: a slash is refused at creation (JD-023).
function childOf(dataset: Dataset): string {
    return root ? dataset.name.slice(root.name.length + 1) : dataset.name
}

// ---- writing ----------------------------------------------------------
//
// Everything below reloads the jail when it is done, however it went. The
// server reads ZFS rather than remembering what it was told (JD-011), so a
// refetch is the only answer that cannot disagree with the host — and a
// command that failed can still have changed something.

// One operation at a time, named so the button that started it can say so.
let busy = $state('')
// Keyed by the form or row it belongs to, so a failed retain of one dataset
// does not put its message under another.
let actionError = $state<Record<string, string>>({})

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

// ---- tags -------------------------------------------------------------

let addingTag = $state(false)
let newTagName = $state('')
let newTagValue = $state('')
let editingTag = $state('')
let editedValue = $state('')

function startAddTag() {
    addingTag = true
    newTagName = ''
    newTagValue = ''
    delete actionError['tag-add']
}

async function addTag(event: Event) {
    event.preventDefault()
    if (await run('tag-add', () => setTags(name, { [newTagName.trim()]: newTagValue }))) {
        addingTag = false
    }
}

function startEditTag(property: string, value: string) {
    editingTag = property
    editedValue = value
    delete actionError['tag-edit']
}

async function saveTag(event: Event) {
    event.preventDefault()
    if (await run('tag-edit', () => setTags(name, { [editingTag]: editedValue }))) {
        editingTag = ''
    }
}

async function removeTag(property: string) {
    const confirmed = await ask({
        title: `Remove ${property} from ${name}?`,
        lines: [
            'The property is inherited away rather than set empty, so the jail stops carrying it at all.',
            'An automation client that finds its jails by this tag will stop finding this one.',
        ],
        verb: 'Remove',
    })
    if (!confirmed) return
    // Null, not "": removing a property and setting it to the empty string are
    // different states, and the API draws that line.
    await run('tag-remove', () => setTags(name, { [property]: null }))
}

// ---- datasets ---------------------------------------------------------

// The four native properties worth offering at creation. mountpoint is the
// fifth the API allows and is deliberately absent: a child inherits the right
// one from the jail, and the API will not move it afterwards (JD-023).
const datasetProperties = [
    { name: 'refquota', hint: 'The limit this dataset may use, as 2G or 512M. Where JD-007 puts a jail’s quota.' },
    { name: 'recordsize', hint: 'A power of two from 512B to 16M. Leave it alone unless the workload asks.' },
    { name: 'compression', hint: 'lz4, zstd, off — whatever this ZFS supports.' },
    { name: 'atime', hint: 'on or off. Off stops a read from being a write.' },
]

let addingDataset = $state(false)
let newChild = $state('')
let newProperties = $state<Record<string, string>>({})

function startAddDataset() {
    addingDataset = true
    newChild = ''
    newProperties = {}
    delete actionError['dataset-add']
}

async function addDataset(event: Event) {
    event.preventDefault()
    // Only what was filled in: an empty string is a value zfs would reject,
    // not a request to leave the property alone.
    const properties: Record<string, string> = {}
    for (const [property, value] of Object.entries(newProperties)) {
        if (value.trim() !== '') properties[property] = value.trim()
    }
    if (busy) return
    busy = 'dataset-add'
    delete actionError['dataset-add']
    try {
        await ensureDataset(name, newChild.trim(), properties)
        addingDataset = false
    } catch (err) {
        actionError['dataset-add'] = conflictMessage(err) ?? describe(err, 'Could not create the dataset')
    } finally {
        busy = ''
        await load()
    }
}

// A 409 from this endpoint is not a sentence: the dataset already exists with
// a property this asked to change, and the body carries both values rather
// than a message (JD-023). Without this the client would show "Conflict",
// which is the one thing the API went out of its way not to say.
function conflictMessage(err: unknown): string | null {
    if (!(err instanceof ApiError) || err.statusCode !== 409) return null
    const conflict = (err.body as { conflict?: PropertyConflict } | undefined)?.conflict
    if (!conflict) return null
    return `It already exists with ${conflict.property}=${conflict.have}, and this asked for `
        + `${conflict.want}. Jail Deck will not move a mounted dataset.`
}

let editingDataset = $state('')
let editedProperties = $state<Record<string, string>>({})

function startEditDataset(dataset: Dataset) {
    editingDataset = dataset.name
    // Prefilled with what the dataset actually sets, so an untouched field
    // sends the value back unchanged rather than silently dropping it.
    editedProperties = { ...(dataset.properties ?? {}) }
    delete actionError[`properties-${dataset.name}`]
}

async function saveDataset(event: Event, dataset: Dataset) {
    event.preventDefault()
    const properties: Record<string, string> = {}
    for (const [property, value] of Object.entries(editedProperties)) {
        if (value.trim() !== '') properties[property] = value.trim()
    }
    const ok = await run(`properties-${dataset.name}`, () =>
        ensureDataset(name, childOf(dataset), properties))
    if (ok) editingDataset = ''
}

// Sorted so the same dataset always reads the same way, rather than in
// whatever order the map came back in.
function propertyEntries(dataset: Dataset): [string, string][] {
    return Object.entries(dataset.properties ?? {}).sort(([a], [b]) => a.localeCompare(b))
}

let retaining = $state('')
let retainAs = $state('')

// ---- the whole jail ---------------------------------------------------

let cloning = $state(false)
let templateName = $state('')

function startClone() {
    cloning = true
    templateName = `${name}-base`
    jailNote = ''
    delete actionError['clone']
}

async function cloneAsTemplate(event: Event) {
    event.preventDefault()
    const ok = await run('clone', async () => {
        const made = await makeTemplate(name, templateName.trim())
        jailNote = `${made.name} is now a template at ${made.dataset}.`
    })
    if (ok) cloning = false
}

let recloning = $state(false)
let recloneFrom = $state('')
let jailNote = $state('')
// A re-clone that failed part way through. Not an error message: it says which
// step stopped and where every dataset now sits, and that is the only record
// of where they went (JD-026).
let recloneFailure = $state<Reclone | null>(null)

function startReclone() {
    recloning = true
    recloneFrom = templates[0]?.snapshot ?? ''
    jailNote = ''
    recloneFailure = null
    delete actionError['reclone']
}

// What a destroy refused over, or got as far as doing. Kept on the page
// rather than shown once and lost: a blocked destroy names things that have
// to be dealt with one at a time, and a partial one is the only record of
// where the jail got to (JD-030).
let destroyResult = $state<Destroyed | null>(null)

async function destroyThisJail() {
    const running = jail?.status === 'running'
    const children = (jail?.datasets ?? []).filter(d => d.mountpoint !== jail?.path)
    const confirmed = await ask({
        title: `Destroy ${name}?`,
        lines: [
            running
                ? 'The jail is stopped first, taken out of rc.conf’s jail_list, its configuration file deleted, and its dataset destroyed.'
                : 'The jail is taken out of rc.conf’s jail_list, its configuration file deleted, and its dataset destroyed.',
            children.length > 0
                ? 'Its child datasets go with it. They are listed below — anything worth keeping has to be retained first.'
                : 'Its dataset and everything in it go. Nothing here can be undone.',
            'A snapshot, a clone of one, or a hold refuses the whole thing and nothing is touched.',
        ],
        items: children.map(d => d.name),
        verb: 'Destroy',
        typed: name,
    })
    if (!confirmed) return

    if (busy) return
    busy = 'destroy'
    delete actionError['destroy']
    destroyResult = null
    try {
        const result = await destroyJail(name, name)
        if (result.destroyed) {
            // Nothing left to show on this page: the jail it describes is
            // gone, and the list is where the operator goes next.
            navigate('/jails')
            return
        }
        // Refused, with nothing done. The body says what stood in the way.
        destroyResult = result
    } catch (err) {
        destroyResult = (err instanceof ApiError && isPartialDestroy(err.body)) ? err.body : null
        actionError['destroy'] = describe(err, `Could not destroy ${name}`)
    } finally {
        busy = ''
        await load()
    }
}

function isPartialDestroy(body: unknown): body is Destroyed {
    return typeof body === 'object' && body !== null && 'destroyed' in body
}

async function reclone(event: Event) {
    event.preventDefault()
    const from = recloneFrom
    const confirmed = await ask({
        title: `Replace ${name}'s root from ${from}?`,
        lines: [
            'The jail is stopped, its child datasets are moved aside, the old root is parked, the new one is cloned, the children are moved back, and the jail is started again. It ends in the state it was found in.',
            'Nothing is destroyed — the old root is kept under retained/, so a re-clone onto the wrong template costs one call to undo.',
            'Everything written to the root itself since it was cloned is not in the new one. State that has to survive belongs in a child dataset.',
        ],
        verb: 'Re-clone',
        typed: name,
    })
    if (!confirmed) return

    if (busy) return
    busy = 'reclone'
    delete actionError['reclone']
    recloneFailure = null
    try {
        const result = await recloneJail(name, from)
        recloning = false
        // `to` is where the new root came from; `from` is what it used to be a
        // clone of. Reporting `from` here would describe the upgrade as the
        // thing it replaced.
        const children = result.children.length
        jailNote = `Root re-cloned from ${result.to}, replacing a clone of ${result.from}.`
            + ` The old one is parked at ${result.old_root}`
            + (children === 0
                ? ' and nothing else moved.'
                : `, and ${children === 1 ? 'one child dataset' : `${children} child datasets`} came back with it.`)
    } catch (err) {
        // A failure after the datasets started moving carries the recovery
        // information; an error message alone would throw it away.
        const body = err instanceof ApiError ? (err.body as Reclone | undefined) : undefined
        if (body?.failed_at) {
            recloneFailure = body
        }
        actionError['reclone'] = describe(err, 'Could not re-clone the root')
    } finally {
        busy = ''
        await load()
    }
}

// ---- snapshots --------------------------------------------------------

// Where a snapshot lives, in the terms the API takes: an empty child is the
// jail's own dataset.
function childFor(snapshot: Snapshot): string {
    if (!root || snapshot.dataset === root.name) return ''
    return snapshot.dataset.slice(root.name.length + 1)
}

// What a destroy would actually take. At the jail's own dataset it is
// recursive and removes every snapshot of that name under the jail -- the same
// set the server computes, read off the list already on screen so the
// confirmation can name them before anything runs.
function alsoDestroyed(snapshot: Snapshot): string[] {
    if (childFor(snapshot) !== '') return [snapshot.name]
    return (snapshots ?? []).filter(s => s.short === snapshot.short).map(s => s.name)
}

let takingSnapshot = $state(false)
let newSnapshotName = $state('')
let snapshotTarget = $state('')
let snapshotNote = $state('')
// Refusals, keyed by the snapshot they are about, so one blocked rollback does
// not put its explanation under another row.
let blocked = $state<Record<string, SnapshotBlock>>({})

function startTakeSnapshot() {
    takingSnapshot = true
    newSnapshotName = ''
    snapshotTarget = ''
    snapshotNote = ''
    delete actionError['snapshot-take']
}

async function takeSnapshot(event: Event) {
    event.preventDefault()
    snapshotNote = ''
    const short = newSnapshotName.trim()
    const ok = await run('snapshot-take', async () => {
        const result = await createSnapshot(name, snapshotTarget, short)
        snapshotNote = result.existed
            ? `${result.snapshot} was already there, so nothing was taken.`
            : `Took ${result.created.length === 1 ? 'one snapshot' : `${result.created.length} snapshots`}.`
    })
    if (ok) takingSnapshot = false
}

async function destroy(snapshot: Snapshot) {
    const targets = alsoDestroyed(snapshot)
    const recursive = childFor(snapshot) === ''
    const confirmed = await ask({
        title: `Destroy ${snapshot.name}?`,
        lines: [
            'A snapshot is the only way back to the point it was taken. Destroying it cannot be undone.',
            ...(recursive && targets.length > 1
                ? ['This one is on the jail\u2019s own dataset, so the destroy is recursive: every snapshot of that name under the jail goes with it.']
                : []),
        ],
        items: targets.length > 1 ? targets : undefined,
        verb: 'Destroy',
        typed: snapshot.name,
    })
    if (!confirmed) return

    await run(`snapshot-${snapshot.name}`, async () => {
        const result = await destroySnapshot(name, childFor(snapshot), snapshot.short, snapshot.name)
        if (result.blocked) {
            blocked[snapshot.name] = result.blocked
            return
        }
        delete blocked[snapshot.name]
        snapshotNote = `Destroyed ${result.destroyed.length === 1 ? 'one snapshot' : `${result.destroyed.length} snapshots`}.`
    })
}

async function startRollback(snapshot: Snapshot) {
    const confirmed = await ask({
        title: `Roll ${snapshot.dataset} back to @${snapshot.short}?`,
        lines: [
            'Everything written to that dataset since the snapshot was taken is discarded.',
            'Not recursive \u2014 zfs rollback cannot descend into children at all, so a child dataset keeps whatever is in it.',
        ],
        verb: 'Roll back',
        typed: snapshot.name,
    })
    if (!confirmed) return
    await rollback(snapshot, false)
}

// The second step of a refused rollback. The first attempt is what produced
// the list of newer snapshots; accepting it is a different decision from
// rolling back, so it is asked separately -- and the name has already been
// typed for this operation, so what the dialog adds here is the list.
async function acceptNewer(snapshot: Snapshot, newer: string[]) {
    const confirmed = await ask({
        title: `Destroy ${newer.length === 1 ? 'one newer snapshot' : `${newer.length} newer snapshots`} and roll back?`,
        lines: ['These were taken after the one being rolled back to. A rollback cannot keep them.'],
        items: newer,
        verb: 'Destroy and roll back',
    })
    if (!confirmed) return
    await rollback(snapshot, true)
}

async function rollback(snapshot: Snapshot, destroyNewer: boolean) {
    await run(`snapshot-${snapshot.name}`, async () => {
        const result = await rollbackSnapshot(
            name, childFor(snapshot), snapshot.short, snapshot.name, destroyNewer)
        if (result.blocked) {
            blocked[snapshot.name] = result.blocked
            return
        }
        delete blocked[snapshot.name]
        snapshotNote = result.destroyed.length > 0
            ? `Rolled back to ${result.snapshot}, destroying ${result.destroyed.length} newer.`
            : `Rolled back to ${result.snapshot}.`
    })
}

function startRetain(dataset: Dataset) {
    retaining = dataset.name
    retainAs = `${name}-${childOf(dataset)}`
    delete actionError[`retain-${dataset.name}`]
}

async function retain(event: Event, dataset: Dataset) {
    event.preventDefault()
    if (await run(`retain-${dataset.name}`, () => retainDataset(name, childOf(dataset), retainAs.trim()))) {
        retaining = ''
    }
}
</script>

<nav class="crumbs" aria-label="Breadcrumb">
    <a href="/jails" onclick={(e) => onNavClick(e, '/jails')}>Jails</a>
    <span aria-hidden="true">/</span>
    <span class="crumbs__here">{name}</span>
</nav>

{#if loadError}
    <p class="notice notice--error" role="alert">
        {loadError}
        <button type="button" class="btn btn--small" onclick={load}>Retry</button>
    </p>
{:else if jail === null}
    <p class="notice">Loading {name}…</p>
{:else}
    <header class="page-head page-head--row">
        <div>
            <h1>{jail.name}</h1>
            <p class="page-sub">
                <span class="badge badge--{jail.status}">{jail.status}</span>
                <span>JID {normalizeEmptyValue(jail.jid)}</span>
                <span>{normalizeEmptyValue(jail.hostname)}</span>
                <span>{normalizeEmptyValue(jail.ip)}</span>
            </p>
        </div>
        {#if jail.path}
            <div class="actions">
                <button type="button" class="btn btn--small" onclick={startClone} disabled={cloning || !!busy}>
                    Clone as a template
                </button>
                <button
                    type="button"
                    class="btn btn--small"
                    onclick={startReclone}
                    disabled={recloning || !!busy || templates.length === 0}
                    title={templates.length === 0 ? 'No ready template to clone a new root from' : undefined}
                >Re-clone the root</button>
            </div>
        {/if}
    </header>

    {#if jailNote}
        <p class="notice" aria-live="polite">{jailNote}</p>
    {/if}

    {#if cloning}
        <form class="form" onsubmit={cloneAsTemplate}>
            <div class="form__row">
                <div class="field field--grow">
                    <label for="template-name">Template name</label>
                    <input id="template-name" bind:value={templateName}
                           autocomplete="off" spellcheck="false" required />
                </div>
            </div>
            <p class="hint">
                Snapshot this jail as <code>@base</code>, clone it into the templates dataset, then
                <code>zfs promote</code> the clone — which is the step that matters: without it the
                template is a clone <em>of</em> this jail, and this jail could never be destroyed.
                It captures whatever you did to the jail, not only packages.
                <strong>The jail must be stopped</strong>, and this refuses rather than stopping it.
            </p>
            {#if actionError['clone']}
                <p class="notice notice--error" role="alert">{actionError['clone']}</p>
            {/if}
            <div class="form__actions">
                <button type="button" class="btn btn--small" onclick={() => (cloning = false)}>Cancel</button>
                <button type="submit" class="btn btn--small btn--primary" disabled={busy === 'clone'}>
                    {busy === 'clone' ? 'Cloning…' : 'Clone'}
                </button>
            </div>
        </form>
    {/if}

    {#if recloning}
        <form class="form" onsubmit={reclone}>
            <div class="form__row">
                <div class="field field--grow">
                    <label for="reclone-from">New root cloned from</label>
                    <select id="reclone-from" bind:value={recloneFrom}>
                        {#each templates as template (template.name)}
                            <option value={template.snapshot}>{template.name} — {template.snapshot}</option>
                        {/each}
                    </select>
                </div>
            </div>
            <p class="hint">
                The upgrade path for a jail whose state lives in a child dataset. There is no
                default: every jail is already a clone of some template's <code>@base</code>, so
                defaulting to one would be a no-op dressed as an upgrade.
            </p>
            {#if actionError['reclone'] && !recloneFailure}
                <p class="notice notice--error" role="alert">{actionError['reclone']}</p>
            {/if}
            <div class="form__actions">
                <button type="button" class="btn btn--small" onclick={() => (recloning = false)}>Cancel</button>
                <button type="submit" class="btn btn--small btn--danger" disabled={busy === 'reclone'}>
                    {busy === 'reclone' ? 'Re-cloning…' : 'Re-clone'}
                </button>
            </div>
        </form>
    {/if}

    {#if recloneFailure}
        <div class="notice notice--error failure" role="alert">
            <p><strong>The re-clone stopped at “{recloneFailure.failed_at}”.</strong>
               {actionError['reclone']}</p>
            <p>
                Nothing was retried and nothing was undone: an automatic undo that itself failed
                would leave two interleaved failures and a host neither describes. These datasets
                are no longer under the jail, and this is where they are now.
            </p>
            {#if recloneFailure.parked && recloneFailure.parked.length > 0}
                <ul class="items">
                    {#each recloneFailure.parked as moved (moved.now)}
                        <li><code>{moved.was}</code> → <code>{moved.now}</code></li>
                    {/each}
                </ul>
                <p>
                    Each can be brought back with a restore, from the Templates page.
                </p>
            {/if}
            <div class="form__actions">
                <button type="button" class="btn btn--small" onclick={() => (recloneFailure = null)}>Dismiss</button>
            </div>
        </div>
    {/if}

    {#if jail?.status === 'running' || consoleOpen}
        <section class="panel" aria-labelledby="console-heading">
            <div class="panel__head">
                <h2 id="console-heading">Console</h2>
                {#if !consoleOpen}
                    <button type="button" class="btn btn--small" onclick={openConsole}>Open a shell</button>
                {:else if consoleEnded !== null}
                    <div class="console-actions">
                        {#if jail?.status === 'running'}
                            <button type="button" class="btn btn--small" onclick={openConsole}>Open again</button>
                        {/if}
                        <button type="button" class="btn btn--small" onclick={() => (consoleOpen = false)}>Dismiss</button>
                    </div>
                {:else}
                    <button type="button" class="btn btn--small" onclick={() => (consoleOpen = false)}>Close</button>
                {/if}
            </div>
            {#if consoleOpen}
                {#key consoleRun}
                    <Console path={`/jails/${encodeURIComponent(name)}/console`} onclosed={consoleClosed} />
                {/key}
                {#if consoleEnded !== null}
                    <p class="notice" role="status">{consoleEnded}</p>
                {/if}
            {/if}
            <p class="hint">
                A root shell inside the jail. It's audited: who opened it, for how long, and why it
                closed — <strong>nothing you type or see is recorded</strong>. It closes when the jail
                stops, when your login ends, or after a while with nothing typed.
            </p>
        </section>
    {/if}

    <section class="panel">
        <div class="panel__head">
            <h2>Datasets</h2>
            {#if jail.path}
                <button type="button" class="btn btn--small" onclick={startAddDataset} disabled={addingDataset}>
                    Add a dataset
                </button>
            {/if}
        </div>

        {#if addingDataset}
            <form class="form" onsubmit={addDataset}>
                <div class="form__row">
                    <div class="field">
                        <label for="child-name">Name</label>
                        <input id="child-name" bind:value={newChild} autocomplete="off" spellcheck="false" required />
                    </div>
                    {#each datasetProperties as property (property.name)}
                        <div class="field">
                            <label for={`property-${property.name}`}>{property.name}</label>
                            <input
                                id={`property-${property.name}`}
                                bind:value={newProperties[property.name]}
                                autocomplete="off"
                                spellcheck="false"
                                placeholder="inherit"
                                title={property.hint}
                            />
                        </div>
                    {/each}
                </div>
                <p class="hint">
                    One level only — a slash would make a nested dataset, which is two calls.
                    An empty box leaves the property inherited rather than setting it empty.
                    This is idempotent: naming a dataset that already exists converges it
                    instead of failing.
                </p>
                {#if actionError['dataset-add']}
                    <p class="notice notice--error" role="alert">{actionError['dataset-add']}</p>
                {/if}
                <div class="form__actions">
                    <button type="button" class="btn btn--small" onclick={() => (addingDataset = false)}>Cancel</button>
                    <button type="submit" class="btn btn--small btn--primary" disabled={busy === 'dataset-add'}>
                        {busy === 'dataset-add' ? 'Creating…' : 'Create'}
                    </button>
                </div>
            </form>
        {/if}

        {#if !jail.path}
            <p class="notice">
                This jail's stanza sets no <code>path</code>, so nothing ties it to ZFS.
            </p>
        {:else if !jail.datasets || jail.datasets.length === 0}
            <p class="notice">
                No dataset is mounted at <code>{jail.path}</code>. That also reads this way
                when the pool could not be read — the reason is in the service log.
            </p>
        {:else}
            <div class="table-wrap">
                <table>
                    <thead>
                        <tr>
                            <th>Dataset</th>
                            <th>Mounted at</th>
                            <th class="num">Used</th>
                            <th class="num">Referenced</th>
                            <th class="num">Available</th>
                            <th class="actions-col">Actions</th>
                        </tr>
                    </thead>
                    <tbody>
                        {#if root}
                            <tr>
                                <td class="name">
                                    <code>{root.name}</code> <span class="note">the jail's own</span>
                                    {#if propertyEntries(root).length > 0}
                                        <div class="props">
                                            {#each propertyEntries(root) as [property, value] (property)}
                                                <span class="prop">{property}={value}</span>
                                            {/each}
                                        </div>
                                    {/if}
                                </td>
                                <td><code>{root.mountpoint}</code></td>
                                <td class="num">{formatBytes(root.used)}</td>
                                <td class="num">{formatBytes(root.referenced)}</td>
                                <td class="num">{formatBytes(root.available)}</td>
                                <td></td>
                            </tr>
                        {/if}
                        {#each children as child (child.name)}
                            <tr>
                                <td class="name">
                                    <code>{child.name}</code>
                                    {#if propertyEntries(child).length > 0}
                                        <div class="props">
                                            {#each propertyEntries(child) as [property, value] (property)}
                                                <span class="prop">{property}={value}</span>
                                            {/each}
                                        </div>
                                    {/if}
                                </td>
                                <td><code>{child.mountpoint}</code></td>
                                <td class="num">{formatBytes(child.used)}</td>
                                <td class="num">{formatBytes(child.referenced)}</td>
                                <td class="num">{formatBytes(child.available)}</td>
                                <td class="actions-col">
                                    <div class="actions">
                                        <button
                                            type="button"
                                            class="btn btn--small"
                                            disabled={!!busy || editingDataset === child.name}
                                            onclick={() => startEditDataset(child)}
                                        >Properties</button>
                                        <button
                                            type="button"
                                            class="btn btn--small"
                                            disabled={!!busy || retaining === child.name}
                                            onclick={() => startRetain(child)}
                                        >Retain</button>
                                    </div>
                                </td>
                            </tr>
                            {#if editingDataset === child.name}
                                <tr class="row-form">
                                    <td colspan="6">
                                        <form class="form" onsubmit={(e) => saveDataset(e, child)}>
                                            <div class="form__row">
                                                {#each datasetProperties as property (property.name)}
                                                    <div class="field">
                                                        <label for={`edit-${property.name}`}>{property.name}</label>
                                                        <input
                                                            id={`edit-${property.name}`}
                                                            bind:value={editedProperties[property.name]}
                                                            autocomplete="off"
                                                            spellcheck="false"
                                                            placeholder="unset"
                                                            title={property.hint}
                                                        />
                                                    </div>
                                                {/each}
                                            </div>
                                            <p class="hint">
                                                These are what the dataset sets itself. An empty box leaves the
                                                property alone rather than removing it — there is no route here
                                                that makes a dataset inherit one again. Values go to ZFS
                                                unchecked, so <code>recordsize=7K</code> comes back as zfs's
                                                own complaint.
                                            </p>
                                            {#if actionError[`properties-${child.name}`]}
                                                <p class="notice notice--error" role="alert">
                                                    {actionError[`properties-${child.name}`]}
                                                </p>
                                            {/if}
                                            <div class="form__actions">
                                                <button type="button" class="btn btn--small"
                                                        onclick={() => (editingDataset = '')}>Cancel</button>
                                                <button type="submit" class="btn btn--small btn--primary"
                                                        disabled={busy === `properties-${child.name}`}>
                                                    {busy === `properties-${child.name}` ? 'Saving…' : 'Save'}
                                                </button>
                                            </div>
                                        </form>
                                    </td>
                                </tr>
                            {/if}
                            {#if retaining === child.name}
                                <tr class="row-form">
                                    <td colspan="6">
                                        <form class="form" onsubmit={(e) => retain(e, child)}>
                                            <div class="form__row">
                                                <div class="field">
                                                    <label for="retain-as">Keep it as</label>
                                                    <input id="retain-as" bind:value={retainAs} autocomplete="off" spellcheck="false" required />
                                                </div>
                                            </div>
                                            <p class="hint">
                                                It moves to <code>&lt;pool&gt;/jails/retained/{retainAs}</code> and stops being
                                                this jail's child, so destroying the jail no longer takes it along.
                                                Nothing is destroyed. It does unmount and remount, though — a running
                                                jail loses that path the moment it moves.
                                            </p>
                                            {#if actionError[`retain-${child.name}`]}
                                                <p class="notice notice--error" role="alert">{actionError[`retain-${child.name}`]}</p>
                                            {/if}
                                            <div class="form__actions">
                                                <button type="button" class="btn btn--small" onclick={() => (retaining = '')}>Cancel</button>
                                                <button type="submit" class="btn btn--small btn--primary" disabled={busy === `retain-${child.name}`}>
                                                    {busy === `retain-${child.name}` ? 'Moving…' : 'Retain'}
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
            {#if root && root.referenced > root.used}
                <p class="hint">
                    This jail refers to more than it uses, because it is a clone and still
                    shares its template's blocks. That is why a quota belongs on a child
                    dataset rather than here.
                </p>
            {/if}
            <p class="hint">
                A dataset's properties are the ones it sets itself; one it inherits from the
                dataset above belongs to that one. Only a child's can be changed here — the
                API writes properties to a jail's children, not to the dataset the jail runs
                from.
            </p>
        {/if}
    </section>

    <section class="panel">
        <div class="panel__head">
            <h2>Tags</h2>
            {#if jail.path}
                <button type="button" class="btn btn--small" onclick={startAddTag} disabled={addingTag}>
                    Add a tag
                </button>
            {/if}
        </div>

        {#if addingTag}
            <form class="form" onsubmit={addTag}>
                <div class="form__row">
                    <div class="field">
                        <label for="tag-name">Property</label>
                        <input id="tag-name" bind:value={newTagName} placeholder="otter:instance"
                               autocomplete="off" spellcheck="false" required />
                    </div>
                    <div class="field field--grow">
                        <label for="tag-value">Value</label>
                        <input id="tag-value" bind:value={newTagValue} autocomplete="off" spellcheck="false" />
                    </div>
                </div>
                <p class="hint">
                    A ZFS user property: the name has to contain a colon, which is what tells
                    one from a native property. The value is passed through untouched.
                </p>
                {#if actionError['tag-add']}
                    <p class="notice notice--error" role="alert">{actionError['tag-add']}</p>
                {/if}
                <div class="form__actions">
                    <button type="button" class="btn btn--small" onclick={() => (addingTag = false)}>Cancel</button>
                    <button type="submit" class="btn btn--small btn--primary" disabled={busy === 'tag-add'}>
                        {busy === 'tag-add' ? 'Setting…' : 'Set'}
                    </button>
                </div>
            </form>
        {/if}

        {#if actionError['tag-remove']}
            <p class="notice notice--error" role="alert">{actionError['tag-remove']}</p>
        {/if}

        {#if tags.length === 0}
            <p class="notice">
                No ZFS user properties on this jail's own dataset. An automation client sets
                these — <code>otter:instance</code> and the like — and finds its jails again
                from the listing alone.
            </p>
        {:else}
            <div class="table-wrap">
                <table>
                    <thead>
                        <tr>
                            <th>Property</th>
                            <th>Value</th>
                            <th class="actions-col">Actions</th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each tags as [property, value] (property)}
                            <tr>
                                <td class="name"><code>{property}</code></td>
                                <td>
                                    {#if editingTag === property}
                                        <form class="inline-edit" onsubmit={saveTag}>
                                            <input bind:value={editedValue} autocomplete="off" spellcheck="false"
                                                   aria-label={`Value for ${property}`} />
                                            <button type="submit" class="btn btn--small btn--primary" disabled={busy === 'tag-edit'}>
                                                {busy === 'tag-edit' ? 'Saving…' : 'Save'}
                                            </button>
                                            <button type="button" class="btn btn--small" onclick={() => (editingTag = '')}>Cancel</button>
                                        </form>
                                    {:else}
                                        {value}
                                    {/if}
                                </td>
                                <td class="actions-col">
                                    <div class="actions">
                                        <button
                                            type="button"
                                            class="btn btn--small"
                                            disabled={!!busy || editingTag === property}
                                            onclick={() => startEditTag(property, value)}
                                        >Edit</button>
                                        <button
                                            type="button"
                                            class="btn btn--small btn--danger"
                                            disabled={!!busy}
                                            onclick={() => removeTag(property)}
                                        >Remove</button>
                                    </div>
                                </td>
                            </tr>
                            {#if editingTag === property && actionError['tag-edit']}
                                <tr class="row-form">
                                    <td colspan="3">
                                        <p class="notice notice--error" role="alert">{actionError['tag-edit']}</p>
                                    </td>
                                </tr>
                            {/if}
                        {/each}
                    </tbody>
                </table>
            </div>
            <p class="hint">
                A jail reports its own dataset's properties, never a child's — otherwise one
                jail with three children would look like four instances.
            </p>
        {/if}
    </section>

    <section class="panel">
        <div class="panel__head">
            <h2>Snapshots {#if snapshots}<span class="count">{snapshots.length}</span>{/if}</h2>
            {#if jail.path}
                <button type="button" class="btn btn--small" onclick={startTakeSnapshot} disabled={takingSnapshot}>
                    Take a snapshot
                </button>
            {/if}
        </div>

        {#if takingSnapshot}
            <form class="form" onsubmit={takeSnapshot}>
                <div class="form__row">
                    <div class="field">
                        <label for="snapshot-name">Name</label>
                        <input id="snapshot-name" bind:value={newSnapshotName} placeholder="before-upgrade"
                               autocomplete="off" spellcheck="false" required />
                    </div>
                    <div class="field field--grow">
                        <label for="snapshot-target">Of</label>
                        <select id="snapshot-target" bind:value={snapshotTarget}>
                            <option value="">The whole jail — every dataset at one point in time</option>
                            {#each children as child (child.name)}
                                <option value={childOf(child)}>{child.name} — that dataset alone</option>
                            {/each}
                        </select>
                    </div>
                </div>
                <p class="hint">
                    A snapshot of the whole jail is one <code>zfs snapshot -r</code>, so every dataset
                    is caught at the same instant. Taking one that already exists changes nothing and
                    says so, which is what makes repeating a timed-out call safe.
                </p>
                {#if actionError['snapshot-take']}
                    <p class="notice notice--error" role="alert">{actionError['snapshot-take']}</p>
                {/if}
                <div class="form__actions">
                    <button type="button" class="btn btn--small" onclick={() => (takingSnapshot = false)}>Cancel</button>
                    <button type="submit" class="btn btn--small btn--primary" disabled={busy === 'snapshot-take'}>
                        {busy === 'snapshot-take' ? 'Taking…' : 'Take'}
                    </button>
                </div>
            </form>
        {/if}

        {#if snapshotNote}
            <p class="notice" aria-live="polite">{snapshotNote}</p>
        {/if}

        {#if snapshotError}
            <p class="notice notice--error" role="alert">{snapshotError}</p>
        {:else if snapshots === null}
            <p class="notice">Loading snapshots…</p>
        {:else if snapshots.length === 0}
            <p class="notice">No snapshots of this jail's datasets.</p>
        {:else}
            <div class="table-wrap">
                <table>
                    <thead>
                        <tr>
                            <th>Snapshot</th>
                            <th>Dataset</th>
                            <th>Taken</th>
                            <th class="num">Used</th>
                            <th class="actions-col">Actions</th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each newestFirst as snapshot (snapshot.name)}
                            <tr>
                                <td class="name"><code>@{snapshot.short}</code></td>
                                <td><code>{snapshot.dataset}</code></td>
                                <td class="nowrap">{when(snapshot.created)}</td>
                                <td class="num">{formatBytes(snapshot.used)}</td>
                                <td class="actions-col">
                                    <div class="actions">
                                        <button
                                            type="button"
                                            class="btn btn--small"
                                            disabled={!!busy}
                                            onclick={() => startRollback(snapshot)}
                                        >Roll back</button>
                                        <button
                                            type="button"
                                            class="btn btn--small btn--danger"
                                            disabled={!!busy}
                                            onclick={() => destroy(snapshot)}
                                        >Destroy</button>
                                    </div>
                                </td>
                            </tr>
                            {#if actionError[`snapshot-${snapshot.name}`]}
                                <tr class="row-form">
                                    <td colspan="5">
                                        <p class="notice notice--error" role="alert">
                                            {actionError[`snapshot-${snapshot.name}`]}
                                        </p>
                                    </td>
                                </tr>
                            {:else if blocked[snapshot.name]}
                                {@const block = blocked[snapshot.name]}
                                <tr class="row-form">
                                    <td colspan="5">
                                        <div class="refusal" role="alert">
                                            <p class="refusal__reason">{block.reason}</p>
                                            <ul class="items">
                                                {#each [...(block.newer ?? []), ...(block.clones ?? []), ...(block.holds ?? [])] as item (item)}
                                                    <li><code>{item}</code></li>
                                                {/each}
                                            </ul>
                                            <div class="form__actions">
                                                <button type="button" class="btn btn--small"
                                                        onclick={() => delete blocked[snapshot.name]}>Dismiss</button>
                                                {#if block.newer && block.newer.length > 0}
                                                    <button
                                                        type="button"
                                                        class="btn btn--small btn--danger"
                                                        disabled={!!busy}
                                                        onclick={() => acceptNewer(snapshot, block.newer ?? [])}
                                                    >Destroy them and roll back</button>
                                                {/if}
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
                Newest first. The server orders them by transaction group rather than by
                time, because two snapshots taken in the same second share a timestamp.
                Destroying one on the jail's own dataset is recursive and takes every
                snapshot of that name with it; the confirmation names them first.
            </p>
        {/if}
    </section>

    <section class="panel panel--danger">
        <div class="panel__head">
            <h2>Destroy</h2>
            <button
                type="button"
                class="btn btn--small btn--danger"
                onclick={destroyThisJail}
                disabled={!!busy || !jail.path}
                title={jail.path ? undefined : 'This jail has no dataset of its own'}
            >{busy === 'destroy' ? 'Destroying…' : `Destroy ${jail.name}`}</button>
        </div>

        {#if !jail.path}
            <p class="hint">
                This jail has no dataset of its own, so there is nothing here to destroy.
                Its block has to be removed from the configuration by hand.
            </p>
        {/if}

        <p class="hint">
            Stops the jail, takes it out of <code>jail_list</code>, deletes its file in
            <code>/etc/jail.conf.d</code>, and destroys its dataset — in that order, and
            nothing is undone if a step fails. <strong>Its child datasets go with it.</strong>
            A dataset worth keeping is retained first, which moves it out of the jail.
        </p>

        {#if actionError['destroy']}
            <p class="notice notice--error" role="alert">{actionError['destroy']}</p>
        {/if}

        {#if destroyResult?.blocked}
            <div class="notice notice--error blocked" role="alert">
                <p>{destroyResult.blocked.reason}</p>
                {#each [
                    { label: 'Snapshots', items: destroyResult.blocked.snapshots },
                    { label: 'Clones', items: destroyResult.blocked.clones },
                    { label: 'Holds', items: destroyResult.blocked.holds },
                ] as group (group.label)}
                    {#if group.items?.length}
                        <p class="blocked__group">
                            <strong>{group.label}</strong>
                            {#each group.items as item (item)}<code>{item}</code>{/each}
                        </p>
                    {/if}
                {/each}
            </div>
        {:else if destroyResult && !destroyResult.destroyed}
            <div class="notice notice--error" role="alert">
                <p>
                    Stopped at <strong>{destroyResult.failed_at}</strong>. Nothing was undone:
                    {destroyResult.stopped ? 'the jail was stopped' : 'the jail was not stopped'}{destroyResult.unlisted ? ', taken out of jail_list' : ''}{destroyResult.config ? `, and ${destroyResult.config} was deleted` : ''}.
                    {#if destroyResult.root}Its dataset is still <code>{destroyResult.root}</code>.{/if}
                </p>
            </div>
        {/if}
    </section>
{/if}

<style>
.console-actions {
    display: flex;
    gap: var(--space-2);
}

/* The one section on this page that destroys something. Marked rather than
   hidden: an operator looking for it should find it, and should not reach it
   by accident. */
.panel--danger {
    margin-top: var(--space-6);
    padding: var(--space-4);
    border: 1px solid var(--danger);
    border-radius: var(--radius);
    background: var(--danger-soft);
}

.panel--danger .panel__head {
    margin-bottom: var(--space-2);
}

.blocked {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    align-items: flex-start;
}

.blocked__group {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    align-items: baseline;
    font-size: 0.85rem;
}

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
    flex-direction: row;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--space-4);
}

/* A failed re-clone is the one message on this page worth making hard to
   miss: it is the only record of where the jail's datasets went. */
.failure {
    flex-direction: column;
    align-items: stretch;
    gap: var(--space-3);
    font-size: 0.925rem;
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

.panel h2 {
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

.note {
    font-weight: 400;
    font-size: 0.75rem;
    color: var(--text-muted);
}

.props {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1);
    margin-top: var(--space-1);
}

.prop {
    font-family: var(--mono);
    font-size: 0.75rem;
    font-weight: 400;
    padding: 0 0.35em;
    border-radius: var(--radius-sm);
    background: var(--surface-sunken);
    border: 1px solid var(--border);
    color: var(--text-muted);
}

/* A form opens where the thing it acts on is, rather than over it: these
   change state but destroy nothing, and a modal for each would be ceremony.
   The confirmation dialog is kept for what cannot be undone. */
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
    flex: 1 1 14rem;
}

.inline-edit {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    flex-wrap: wrap;
}

.row-form td {
    background: var(--surface-sunken);
}

.actions-col {
    text-align: right;
}

.actions {
    display: flex;
    gap: var(--space-2);
    justify-content: flex-end;
}

.hint {
    margin-top: var(--space-3);
    font-size: 0.85rem;
    color: var(--text-muted);
}

.form .hint {
    margin-top: 0;
}

/* A refusal is not an error: nothing ran, and what it names is the answer.
   Carried in the warning colour rather than the danger one for that reason. */
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
</style>
