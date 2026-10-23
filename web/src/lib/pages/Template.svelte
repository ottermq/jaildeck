<script lang="ts">
import { onDestroy, onMount } from 'svelte'
import { getTemplate, listTemplates, classifyClones, removeTemplate } from '../stores/templates.svelte'
import type { Template, TemplateDetail, Blocked } from '../stores/templates.svelte'
import { listJails } from '../stores/jails.svelte'
import type { JailData } from '../stores/jails.svelte'
import { ApiError } from '../services/api'
import { navigate, onNavClick } from '../router.svelte'
import { ask } from '../stores/confirm.svelte'
import { formatBytes } from '../utils'

let { name }: { name: string } = $props()

let template = $state<TemplateDetail | null>(null)
let loadError = $state('')

// What the clones are matched against. Empty when they could not be read,
// which costs the names and the links and nothing else: every clone then shows
// as the dataset the host named, which is still the truth.
let jailList = $state<JailData[]>([])
let templateList = $state<Template[]>([])

async function load() {
    loadError = ''
    try {
        template = await getTemplate(name)
    } catch (err) {
        template = null
        loadError = err instanceof ApiError
            ? err.statusText ?? `Could not load ${name}`
            : 'Could not reach Jail Deck'
        return
    }

    // Allowed to fail on their own: this page is about one template, and a
    // failure to list the others must not take it down with them.
    const [jailsResult, templatesResult] = await Promise.allSettled([listJails(), listTemplates()])
    jailList = jailsResult.status === 'fulfilled' ? jailsResult.value : []
    templateList = templatesResult.status === 'fulfilled' ? templatesResult.value : []
}

onMount(load)

// While something is working on this template, re-read it. There is no job to
// subscribe to: the template is the resource, and its state comes back with
// every read (JD-008).
let poll: ReturnType<typeof setInterval> | null = null

$effect(() => {
    if (template?.running && poll === null) {
        poll = setInterval(load, 2000)
    } else if (!template?.running && poll !== null) {
        clearInterval(poll)
        poll = null
    }
})

onDestroy(() => {
    if (poll !== null) clearInterval(poll)
})

// What depends on this template, split by what each clone turned out to be --
// a jail, another template, or something made outside Jail Deck. Worth
// separating: one is a running service, the next is more userlands, and the
// last is someone else's.
let clones = $derived(
    classifyClones(template?.dependents.clones ?? [], jailList, templateList)
)
let holds = $derived(template?.dependents.holds ?? [])
let parent = $derived(template?.parent ?? '')
let removable = $derived(template !== null && (template.dependents.clones.length + holds.length) === 0)

// ---- removing ---------------------------------------------------------

let busy = $state(false)
let removeError = $state('')
// A refusal is not a failure: nothing ran, and what it names is the answer.
// This page already lists the dependents, so a refusal here should agree with
// what is on screen -- and when it does not, the server is the one that just
// read ZFS.
let blocked = $state<Blocked | null>(null)

async function remove() {
    if (!template || busy) return
    const confirmed = await ask({
        title: `Remove the template ${template.name}?`,
        lines: [
            'The dataset and its snapshots are destroyed.',
            removable
                ? 'Nothing depends on it, so nothing else is affected.'
                : 'Everything listed below depends on it, and this will refuse rather than take them along.',
        ],
        verb: 'Remove',
        typed: template.name,
    })
    if (!confirmed) return

    busy = true
    removeError = ''
    blocked = null
    try {
        const removal = await removeTemplate(template.name)
        if (removal.blocked) {
            blocked = removal.blocked
            await load()
            return
        }
        // Gone, so there is no page left to show.
        navigate('/templates')
        return
    } catch (err) {
        removeError = err instanceof ApiError
            ? err.statusText ?? 'Could not remove the template'
            : 'Could not reach Jail Deck'
        await load()
    } finally {
        busy = false
    }
}
</script>

<nav class="crumbs" aria-label="Breadcrumb">
    <a href="/templates" onclick={(e) => onNavClick(e, '/templates')}>Templates</a>
    <span aria-hidden="true">/</span>
    <span class="crumbs__here">{name}</span>
</nav>

{#if loadError}
    <p class="notice notice--error" role="alert">
        {loadError}
        <button type="button" class="btn btn--small" onclick={load}>Retry</button>
    </p>
{:else if template === null}
    <p class="notice">Loading {name}…</p>
{:else}
    <header class="page-head page-head--row">
        <div>
            <h1>{template.name}</h1>
            <p class="page-sub">
            {#if template.running}
                <span class="badge badge--timed-out">{template.state}</span>
                <span class="step">{template.running.step}</span>
            {:else if template.ready}
                <span class="badge badge--running">ready</span>
            {:else}
                <span class="badge badge--stopped">{template.state}</span>
            {/if}
            <span>{formatBytes(template.used)} used</span>
            <span>{formatBytes(template.referenced)} referenced</span>
            {#if parent}
                <span>derived from
                    <a href={`/templates/${encodeURIComponent(parent)}`}
                       onclick={(e) => onNavClick(e, `/templates/${encodeURIComponent(parent)}`)}>
                        {parent}
                    </a>
                </span>
            {/if}
            </p>
        </div>
        <button type="button" class="btn btn--small btn--danger" disabled={busy} onclick={remove}>
            {busy ? 'Removing…' : 'Remove'}
        </button>
    </header>

    {#if removeError}
        <p class="notice notice--error" role="alert">{removeError}</p>
    {:else if blocked}
        <div class="refusal" role="alert">
            <p class="refusal__reason">{blocked.reason}</p>
            <ul class="deps">
                {#each [...(blocked.clones ?? []), ...(blocked.holds ?? [])] as item (item)}
                    <li><code>{item}</code></li>
                {/each}
            </ul>
            <div class="refusal__actions">
                <button type="button" class="btn btn--small" onclick={() => (blocked = null)}>Dismiss</button>
            </div>
        </div>
    {/if}

    {#if template.running}
        <p class="notice">
            Still being prepared. The state is read off the host, so leaving this page or
            restarting Jail Deck does not lose it — and the operations log says what each
            stage did.
        </p>
    {:else if !template.dataset}
        <p class="notice">
            The release archive has been fetched and nothing has been built from it yet.
            Extracting it into a dataset is the next stage.
        </p>
    {:else if !template.ready}
        <p class="notice notice--error">
            No <code>@base</code> snapshot, so nothing can be cloned from this. That is what a
            derive leaves behind when the package install failed part way through, and what a
            prepare that stopped early leaves too — remove it and try again rather than
            building on top of it.
        </p>
    {/if}

    <section class="panel">
        <h2>Where it lives</h2>
        {#if template.dataset}
            <dl class="facts">
                <dt>Dataset</dt>
                <dd><code>{template.dataset}</code></dd>
                <dt>Mounted at</dt>
                <dd><code>{template.mountpoint}</code></dd>
                {#if template.snapshot}
                    <dt>Clone source</dt>
                    <dd><code>{template.snapshot}</code></dd>
                {/if}
            </dl>
        {:else}
            <p class="notice">
                No dataset yet — only the archive under <code>media/</code>. A template with no
                dataset is one the later stages have not reached, which is why this page can
                show it at all: the resource exists from the moment the work starts.
            </p>
        {/if}
    </section>

    <section class="panel">
        <h2>What depends on this</h2>

        {#if removable}
            <p class="notice">
                Nothing. This template can be removed.
            </p>
        {:else}
            <p class="hint hint--lead">
                Destroying this template would take these with it, so each has to go first —
                or, for a jail that should outlive it, <code>zfs promote</code> on the jail
                moves the dependency the other way.
            </p>

            {#if clones.jails.length > 0}
                <h3>Jails cloned from it</h3>
                <ul class="deps">
                    {#each clones.jails as jail (jail.dataset)}
                        <li>
                            <a href={`/jails/${encodeURIComponent(jail.name)}`}
                               onclick={(e) => onNavClick(e, `/jails/${encodeURIComponent(jail.name)}`)}>
                                {jail.name}
                            </a>
                            <code class="muted">{jail.dataset}</code>
                        </li>
                    {/each}
                </ul>
            {/if}

            {#if clones.templates.length > 0}
                <h3>Templates derived from it</h3>
                <ul class="deps">
                    {#each clones.templates as derived (derived.dataset)}
                        <li>
                            <a href={`/templates/${encodeURIComponent(derived.name)}`}
                               onclick={(e) => onNavClick(e, `/templates/${encodeURIComponent(derived.name)}`)}>
                                {derived.name}
                            </a>
                            <code class="muted">{derived.dataset}</code>
                        </li>
                    {/each}
                </ul>
            {/if}

            {#if clones.other.length > 0}
                <h3>Other clones</h3>
                <ul class="deps">
                    {#each clones.other as dataset (dataset)}
                        <li><code>{dataset}</code></li>
                    {/each}
                </ul>
                <p class="hint">
                    Datasets cloned from this template that are neither a jail on this host nor
                    a template of its own.
                </p>
            {/if}

            {#if holds.length > 0}
                <h3>Holds</h3>
                <ul class="deps">
                    {#each holds as hold (hold)}
                        <li><code>{hold}</code></li>
                    {/each}
                </ul>
                <p class="hint">
                    A hold exists to say "not this one". Releasing it is a separate,
                    deliberate act.
                </p>
            {/if}
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

.step {
    font-size: 0.85rem;
    color: var(--text-muted);
}

.page-head--row {
    flex-direction: row;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--space-4);
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

.refusal__actions {
    display: flex;
    justify-content: flex-end;
}

.panel { margin-top: var(--space-6); }
.panel h2 { margin-bottom: var(--space-3); }

.panel h3 {
    font-size: 0.95rem;
    color: var(--text-muted);
    margin: var(--space-4) 0 var(--space-2);
}

.facts {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--space-2) var(--space-4);
    margin: 0;
}

.facts dt { margin: 0; color: var(--text-muted); }
.facts dd { margin: 0; }

.deps {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
}

.deps li {
    display: flex;
    gap: var(--space-3);
    align-items: baseline;
    flex-wrap: wrap;
}

.muted {
    color: var(--text-muted);
    font-size: 0.8rem;
}

.hint {
    margin-top: var(--space-3);
    font-size: 0.85rem;
    color: var(--text-muted);
}

.hint--lead { margin-top: 0; }
</style>
