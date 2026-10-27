<script lang="ts">
import { onMount } from 'svelte'
import { createVM } from '../stores/vms.svelte'
import type { Created, CreateRequest } from '../stores/vms.svelte'
import { listImages } from '../stores/vmImages.svelte'
import type { Image } from '../stores/vmImages.svelte'
import { ApiError } from '../services/api'
import { formatBytes } from '../utils'

// The form that creates a guest from an image template (JD-038). Everything
// it could check, the server checks again; what it shows here is so that a
// refusal is rare, not so that the server can trust it.
let { switches, oncreated, oncancel }: {
    switches: string[]
    oncreated: (created: Created) => void
    oncancel: () => void
} = $props()

let images = $state<Image[] | null>(null)
let imagesError = $state('')

onMount(async () => {
    try {
        const listing = await listImages()
        // Only a ready image can be cloned from.
        images = listing.images.filter(i => i.ready && !i.running)
        image = images[0]?.name ?? ''
    } catch {
        imagesError = 'Could not read the images'
        images = []
    }
})

let name = $state('')
let image = $state('')
let cpu = $state(1)
let memory = $state('1G')
let disk = $state('')
// The first switch is only the starting choice; a list that changes later
// does not change it.
// svelte-ignore state_referenced_locally
let vmSwitch = $state(switches[0] ?? '')
let addressing = $state<'dhcp' | 'static'>('dhcp')
let address = $state('')
let gateway = $state('')
let nameservers = $state('')
let keys = $state('')
let start = $state(true)

let chosen = $derived(images?.find(i => i.name === image))
let busy = $state(false)
let error = $state('')

function split(text: string, by: RegExp): string[] {
    return text.split(by).map(s => s.trim()).filter(s => s !== '')
}

async function submit(event: Event) {
    event.preventDefault()
    if (busy) return
    busy = true
    error = ''
    const req: CreateRequest = {
        name: name.trim(),
        image,
        cpu,
        memory: memory.trim(),
        start,
        ssh_keys: split(keys, /\r?\n/),
    }
    if (disk.trim() !== '') req.disk = disk.trim()
    if (vmSwitch !== '') {
        req.switch = vmSwitch
        if (addressing === 'static') {
            req.network = { address: address.trim() }
            if (gateway.trim() !== '') req.network.gateway = gateway.trim()
            const servers = split(nameservers, /[\s,]+/)
            if (servers.length > 0) req.network.nameservers = servers
        }
    }
    try {
        oncreated(await createVM(req))
    } catch (err) {
        error = err instanceof ApiError ? err.statusText ?? 'Could not create it' : 'Could not reach Jail Deck'
    } finally {
        busy = false
    }
}
</script>

<form class="form" onsubmit={submit} aria-label="Create a virtual machine">
    {#if images === null}
        <p class="hint">Reading the images…</p>
    {:else if images.length === 0}
        <p class="notice notice--warn" role="status">
            {imagesError || 'There is no ready image to create a guest from. Prepare one under Images, below.'}
        </p>
        <div class="form__actions">
            <button type="button" class="btn btn--small" onclick={oncancel}>Close</button>
        </div>
    {:else}
        <div class="form__row">
            <div class="field">
                <label for="vm-name">Name</label>
                <input id="vm-name" bind:value={name} placeholder="web" autocomplete="off" spellcheck="false" required />
            </div>
            <div class="field">
                <label for="vm-image">Image</label>
                <select id="vm-image" bind:value={image}>
                    {#each images as i (i.name)}
                        <option value={i.name}>{i.name}</option>
                    {/each}
                </select>
            </div>
            <div class="field field--narrow">
                <label for="vm-cpu">CPUs</label>
                <input id="vm-cpu" type="number" min="1" max="64" bind:value={cpu} required />
            </div>
            <div class="field field--narrow">
                <label for="vm-memory">Memory</label>
                <input id="vm-memory" bind:value={memory} placeholder="1G" autocomplete="off" spellcheck="false" required />
            </div>
            <div class="field field--narrow">
                <label for="vm-disk">Disk</label>
                <input id="vm-disk" bind:value={disk} placeholder={chosen?.size ? formatBytes(chosen.size) : ''}
                       autocomplete="off" spellcheck="false" />
            </div>
        </div>
        <p class="hint">
            Memory as vm-bhyve writes it, <code>512M</code> or <code>2G</code>. The disk is a clone of the
            image{#if chosen?.size}, at least its {formatBytes(chosen.size)} and that by default{/if}; the guest
            grows its own filesystem into a larger one on first boot.
        </p>

        <div class="form__row">
            <div class="field">
                <label for="vm-switch">Network</label>
                <select id="vm-switch" bind:value={vmSwitch}>
                    {#each switches as s (s)}
                        <option value={s}>on switch {s}</option>
                    {/each}
                    <option value="">no network</option>
                </select>
            </div>
            {#if vmSwitch !== ''}
                <fieldset class="field">
                    <legend>Address</legend>
                    <div class="radios">
                        <label><input type="radio" bind:group={addressing} value="dhcp" /> DHCP</label>
                        <label><input type="radio" bind:group={addressing} value="static" /> Static</label>
                    </div>
                </fieldset>
            {/if}
        </div>
        {#if switches.length === 0}
            <p class="hint">
                vm-bhyve has no switches, so a guest made now has no network. Switches are the
                operator's, made with <code>vm switch create</code>.
            </p>
        {/if}
        {#if vmSwitch !== '' && addressing === 'static'}
            <div class="form__row">
                <div class="field">
                    <label for="vm-address">Address</label>
                    <input id="vm-address" bind:value={address} placeholder="10.0.0.5/24"
                           autocomplete="off" spellcheck="false" required />
                </div>
                <div class="field">
                    <label for="vm-gateway">Gateway</label>
                    <input id="vm-gateway" bind:value={gateway} placeholder="10.0.0.1" autocomplete="off" spellcheck="false" />
                </div>
                <div class="field field--grow">
                    <label for="vm-nameservers">Nameservers</label>
                    <input id="vm-nameservers" bind:value={nameservers} placeholder="10.0.0.1"
                           autocomplete="off" spellcheck="false" />
                </div>
            </div>
        {/if}

        <div class="field">
            <label for="vm-keys">SSH keys</label>
            <textarea id="vm-keys" rows="3" bind:value={keys} spellcheck="false"
                      placeholder="ssh-ed25519 AAAA… you@laptop"></textarea>
        </div>
        <p class="hint">
            One per line, for the image's default user (<code>debian</code> on Debian). A cloud image has
            no password: <strong>without a key the guest is reachable only on its console.</strong>
        </p>

        <label class="check"><input type="checkbox" bind:checked={start} /> Start it once it is made</label>

        {#if error}
            <p class="notice notice--error" role="alert">{error}</p>
        {/if}
        <div class="form__actions">
            <button type="button" class="btn btn--small" onclick={oncancel}>Cancel</button>
            <button type="submit" class="btn btn--small btn--primary" disabled={busy}>
                {busy ? 'Creating…' : 'Create'}
            </button>
        </div>
    {/if}
</form>

<style>
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
    min-width: 0;
}

.field--narrow input {
    width: 6rem;
}

.field--grow {
    flex: 1 1 12rem;
}

fieldset.field {
    border: 0;
    margin: 0;
    padding: 0;
}

/* A legend names its radios the way a label names its input. */
legend {
    padding: 0;
    margin-bottom: var(--space-2);
}

.radios {
    display: flex;
    gap: var(--space-4);
    align-items: center;
    min-height: 2.25rem;
}

.radios label,
.check {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    font-weight: 400;
    text-transform: none;
    letter-spacing: 0;
}

textarea {
    width: 100%;
    font-family: var(--font-mono, monospace);
    font-size: 0.8rem;
}

.hint {
    margin: 0;
    font-size: 0.85rem;
    color: var(--text-muted);
}

.notice--warn {
    color: var(--warn);
    background: var(--warn-soft);
    border-color: color-mix(in srgb, var(--warn) 35%, transparent);
}
</style>
