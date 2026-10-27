<script lang="ts">
import { onDestroy, onMount } from 'svelte'
import { listImages, prepareImage, removeImage } from '../stores/vmImages.svelte'
import type { Image, ImageListing } from '../stores/vmImages.svelte'
import { ApiError } from '../services/api'
import { onNavClick } from '../router.svelte'
import { ask } from '../stores/confirm.svelte'
import { formatBytes } from '../utils'

let listing = $state<ImageListing | null>(null)
let loadError = $state('')

// Read on every visit and kept nowhere: the zvols are the record (JD-011).
async function load() {
    loadError = ''
    try {
        listing = await listImages()
    } catch (err) {
        loadError = describe(err, 'Could not load images')
        return
    }
    settle()
}

// ---- how work this page started came out -------------------------------
//
// The prepare call only says the work began. Whether it finished is the
// image's state once nothing is running for it: ready, or a stage that
// stopped, whose reason is in the audit log rather than anywhere here (JD-008).
let watching = $state<string[]>([])
let outcome = $state<{ text: string, kind: 'ok' | 'warn' } | null>(null)

function settle() {
    for (const name of watching) {
        const image = listing?.images.find(i => i.name === name)
        if (image?.running) continue
        watching = watching.filter(n => n !== name)
        note = ''
        outcome = image?.ready
            ? { text: `${name} is ready.`, kind: 'ok' }
            : { text: `${name} stopped before it was ready${image ? `, at ${image.state}` : ''}.`, kind: 'warn' }
    }
}

onMount(load)

// ---- watching work in flight ------------------------------------------
//
// There is no job to ask about: the image is the resource, and its state
// comes back with every listing (JD-008). The panel reloads while anything is
// running and stops when nothing is.
let working = $derived((listing?.images ?? []).some(i => i.running))
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

let usable = $derived(listing !== null && listing.vm_bhyve === 'ready' && !!listing.dataset)

// ---- writing ----------------------------------------------------------
//
// Every action reloads when it is done, however it went: a command that
// failed can still have changed something.

let busy = $state('')
let actionError = $state<Record<string, string>>({})
let note = $state('')

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

let preparing = $state(false)
let newName = $state('')
let newSource = $state('')
let source = $derived(listing?.sources.find(s => s.name === newSource))

function startPrepare() {
    preparing = true
    newSource = listing?.sources[0]?.name ?? ''
    // The source's own name is the obvious name for the first image of it.
    newName = newSource
    note = ''
    delete actionError['prepare']
}

async function prepare(event: Event) {
    event.preventDefault()
    const ok = await run('prepare', async () => {
        const image = await prepareImage(newName.trim(), newSource)
        outcome = null
        if (image.state === 'ready') {
            note = `${image.name} was already ready.`
            return
        }
        note = `Preparing ${image.name}. It keeps going if you leave this page.`
        watching = [...watching, image.name]
    })
    if (ok) preparing = false
}

async function remove(image: Image) {
    const confirmed = await ask({
        title: `Remove the image ${image.name}?`,
        lines: [
            'Its zvol and @base snapshot are destroyed. The downloaded archive is kept, so preparing it again starts at the write.',
            'This refuses while any guest’s disk is cloned from it.',
        ],
        verb: 'Remove',
        typed: image.name,
    })
    if (!confirmed) return

    await run(`remove-${image.name}`, async () => {
        const removal = await removeImage(image.name)
        outcome = null
        note = `Removed ${removal.name}.`
    })
}

function vmHref(name: string): string {
    return `/vms/${encodeURIComponent(name)}`
}
</script>

<section class="panel" aria-labelledby="images-heading">
    <div class="panel__head">
        <h2 id="images-heading">Images</h2>
        {#if usable}
            <button type="button" class="btn btn--small" onclick={startPrepare} disabled={preparing}>
                Prepare an image
            </button>
        {/if}
    </div>
    <p class="page-sub">The cloud images a guest’s disk is cloned from.</p>

    {#if loadError}
        <p class="notice notice--error" role="alert">
            {loadError}
            <button type="button" class="btn btn--small" onclick={load}>Retry</button>
        </p>
    {:else if listing === null}
        <p class="notice">Loading images…</p>
    {:else if !usable}
        <p class="notice notice--warn" role="status">
            Images cannot be prepared on this host: {listing.problem}
        </p>
    {:else}
        {#if note}
            <p class="notice" aria-live="polite">{note}</p>
        {/if}
        {#if outcome?.kind === 'ok'}
            <p class="notice" aria-live="polite">{outcome.text}</p>
        {:else if outcome}
            <p class="notice notice--warn" role="status">
                {outcome.text}
                <a href="/operations" onclick={(e) => onNavClick(e, '/operations')}>Operations</a> has what went wrong.
            </p>
        {/if}

        {#if preparing}
            <form class="form" onsubmit={prepare}>
                <div class="form__row">
                    <div class="field">
                        <label for="image-source">Source</label>
                        <select id="image-source" bind:value={newSource}>
                            {#each listing.sources as s (s.name)}
                                <option value={s.name}>{s.name}</option>
                            {/each}
                        </select>
                    </div>
                    <div class="field field--grow">
                        <label for="image-name">Name</label>
                        <input id="image-name" bind:value={newName} autocomplete="off" spellcheck="false" required />
                    </div>
                </div>
                {#if source}
                    <p class="hint">
                        {source.description}.
                        {#if source.fetched}
                            Its archive is already on this host, so this only writes it into a new zvol —
                            under a minute.
                        {:else}
                            Downloads <code class="url">{source.url}</code>, checks it against the
                            publisher’s checksums, and writes its disk into a zvol. Minutes of work, and it
                            keeps going whether or not you stay here.
                        {/if}
                    </p>
                {/if}
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

        {#if listing.images.length > 0}
            <div class="table-wrap">
                <table>
                    <thead>
                        <tr>
                            <th>Image</th>
                            <th>State</th>
                            <th>Source</th>
                            <th class="num">Disk</th>
                            <th class="num">Used</th>
                            <th>Guests</th>
                            <th class="actions-col">Actions</th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each listing.images as image (image.name)}
                            <tr>
                                <td class="name">{image.name}</td>
                                <td>
                                    {#if image.running}
                                        <span class="badge badge--timed-out">{image.state}</span>
                                        <div class="step">{image.running.step} · {elapsed(image.running.since)}</div>
                                    {:else if image.ready}
                                        <span class="badge badge--running">ready</span>
                                    {:else}
                                        <span class="badge badge--stopped">{image.state}</span>
                                    {/if}
                                </td>
                                <td>
                                    {image.source ?? image.running?.source ?? '—'}
                                    {#if image.sha512}
                                        <div class="step" title={`SHA-512 of the archive: ${image.sha512}`}>
                                            build <code>{image.sha512.slice(0, 12)}</code>
                                        </div>
                                    {/if}
                                </td>
                                <td class="num">{image.size ? formatBytes(image.size) : '—'}</td>
                                <td class="num">{image.used ? formatBytes(image.used) : '—'}</td>
                                <td>
                                    {#each image.guests as guest, i (guest)}
                                        {#if i > 0}, {/if}<a href={vmHref(guest)} onclick={(e) => onNavClick(e, vmHref(guest))}>{guest}</a>
                                    {:else}
                                        —
                                    {/each}
                                </td>
                                <td class="actions-col">
                                    <button
                                        type="button"
                                        class="btn btn--small btn--danger"
                                        disabled={!!busy || !!image.running}
                                        title={image.running ? 'This image is being prepared' : undefined}
                                        onclick={() => remove(image)}
                                    >Remove</button>
                                </td>
                            </tr>
                            {#if actionError[`remove-${image.name}`]}
                                <tr class="row-note">
                                    <td colspan="7"><span role="alert">{actionError[`remove-${image.name}`]}</span></td>
                                </tr>
                            {/if}
                        {/each}
                    </tbody>
                </table>
            </div>
            <p class="hint">
                An image is usable once its <code>@base</code> snapshot exists. <em>written</em> is a
                zvol without one: a write that stopped, to be removed and prepared again. The state is
                read off the host, so a reload or a restart of Jail Deck shows where the work actually
                got to. <code>Disk</code> is the smallest a guest’s disk can be; <code>Used</code> is
                what the image costs the pool.
            </p>
        {:else}
            <p class="notice">No images yet. A guest is created from one, so preparing one comes first.</p>
        {/if}
    {/if}
</section>

<style>
.panel {
    margin-top: var(--space-6);
}

.notice {
    margin-bottom: var(--space-3);
}

.panel__head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-4);
}

.page-sub {
    margin: var(--space-1) 0 var(--space-3);
}

.name {
    font-weight: 600;
    color: var(--text-strong);
}

.step {
    margin-top: var(--space-1);
    font-size: 0.75rem;
    color: var(--text-muted);
}

.actions-col {
    text-align: right;
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

/* A URL is one long word, which would otherwise push the page sideways at
   phone width. */
.url {
    overflow-wrap: anywhere;
}

.row-note td {
    font-size: 0.875rem;
    color: var(--danger);
    background: var(--danger-soft);
}

.notice--warn {
    color: var(--warn);
    background: var(--warn-soft);
    border-color: color-mix(in srgb, var(--warn) 35%, transparent);
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
