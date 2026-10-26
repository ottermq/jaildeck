<script lang="ts">
import { onMount } from 'svelte'
import { navigate, onNavClick } from '../router.svelte'
import { listTemplates } from '../stores/templates.svelte'
import type { Template } from '../stores/templates.svelte'
import { createJail, partialCreate } from '../stores/jails.svelte'
import type { Created, JailSpec, NetworkMode } from '../stores/jails.svelte'
import { buildSpec, limitFields, numericLimits, parameterFields, datasetProperties } from '../jailSpec'
import type { ChildRow, TagRow } from '../jailSpec'
import { ApiError } from '../services/api'

let templates = $state<Template[] | null>(null)
let loadError = $state('')

let name = $state('')
let template = $state('')
let hostname = $state('')

let mode = $state<NetworkMode>('disable')
let ip4 = $state('')
let iface = $state('')
let bridge = $state('')
let isPrivate = $state(false)
let gateway = $state('')

let autostart = $state(false)
let start = $state(true)

// The three sections a jail usually does not need. Hidden until asked for,
// the way a form opens where the thing it acts on is: a create that needs
// none of them should not read as one that left them blank.
let showLimits = $state(false)
let showParams = $state(false)
let showStorage = $state(false)

let limits = $state<Record<string, string>>({})
// A number input binds a number and an emptied one binds null, so this holds
// what the control gives rather than what the API wants; typed() does the
// one conversion, where the payload is built.
let paramValues = $state<Record<string, string | number | null>>({})

// Rows are keyed by an id of their own rather than by index (see ChildRow).
let nextRow = 0

let children = $state<ChildRow[]>([])
let tags = $state<TagRow[]>([])

let busy = $state(false)
let created = $state<Created | null>(null)
let failed = $state<Created | null>(null)
let submitError = $state('')

// Only a template with its @base snapshot can be cloned (JD-007). One still
// being prepared is listed as unusable rather than hidden, so a half-built
// template explains why it is not on offer.
let ready = $derived((templates ?? []).filter(t => t.ready && t.snapshot))
let unready = $derived((templates ?? []).filter(t => !t.ready || !t.snapshot))

async function load() {
    loadError = ''
    try {
        templates = await listTemplates()
        if (!template && ready.length === 1) template = ready[0].snapshot!
    } catch (err) {
        loadError = err instanceof ApiError
            ? err.statusText ?? 'Could not load templates'
            : 'Could not reach Jail Deck'
    }
}

onMount(() => {
    load()
})

function addChild() {
    children = [...children, { id: nextRow++, name: '', properties: {} }]
}

function removeChild(id: number) {
    children = children.filter(row => row.id !== id)
}

function addTag() {
    tags = [...tags, { id: nextRow++, name: '', value: '' }]
}

function removeTag(id: number) {
    tags = tags.filter(row => row.id !== id)
}

function spec(): JailSpec {
    return buildSpec({
        name, template, hostname, mode, ip4, iface, bridge, isPrivate, gateway,
        autostart, start, limits, paramValues, children, tags,
    })
}

async function submit(event: SubmitEvent) {
    event.preventDefault()
    if (busy) return

    busy = true
    submitError = ''
    created = null
    failed = null
    try {
        created = await createJail(spec())
    } catch (err) {
        failed = partialCreate(err)
        submitError = err instanceof ApiError
            ? err.statusText ?? 'Could not create the jail'
            : 'Could not reach Jail Deck'
    } finally {
        busy = false
    }
}

// A step of the create, as the report shows it happened. The order is the
// order the server runs them in (JD-030), so a failure reads as "this far,
// and no further".
interface Step {
    label: string
    detail: string
    state: 'done' | 'failed' | 'skipped'
}

function steps(result: Created, stopped: string | undefined): Step[] {
    const order = ['clone', 'datasets', 'configure', 'autostart', 'start']
    const reached = (step: string) =>
        stopped === undefined ? true : order.indexOf(step) < order.indexOf(stopped)

    const at = (step: string, done: boolean, detail: string): Step => ({
        label: step,
        detail,
        state: stopped === step ? 'failed' : done && reached(step) ? 'done' : 'skipped',
    })

    const made = (result.datasets ?? []).map(d => d.dataset).join(', ')
    const tagged = Object.entries(result.properties ?? {}).map(([k, v]) => `${k}=${v}`).join(', ')
    const storage = [made, tagged].filter(Boolean).join(' · ')

    const list: Step[] = [
        at('clone', !!result.root, result.root ? `${result.from} → ${result.root}` : 'nothing was cloned'),
    ]
    if (storage || stopped === 'datasets') {
        list.push(at('datasets', !!storage, storage || 'nothing was made'))
    }
    list.push(at('configure', !!result.config, result.config || 'no file was written'))
    if (result.autostart || stopped === 'autostart') {
        list.push(at('autostart', result.autostart, 'added to rc.conf’s jail_list'))
    }
    list.push(at('start', result.running, result.running ? 'running' : 'not running'))
    return list
}

let report = $derived(created ?? failed)
let stoppedAt = $derived(failed?.failed_at)
</script>

<header class="page-head">
    <h1>Create a jail</h1>
    <p class="page-sub">
        Cloned from a ready template, configured, and started — one request, and
        nothing is undone if a step fails.
    </p>
</header>

{#if loadError}
    <p class="notice notice--error" role="alert">
        {loadError}
        <button type="button" class="btn btn--small" onclick={load}>Retry</button>
    </p>
{/if}

{#if report}
    <section class="panel">
        <div class="panel__head">
            <h2>
                {#if created}Created {created.jail.name || name}{:else}Stopped at {stoppedAt}{/if}
            </h2>
            {#if created}
                {@const to = `/jails/${encodeURIComponent(created.jail.name)}`}
                <a class="btn btn--small btn--primary" href={to} onclick={(e) => onNavClick(e, to)}>
                    Open {created.jail.name}
                </a>
            {/if}
        </div>

        {#if submitError}
            <p class="notice notice--error" role="alert">{submitError}</p>
        {/if}

        <ol class="steps">
            {#each steps(report, stoppedAt) as step (step.label)}
                <li class={`step step--${step.state}`}>
                    <span class="step__mark" aria-hidden="true">
                        {step.state === 'done' ? '✓' : step.state === 'failed' ? '✕' : '·'}
                    </span>
                    <span class="step__label">{step.label}</span>
                    <span class="step__detail">{step.detail}</span>
                </li>
            {/each}
        </ol>

        {#if report.epair}
            <p class="hint">
                Its epair is <code>{report.epair}</code>{#if report.epair_removed}, which was destroyed
                again when the start could not be confirmed{/if}.
            </p>
        {/if}
        {#if report.rules_removed?.length}
            <p class="hint">
                The rctl rules its start had already applied were taken back:
                {report.rules_removed.join(', ')}.
            </p>
        {/if}
        {#if report.rules_error}
            <p class="notice notice--error">{report.rules_error}</p>
        {/if}

        {#if failed}
            <p class="hint">
                Nothing was undone. What exists is listed above — destroy the jail to
                clear it, or fix the spec and create it again under another name.
            </p>
        {/if}
    </section>
{/if}

{#if !created}
    <form class="form form--page" onsubmit={submit}>
        <section class="group" aria-labelledby="group-source">
            <h2 class="group__title" id="group-source">Source</h2>
            <div class="form__row">
                <div class="field field--grow">
                    <label for="template">Template</label>
                    <select id="template" bind:value={template} required>
                        <option value="" disabled>Choose a ready template…</option>
                        {#each ready as t (t.name)}
                            <option value={t.snapshot}>{t.name}</option>
                        {/each}
                    </select>
                </div>
                <div class="field field--grow">
                    <label for="name">Name</label>
                    <input
                        id="name" bind:value={name} required autocomplete="off" spellcheck="false"
                        placeholder="db1"
                    />
                </div>
                <div class="field field--grow">
                    <label for="hostname">Hostname</label>
                    <input
                        id="hostname" bind:value={hostname} autocomplete="off" spellcheck="false"
                        placeholder={name || 'the jail’s name'}
                    />
                </div>
            </div>
            {#if templates !== null && ready.length === 0}
                <p class="notice">
                    No template is ready. A template can be cloned once it has its
                    <code>@base</code> snapshot —
                    <a href="/templates" onclick={(e) => onNavClick(e, '/templates')}>prepare one</a> first.
                </p>
            {:else if unready.length > 0}
                <p class="hint">
                    Not offered: {unready.map(t => `${t.name} (${t.state})`).join(', ')} — a
                    template is cloneable only once it has its <code>@base</code> snapshot.
                </p>
            {/if}
            <p class="hint">
                A name may hold letters, digits, <code>-</code> and <code>_</code>. Not a dot:
                jail(8) reads a dotted name as a hierarchical jail, and rc.d/jail maps it to
                an underscore, so two such names collide there.
            </p>
        </section>

        <section class="group" aria-labelledby="group-network">
            <h2 class="group__title" id="group-network">Network</h2>
            <div class="form__row">
                <div class="field field--grow">
                    <label for="mode">Mode</label>
                    <select id="mode" bind:value={mode}>
                        <option value="disable">disable — no network</option>
                        <option value="inherit">inherit — the host’s addresses and its stack</option>
                        <option value="alias">alias — one address on a host interface</option>
                        <option value="vnet">vnet — its own stack on a bridge</option>
                    </select>
                </div>
                {#if mode === 'alias'}
                    <div class="field field--grow">
                        <label for="ip4">Address</label>
                        <input id="ip4" bind:value={ip4} required autocomplete="off"
                               spellcheck="false" placeholder="192.0.2.10/24" />
                    </div>
                    <div class="field field--grow">
                        <label for="iface">Interface</label>
                        <input id="iface" bind:value={iface} required autocomplete="off"
                               spellcheck="false" placeholder="em0" />
                    </div>
                {:else if mode === 'vnet'}
                    <div class="field field--grow">
                        <label for="bridge">Bridge</label>
                        <input id="bridge" bind:value={bridge} required autocomplete="off"
                               spellcheck="false" placeholder="bridge0" />
                    </div>
                    <div class="field field--grow">
                        <label for="vnet-ip4">Address</label>
                        <input id="vnet-ip4" bind:value={ip4} autocomplete="off"
                               spellcheck="false" placeholder="10.77.0.23/24 — or DHCP inside" />
                    </div>
                    <div class="field field--grow">
                        <label for="gateway">Gateway</label>
                        <input id="gateway" bind:value={gateway} autocomplete="off"
                               spellcheck="false" placeholder="10.77.0.1" />
                    </div>
                {/if}
            </div>
            {#if mode === 'inherit'}
                <p class="hint">
                    The jail shares the host’s network stack, <strong>127.0.0.1 included</strong> —
                    anything listening on the host’s loopback is reachable from inside it.
                </p>
            {:else if mode === 'vnet'}
                <label class="check">
                    <input type="checkbox" bind:checked={isPrivate} />
                    Private member of the bridge
                </label>
                <p class="hint">
                    The bridge is yours: Jail Deck adds and removes its own epairs and
                    nothing else, and never creates one. <strong>Private</strong> stops this jail
                    reaching the <em>other private members</em> — measured on 15.1-RELEASE-p5, a
                    private member still reaches non-private ones and the bridge itself.
                </p>
            {/if}
        </section>

        <section class="group" aria-labelledby="group-after">
            <h2 class="group__title" id="group-after">After it is built</h2>
            <label class="check">
                <input type="checkbox" bind:checked={start} />
                Start it once it is configured
            </label>
            <label class="check">
                <input type="checkbox" bind:checked={autostart} />
                Bring it back after a reboot — adds it to rc.conf’s <code>jail_list</code>
            </label>
        </section>

        <section class="group" aria-labelledby="group-limits">
            <div class="group__head">
                <h2 class="group__title" id="group-limits">Limits</h2>
                <button type="button" class="btn btn--small" onclick={() => (showLimits = !showLimits)}>
                    {showLimits ? 'Hide' : 'Set limits'}
                </button>
            </div>
            {#if showLimits}
                <div class="form__row">
                    {#each limitFields as field (field.name)}
                        <div class="field">
                            <label for={`limit-${field.name}`}>{field.label}</label>
                            <input
                                id={`limit-${field.name}`}
                                bind:value={limits[field.name]}
                                autocomplete="off" spellcheck="false"
                                inputmode={numericLimits.includes(field.name) ? 'numeric' : undefined}
                                placeholder="none" title={field.hint}
                            />
                        </div>
                    {/each}
                </div>
                <p class="hint">
                    Applied by the jail’s own <code>exec.poststart</code> and removed by its
                    <code>exec.prestop</code>, so a jail started from a shell is limited the same
                    way — and a jail whose limits cannot be applied does not start at all.
                    All but <code>cpuset</code> need <code>kern.racct.enable=1</code>, which the
                    <a href="/host" onclick={(e) => onNavClick(e, '/host')}>host report</a> answers.
                </p>
            {/if}
        </section>

        <section class="group" aria-labelledby="group-params">
            <div class="group__head">
                <h2 class="group__title" id="group-params">Jail parameters</h2>
                <button type="button" class="btn btn--small" onclick={() => (showParams = !showParams)}>
                    {showParams ? 'Hide' : 'Set parameters'}
                </button>
            </div>
            {#if showParams}
                <div class="form__row">
                    {#each parameterFields as field (field.name)}
                        <div class="field">
                            <label for={`param-${field.name}`}>{field.name}</label>
                            {#if field.kind === 'number'}
                                <input
                                    id={`param-${field.name}`} type="number" step="1"
                                    bind:value={paramValues[field.name]}
                                    placeholder="unset" title={field.hint}
                                />
                            {:else}
                                <select id={`param-${field.name}`} bind:value={paramValues[field.name]} title={field.hint}>
                                    <option value="">unset</option>
                                    {#each field.kind === 'flag' ? ['true', 'false'] : field.choices ?? [] as choice (choice)}
                                        <option value={choice}>{choice}</option>
                                    {/each}
                                </select>
                            {/if}
                        </div>
                    {/each}
                </div>
                <p class="hint">
                    These are the parameters Jail Deck will set — an allowlist, because a
                    denylist fails open the day jail(8) gains a parameter. Anything else is
                    refused with the parameter named.
                </p>
            {/if}
        </section>

        <section class="group" aria-labelledby="group-storage">
            <div class="group__head">
                <h2 class="group__title" id="group-storage">Datasets and tags</h2>
                <button type="button" class="btn btn--small" onclick={() => (showStorage = !showStorage)}>
                    {showStorage ? 'Hide' : 'Add storage'}
                </button>
            </div>
            {#if showStorage}
                {#each children as child (child.id)}
                    <div class="form__row row--child">
                        <div class="field">
                            <label for={`child-${child.id}`}>Dataset</label>
                            <input id={`child-${child.id}`} bind:value={child.name}
                                   autocomplete="off" spellcheck="false" placeholder="data" />
                        </div>
                        {#each datasetProperties as property (property.name)}
                            <div class="field">
                                <label for={`child-${child.id}-${property.name}`}>{property.name}</label>
                                <input
                                    id={`child-${child.id}-${property.name}`}
                                    bind:value={child.properties[property.name]}
                                    autocomplete="off" spellcheck="false"
                                    placeholder="inherit" title={property.hint}
                                />
                            </div>
                        {/each}
                        <button type="button" class="btn btn--small" onclick={() => removeChild(child.id)}>
                            Remove
                        </button>
                    </div>
                {/each}
                <div class="form__actions form__actions--left">
                    <button type="button" class="btn btn--small" onclick={addChild}>Add a dataset</button>
                    <button type="button" class="btn btn--small" onclick={addTag}>Add a tag</button>
                </div>

                {#each tags as tag (tag.id)}
                    <div class="form__row">
                        <div class="field">
                            <label for={`tag-${tag.id}`}>Tag</label>
                            <input id={`tag-${tag.id}`} bind:value={tag.name}
                                   autocomplete="off" spellcheck="false" placeholder="otter:instance" />
                        </div>
                        <div class="field field--grow">
                            <label for={`tag-${tag.id}-value`}>Value</label>
                            <input id={`tag-${tag.id}-value`} bind:value={tag.value}
                                   autocomplete="off" spellcheck="false" />
                        </div>
                        <button type="button" class="btn btn--small" onclick={() => removeTag(tag.id)}>
                            Remove
                        </button>
                    </div>
                {/each}

                <p class="hint">
                    A dataset is made one level beneath the jail’s own, before the jail
                    starts. A quota belongs on a child rather than on the jail’s root,
                    because a clone’s <code>referenced</code> space includes the template’s
                    blocks. A tag is a ZFS user property on the jail’s own dataset — the name
                    needs a colon, and Jail Deck never reads what it says.
                </p>
            {/if}
        </section>

        {#if submitError && !report}
            <p class="notice notice--error" role="alert">{submitError}</p>
        {/if}

        <div class="form__actions">
            <a class="btn btn--small" href="/jails" onclick={(e) => onNavClick(e, '/jails')}>Cancel</a>
            <button type="submit" class="btn btn--small btn--primary" disabled={busy || ready.length === 0}>
                {busy ? 'Creating…' : 'Create jail'}
            </button>
        </div>
    </form>
{:else}
    <div class="form__actions form__actions--left">
        <button type="button" class="btn btn--small" onclick={() => navigate('/jails')}>Back to jails</button>
    </div>
{/if}

<style>
/* The page's own form: no sunken panel, because this page *is* the form.
   Spacing is on the sections rather than a gap here, so the rule between two
   of them sits the same distance from each. */
.form--page {
    display: flex;
    flex-direction: column;
    margin-top: var(--space-5);
}

/* A section rather than a fieldset: a legend is rendered inside the
   fieldset's top border, where it cuts through the rule and crowds whatever
   sits above it. The grouping is kept by aria-labelledby. */
.group {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    margin: 0;
}

.group + .group {
    border-top: 1px solid var(--border);
    margin-top: var(--space-5);
    padding-top: var(--space-5);
}

.group__title {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    margin: 0;
}

/* A section that is usually left alone carries its own reveal, so the form
   reads as what this jail needs rather than as what was skipped. */
.group__head {
    display: flex;
    align-items: center;
    gap: var(--space-3);
}

.form__row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
    align-items: flex-end;
}

.row--child {
    padding-bottom: var(--space-3);
    border-bottom: 1px dashed var(--border);
}

.field {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
}

.field--grow {
    flex: 1 1 14rem;
}

.form__actions {
    display: flex;
    gap: var(--space-2);
    justify-content: flex-end;
    align-items: center;
    margin-top: var(--space-5);
    padding-top: var(--space-5);
    border-top: 1px solid var(--border);
}

/* The in-section ones add rows rather than submit, so they carry neither the
   rule nor the margin. */
.group .form__actions {
    margin-top: 0;
    padding-top: 0;
    border-top: 0;
}

.form__actions--left {
    justify-content: flex-start;
}

.check {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    text-transform: none;
    letter-spacing: normal;
    font-weight: 400;
    font-size: 0.9rem;
    color: var(--text);
}

.hint {
    font-size: 0.85rem;
    color: var(--text-muted);
    max-width: 68ch;
}

/* In the report the hints follow the steps rather than a heading, so they
   need the gap the form's own layout supplies elsewhere. */
.panel .hint,
.panel .notice {
    margin-top: var(--space-3);
}

.panel {
    margin-top: var(--space-5);
    padding: var(--space-4);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
}

.panel__head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-4);
    margin-bottom: var(--space-3);
}

.panel__head h2 {
    margin: 0;
}

/* The steps are the create's own report, in the order the server runs them,
   so a failure reads as "this far and no further" rather than as one line. */
.steps {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
}

.step {
    display: grid;
    grid-template-columns: 1.2rem 7rem 1fr;
    gap: var(--space-3);
    align-items: baseline;
    font-size: 0.9rem;
}

.step__mark {
    text-align: center;
    font-weight: 700;
}

.step__label {
    font-weight: 600;
    color: var(--text-strong);
}

.step__detail {
    color: var(--text-muted);
    font-family: var(--mono);
    font-size: 0.8rem;
    word-break: break-all;
}

.step--done .step__mark {
    color: var(--ok);
}

.step--failed .step__mark,
.step--failed .step__label {
    color: var(--danger);
}

.step--skipped {
    opacity: 0.55;
}
</style>
