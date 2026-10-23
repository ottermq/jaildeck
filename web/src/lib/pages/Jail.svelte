<script lang="ts">
import { onMount } from 'svelte'
import { getJail, listSnapshots, setTags, ensureDataset, retainDataset } from '../stores/jails.svelte'
import type { JailData, Snapshot, Dataset, PropertyConflict } from '../stores/jails.svelte'
import { ApiError } from '../services/api'
import { onNavClick } from '../router.svelte'
import { ask } from '../stores/confirm.svelte'
import { formatBytes, normalizeEmptyValue } from '../utils'

let { name }: { name: string } = $props()

let jail = $state<JailData | null>(null)
let snapshots = $state<Snapshot[] | null>(null)
let loadError = $state('')
let snapshotError = $state('')

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

let retaining = $state('')
let retainAs = $state('')

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
    <header class="page-head">
        <h1>{jail.name}</h1>
        <p class="page-sub">
            <span class="badge badge--{jail.status}">{jail.status}</span>
            <span>JID {normalizeEmptyValue(jail.jid)}</span>
            <span>{normalizeEmptyValue(jail.hostname)}</span>
            <span>{normalizeEmptyValue(jail.ip)}</span>
        </p>
    </header>

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
                                <td class="name"><code>{root.name}</code> <span class="note">the jail's own</span></td>
                                <td><code>{root.mountpoint}</code></td>
                                <td class="num">{formatBytes(root.used)}</td>
                                <td class="num">{formatBytes(root.referenced)}</td>
                                <td class="num">{formatBytes(root.available)}</td>
                                <td></td>
                            </tr>
                        {/if}
                        {#each children as child (child.name)}
                            <tr>
                                <td class="name"><code>{child.name}</code></td>
                                <td><code>{child.mountpoint}</code></td>
                                <td class="num">{formatBytes(child.used)}</td>
                                <td class="num">{formatBytes(child.referenced)}</td>
                                <td class="num">{formatBytes(child.available)}</td>
                                <td class="actions-col">
                                    <button
                                        type="button"
                                        class="btn btn--small"
                                        disabled={!!busy || retaining === child.name}
                                        onclick={() => startRetain(child)}
                                    >Retain</button>
                                </td>
                            </tr>
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
        <h2>Snapshots {#if snapshots}<span class="count">{snapshots.length}</span>{/if}</h2>
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
                        </tr>
                    </thead>
                    <tbody>
                        {#each newestFirst as snapshot (snapshot.name)}
                            <tr>
                                <td class="name"><code>@{snapshot.short}</code></td>
                                <td><code>{snapshot.dataset}</code></td>
                                <td class="nowrap">{when(snapshot.created)}</td>
                                <td class="num">{formatBytes(snapshot.used)}</td>
                            </tr>
                        {/each}
                    </tbody>
                </table>
            </div>
            <p class="hint">
                Newest first. The server orders them by transaction group rather than by
                time, because two snapshots taken in the same second share a timestamp.
            </p>
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
</style>
