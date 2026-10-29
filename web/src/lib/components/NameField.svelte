<script lang="ts">
import { onMount } from 'svelte'
import { suggestName } from '../stores/names.svelte'

// A name field that starts with a suggestion in Jail Deck's style, jolly-otter
// (#148), and can draw another. It only suggests: the name is whatever is in
// the field when the form is sent, typed or drawn.
let { id, value = $bindable(''), placeholder = '' }: { id: string, value?: string, placeholder?: string } = $props()

let drawing = $state(false)

// first is the suggestion the field opens with, which must not land on a name
// someone has started typing while it was on its way.
async function draw(first = false) {
    drawing = true
    try {
        const suggested = await suggestName()
        if (!first || value === '') value = suggested
    } catch {
        // No suggestion is no harm: the field stays as it was, to be typed in.
    } finally {
        drawing = false
    }
}

onMount(() => {
    if (value === '') void draw(true)
})
</script>

<div class="name-field">
    <input {id} bind:value required autocomplete="off" spellcheck="false" {placeholder} />
    <button type="button" class="btn btn--small" onclick={() => draw()} disabled={drawing}
            title="Suggest another name" aria-label="Suggest another name">Another</button>
</div>

<style>
.name-field {
    display: flex;
    gap: var(--space-2);
    align-items: center;
}

.name-field input {
    flex: 1;
    min-width: 0;
}
</style>
