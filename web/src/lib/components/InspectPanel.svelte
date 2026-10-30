<script lang="ts">
import type { Snippet } from 'svelte'

// What the host has for a jail or a guest, file by file, read only (#127,
// JD-048). Closed until asked for, so the page itself reads nothing more;
// opened, it reads the host now, and Reload reads it again.
export interface InspectFile {
    name: string
    path: string
    present: boolean
    content?: string
    truncated?: boolean
    masked?: string[]
}

export interface Inspected {
    files: InspectFile[]
    // A jail's rctl rules; null when they could not be read, with why.
    rules?: string[] | null
    rulesError?: string
}

let {
    noun,
    read,
    absent,
}: {
    noun: 'jail' | 'guest'
    read: () => Promise<Inspected>
    // What a file that is not there means, by its name.
    absent: Snippet<[InspectFile]>
} = $props()

let open = $state(false)
let loading = $state(false)
let loadError = $state('')
let inspected = $state<Inspected | null>(null)
let copied = $state('')

async function load() {
    loading = true
    loadError = ''
    try {
        inspected = await read()
    } catch (err) {
        loadError = err instanceof Error && err.message ? err.message : `Could not read the ${noun}'s files`
    } finally {
        loading = false
    }
}

async function toggle() {
    open = !open
    if (open && !inspected) await load()
}

const canCopy = typeof navigator !== 'undefined' && !!navigator.clipboard && window.isSecureContext

async function copy(file: InspectFile) {
    try {
        await navigator.clipboard.writeText(file.content ?? '')
        copied = file.path
        setTimeout(() => { if (copied === file.path) copied = '' }, 2000)
    } catch {
        // The text stays selectable.
    }
}
</script>

<section class="panel" aria-labelledby="inspect-heading">
    <div class="panel__head">
        <h2 id="inspect-heading">Inspect</h2>
        <div class="actions">
            {#if open}
                <button type="button" class="btn btn--small" onclick={load} disabled={loading}>Reload</button>
            {/if}
            <button type="button" class="btn btn--small" onclick={toggle} aria-expanded={open}>
                {open ? 'Hide' : 'Show the files'}
            </button>
        </div>
    </div>
    <p class="hint">
        The {noun}'s files as they are on the host now, read only: what Jail Deck wrote, and
        anything changed since.
    </p>

    {#if open}
        {#if loading && !inspected}
            <p class="notice">Reading…</p>
        {:else if loadError}
            <p class="notice notice--error" role="alert">{loadError}</p>
        {/if}
        {#if inspected}
            {#each inspected.files as file (file.path)}
                <div class="file">
                    <div class="file__head">
                        <code class="path">{file.path}</code>
                        {#if file.present && canCopy}
                            <button type="button" class="btn btn--small" onclick={() => copy(file)}>
                                {copied === file.path ? 'Copied' : 'Copy'}
                            </button>
                        {/if}
                    </div>
                    {#if file.present}
                        <pre class="content">{file.content}</pre>
                        {#if file.masked?.length}
                            <p class="hint">
                                Masked: the value of <code>{file.masked.join(', ')}</code>. A console
                                password's hash can be guessed at offline, so it is never shown.
                            </p>
                        {/if}
                        {#if file.truncated}
                            <p class="hint">Cut at 64 KiB; the file goes on.</p>
                        {/if}
                    {:else}
                        <p class="hint">{@render absent(file)}</p>
                    {/if}
                </div>
            {/each}

            {#if inspected.rules !== undefined}
                <h3>rctl rules</h3>
                {#if inspected.rules === null}
                    <p class="notice notice--error">Could not read them: {inspected.rulesError}</p>
                {:else if inspected.rules.length === 0}
                    <p class="hint">None keyed to this jail's name.</p>
                {:else}
                    <ul class="rules">
                        {#each inspected.rules as rule (rule)}
                            <li><code>{rule}</code></li>
                        {/each}
                    </ul>
                {/if}
            {/if}
        {/if}
    {/if}
</section>

<style>
.panel {
    margin-top: var(--space-6);
}

.panel__head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-4);
    margin-bottom: var(--space-2);
}

.panel__head h2 {
    margin-bottom: 0;
}

.actions {
    display: flex;
    gap: var(--space-2);
}

.hint {
    margin-top: var(--space-2);
    font-size: 0.85rem;
    color: var(--text-muted);
}

.notice {
    margin-top: var(--space-3);
}

.file {
    margin-top: var(--space-4);
}

.file__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
    min-width: 0;
}

.path {
    overflow-wrap: anywhere;
    color: var(--text-strong);
}

.content {
    margin: var(--space-2) 0 0;
    padding: var(--space-3);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    background: var(--surface-sunken);
    font-size: 0.85rem;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    max-height: 32rem;
    overflow-y: auto;
}

h3 {
    margin-top: var(--space-5);
    font-size: 1rem;
}

.rules {
    margin: var(--space-2) 0 0;
    padding-left: var(--space-5);
}
</style>
