<script lang="ts">
import type { Snippet } from 'svelte'
import { ask } from '../stores/confirm.svelte'
import { ApiError } from '../services/api'

// A jail's or a guest's ZFS user properties: the labels an automation client
// sets and finds its instances by again (JD-023, JD-044). The page owns the
// instance; this owns the edits, and hands every write back through changed
// so the page refetches rather than trusting what it asked for.
let {
    name,
    noun,
    properties,
    save,
    changed,
    canAdd = true,
    empty,
    hint,
}: {
    name: string
    noun: 'jail' | 'guest'
    properties: Record<string, string> | undefined
    // Null removes a property; a string sets it, "" included.
    save: (tags: Record<string, string | null>) => Promise<unknown>
    changed: () => Promise<void>
    canAdd?: boolean
    empty: Snippet
    hint?: Snippet
} = $props()

let tags = $derived(Object.entries(properties ?? {}).sort(([a], [b]) => a.localeCompare(b)))

let busy = $state('')
let actionError = $state<Record<string, string>>({})

async function run(key: string, tags: Record<string, string | null>): Promise<boolean> {
    if (busy) return false
    busy = key
    delete actionError[key]
    try {
        await save(tags)
        return true
    } catch (err) {
        actionError[key] = err instanceof ApiError
            ? err.statusText ?? 'That did not work'
            : 'Could not reach Jail Deck'
        return false
    } finally {
        busy = ''
        await changed()
    }
}

let adding = $state(false)
let newName = $state('')
let newValue = $state('')
let editing = $state('')
let editedValue = $state('')

function startAdd() {
    adding = true
    newName = ''
    newValue = ''
    delete actionError['tag-add']
}

async function add(event: Event) {
    event.preventDefault()
    if (await run('tag-add', { [newName.trim()]: newValue })) {
        adding = false
    }
}

function startEdit(property: string, value: string) {
    editing = property
    editedValue = value
    delete actionError['tag-edit']
}

async function saveEdit(event: Event) {
    event.preventDefault()
    if (await run('tag-edit', { [editing]: editedValue })) {
        editing = ''
    }
}

async function remove(property: string) {
    const confirmed = await ask({
        title: `Remove ${property} from ${name}?`,
        lines: [
            `The property is inherited away rather than set empty, so the ${noun} stops carrying it at all.`,
            `An automation client that finds its ${noun}s by this tag will stop finding this one.`,
        ],
        verb: 'Remove',
    })
    if (!confirmed) return
    // Null, not "": removing a property and setting it to the empty string are
    // different states, and the API draws that line.
    await run('tag-remove', { [property]: null })
}
</script>

<section class="panel">
    <div class="panel__head">
        <h2>Tags</h2>
        {#if canAdd}
            <button type="button" class="btn btn--small" onclick={startAdd} disabled={adding}>
                Add a tag
            </button>
        {/if}
    </div>

    {#if adding}
        <form class="form" onsubmit={add}>
            <div class="form__row">
                <div class="field">
                    <label for="tag-name">Property</label>
                    <input id="tag-name" bind:value={newName} placeholder="otter:instance"
                           autocomplete="off" spellcheck="false" required />
                </div>
                <div class="field field--grow">
                    <label for="tag-value">Value</label>
                    <input id="tag-value" bind:value={newValue} autocomplete="off" spellcheck="false" />
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
                <button type="button" class="btn btn--small" onclick={() => (adding = false)}>Cancel</button>
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
        <p class="notice">{@render empty()}</p>
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
                                {#if editing === property}
                                    <form class="inline-edit" onsubmit={saveEdit}>
                                        <input bind:value={editedValue} autocomplete="off" spellcheck="false"
                                               aria-label={`Value for ${property}`} />
                                        <button type="submit" class="btn btn--small btn--primary" disabled={busy === 'tag-edit'}>
                                            {busy === 'tag-edit' ? 'Saving…' : 'Save'}
                                        </button>
                                        <button type="button" class="btn btn--small" onclick={() => (editing = '')}>Cancel</button>
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
                                        disabled={!!busy || editing === property}
                                        onclick={() => startEdit(property, value)}
                                    >Edit</button>
                                    <button
                                        type="button"
                                        class="btn btn--small btn--danger"
                                        disabled={!!busy}
                                        onclick={() => remove(property)}
                                    >Remove</button>
                                </div>
                            </td>
                        </tr>
                        {#if editing === property && actionError['tag-edit']}
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
        {#if hint}
            <p class="hint">{@render hint()}</p>
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
    margin-bottom: var(--space-3);
}

.panel__head h2 {
    margin-bottom: 0;
}

.name {
    font-weight: 600;
    color: var(--text-strong);
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
