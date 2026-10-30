import { api, ApiError } from "../services/api";
import type { JailData, DatasetDestroy } from "./jails.svelte";
import { datasetDestroyCall } from "./jails.svelte";

// Template is a FreeBSD userland a jail is cloned from.
//
// Nothing here is stored by Jail Deck (JD-011): `ready` is whether the @base
// snapshot exists, which is what makes a template usable, and `parent` is read
// back from `zfs get origin`. A template that exists and is not ready is a
// derive that failed part way through.
// Running is the step executing right now. Absent when nothing is, and held in
// the server's memory rather than written down: a restart kills the work, so a
// marker that survived it would be claiming something false (JD-008).
export interface Running {
    step: string
    since: string
}

export interface Template {
    name: string
    dataset: string
    mountpoint: string
    ready: boolean
    parent?: string
    snapshot?: string
    // Where this template has got to. Read off the host's filesystem except
    // for the -ing forms, which come from what the server is doing. A template
    // being prepared has a state before it has a dataset.
    state: string
    running?: Running
    used: number
    referenced: number
}

// The states a template reports, in the order it passes through them.
export const TemplateStates = ['absent', 'fetching', 'fetched', 'extracting', 'extracted', 'updating', 'ready']

// prepareRelease starts a release template from the FreeBSD mirror. It answers
// at once with the state the work begins in; the template is then polled
// (JD-008). An empty release means the host's own, which is also the ceiling.
export async function prepareRelease(name: string, release: string): Promise<Template> {
    return await api<Template>(`/templates/${encodeURIComponent(name)}`, {
        method: 'PUT',
        body: JSON.stringify({ release }),
    })
}

// Dependents is what is holding a template in place: every jail cloned from it
// and every template derived from it, plus holds on its snapshots. Present and
// empty rather than null.
export interface Dependents {
    clones: string[]
    holds: string[]
}

export interface TemplateDetail extends Template {
    dependents: Dependents
}

export interface RetainedDataset {
    name: string
    short: string
    mountpoint: string
    used: number
    referenced: number
    // What destroying it would take with it, oldest first.
    snapshots: string[]
}

export async function listTemplates(): Promise<Template[]> {
    return await api<Template[]>('/templates')
}

export async function getTemplate(name: string): Promise<TemplateDetail> {
    return await api<TemplateDetail>(`/templates/${encodeURIComponent(name)}`)
}

export async function listRetained(): Promise<RetainedDataset[]> {
    return await api<RetainedDataset[]>('/retained')
}

// destroyRetained ends a retain (#120): the dataset and its snapshots. confirm
// is the short name, the one GET /retained answers and a restore asks for.
export async function destroyRetained(short: string): Promise<DatasetDestroy> {
    const query = new URLSearchParams({ confirm: short })
    return await datasetDestroyCall(`/retained/${encodeURIComponent(short)}?${query}`)
}

// lineage arranges templates parent-before-child with a depth for each, so a
// plain table can show the tree by indenting.
//
// A list rather than a tree widget on purpose: a host has a handful of
// templates two or three deep, and indentation carries that without the
// expand, collapse and selection state a widget brings with it.
export function lineage(templates: Template[]): { template: Template; depth: number }[] {
    const byParent = new Map<string, Template[]>()
    for (const t of templates) {
        const key = t.parent ?? ''
        byParent.set(key, [...(byParent.get(key) ?? []), t])
    }

    const known = new Set(templates.map(t => t.name))
    const out: { template: Template; depth: number }[] = []

    function walk(parent: string, depth: number) {
        const children = (byParent.get(parent) ?? []).sort((a, b) => a.name.localeCompare(b.name))
        for (const template of children) {
            out.push({ template, depth })
            walk(template.name, depth + 1)
        }
    }
    walk('', 0)

    // A template whose parent is not itself a template -- cloned from a
    // snapshot outside <pool>/jails/templates -- would otherwise never be
    // walked to and would vanish from the listing entirely.
    for (const template of templates) {
        if (template.parent && !known.has(template.parent) && !out.some(o => o.template.name === template.name)) {
            out.push({ template, depth: 0 })
        }
    }
    return out
}

// Dependent is one thing standing in a template's way, named as the host names
// it and linked to its own page.
export interface Dependent {
    dataset: string
    name: string
}

// classifyClones sorts a template's clones into the jails, the derived
// templates and whatever else.
//
// Deliberately not done by reading the dataset's name. <pool>/jails/containers
// is the layout Jail Deck creates (JD-006, JD-021), not where the jails it
// lists have to be -- those come from the host's own configuration. Jail Deck
// stores no layout (JD-011) and matches a jail to its datasets by mountpoint
// rather than by name (JD-022). So a clone is a jail only if some jail reports
// that dataset as its own, and a template only if some template does. Anything
// matching neither is shown as the bare dataset it is rather than guessed at
// (JD-028).
export function classifyClones(
    clones: string[],
    jails: JailData[],
    templates: Template[],
): { jails: Dependent[]; templates: Dependent[]; other: string[] } {
    const jailOf = new Map<string, string>()
    for (const jail of jails) {
        for (const dataset of jail.datasets ?? []) jailOf.set(dataset.name, jail.name)
    }
    const templateOf = new Map<string, string>()
    for (const template of templates) templateOf.set(template.dataset, template.name)

    const out = { jails: [] as Dependent[], templates: [] as Dependent[], other: [] as string[] }
    for (const dataset of clones) {
        const template = templateOf.get(dataset)
        if (template !== undefined) {
            out.templates.push({ dataset, name: template })
            continue
        }
        const jail = jailOf.get(dataset)
        if (jail !== undefined) {
            out.jails.push({ dataset, name: jail })
            continue
        }
        out.other.push(dataset)
    }
    return out
}

// Blocked names what is in a removal's way. Neither a clone nor a hold can be
// overridden here: taking a clone with its origin needs `zfs destroy -R`,
// which Jail Deck never passes, and a hold exists to say "not this one".
export interface Blocked {
    reason: string
    clones?: string[]
    holds?: string[]
}

// Removal is what destroying a template did. `blocked` means nothing ran.
export interface Removal {
    template: string
    destroyed: boolean
    blocked?: Blocked
}

export async function deriveTemplate(name: string, from: string, packages: string[]): Promise<Template> {
    return await api<Template>(`/templates/${encodeURIComponent(name)}`, {
        method: 'PUT',
        body: JSON.stringify({ from, packages }),
    })
}

// confirm is the template's name. Unlike a snapshot's it is unambiguous on its
// own, because a template name is unique within the pool (JD-027).
//
// A refusal arrives as 409 with the removal as its body and is returned rather
// than thrown: nothing ran, and what it names is the answer.
export async function removeTemplate(name: string): Promise<Removal> {
    const query = new URLSearchParams({ confirm: name })
    try {
        return await api<Removal>(`/templates/${encodeURIComponent(name)}?${query}`, { method: 'DELETE' })
    } catch (err) {
        if (err instanceof ApiError && err.statusCode === 409 && isRemoval(err.body)) return err.body
        throw err
    }
}

function isRemoval(body: unknown): body is Removal {
    return typeof body === 'object' && body !== null && 'template' in body && 'blocked' in body
}
