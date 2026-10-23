<script lang="ts">
import { current, answer } from '../stores/confirm.svelte'

// Mounted once, in the shell. Everything it shows comes from ask().
let dialog = $state<HTMLDialogElement | null>(null)
let typed = $state('')

let confirmation = $derived(current())

// Nothing is typed yet when a dialog that wants typing opens, so the button
// starts disabled and the Enter key cannot run it by accident.
let ready = $derived(!confirmation?.typed || typed === confirmation.typed)

$effect(() => {
    if (!dialog) return
    if (confirmation) {
        typed = ''
        if (!dialog.open) dialog.showModal()
    } else if (dialog.open) {
        dialog.close()
    }
})

// Escape closes a <dialog> on its own, which fires close without going through
// either button. Treated as a no, like every other way out that is not the
// verb.
function onClose() {
    if (current()) answer(false)
}

function submit(event: Event) {
    event.preventDefault()
    if (ready) answer(true)
}
</script>

<dialog bind:this={dialog} onclose={onClose} class="confirm">
    {#if confirmation}
        <form onsubmit={submit}>
            <h2>{confirmation.title}</h2>

            {#each confirmation.lines ?? [] as line (line)}
                <p class="line">{line}</p>
            {/each}

            {#if confirmation.items && confirmation.items.length > 0}
                <ul class="items">
                    {#each confirmation.items as item (item)}
                        <li><code>{item}</code></li>
                    {/each}
                </ul>
            {/if}

            {#if confirmation.typed}
                <!-- No autofocus: showModal focuses the first focusable thing in
                     the dialog, and this field is it. Without one, that is Cancel,
                     which is the right place for the caret to land anyway. -->
                <div class="field">
                    <label for="confirm-typed">Type <code>{confirmation.typed}</code> to confirm</label>
                    <input
                        id="confirm-typed"
                        type="text"
                        autocomplete="off"
                        spellcheck="false"
                        bind:value={typed}
                    />
                </div>
            {/if}

            <div class="actions">
                <button type="button" class="btn btn--small" onclick={() => answer(false)}>Cancel</button>
                <button type="submit" class="btn btn--small btn--danger" disabled={!ready}>
                    {confirmation.verb}
                </button>
            </div>
        </form>
    {/if}
</dialog>

<style>
.confirm {
    width: min(32rem, calc(100vw - 2 * var(--space-4)));
    padding: var(--space-5);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
}

.confirm::backdrop {
    background: rgba(0, 0, 0, 0.45);
}

form {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
}

.line {
    font-size: 0.925rem;
}

.items {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    max-height: 12rem;
    overflow-y: auto;
}

.field {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
}

.field input {
    width: 100%;
    font-family: var(--mono);
}

.actions {
    display: flex;
    gap: var(--space-2);
    justify-content: flex-end;
}
</style>
