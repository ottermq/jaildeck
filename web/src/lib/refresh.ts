// Refreshing a template (#252): whether one may be, and what to call the new
// one. The server decides; this only says so before the operator asks.

import type { Template } from './stores/templates.svelte'

// refreshRefusal is why a template cannot be refreshed, or '' when it can.
// The same rule as the server's: a refresh runs freebsd-update and pkg as
// root on the tree, so only a tree Jail Deck built itself qualifies.
export function refreshRefusal(t: Template): string {
    if (t.running) return 'Something is already working on this template.'
    if (!t.ready) return 'It has no @base snapshot to refresh from.'
    if (t.superseded_by) return supersededRefusal(t)
    switch (t.built) {
        case 'release':
        case 'derived':
        case 'refreshed':
            return ''
        case 'jail':
            return 'It was made from a jail, so its files are whatever that jail\'s root left in them. '
                + 'A refresh runs freebsd-update and pkg on them as root, and is refused.'
        default:
            return `It doesn't say how it was made. If it was prepared from a release or derived, `
                + `mark it once on the host: zfs set jaildeck:built=derived ${t.dataset}`
    }
}

// supersededRefusal is why a superseded template is not offered as a source
// (#259), or '' when it is not superseded.
export function supersededRefusal(t: Template): string {
    if (!t.superseded_by) return ''
    return `It has been superseded by ${t.superseded_by}; use that one instead. `
        + `To use this one anyway, clear the mark on the host: zfs inherit jaildeck:superseded-by ${t.dataset}`
}

// builtLabel says how a template was made, for the page's header.
export function builtLabel(t: Template): string {
    switch (t.built) {
        case 'release': return 'prepared from a release'
        case 'derived': return 'derived'
        case 'refreshed': return 'refreshed'
        case 'jail': return 'made from a jail'
        default: return ''
    }
}

// suggestedName is the default name for a refresh of t: its name and today's
// date, so the new template sorts next to its parent and says when. A name
// in taken -- a template that exists, or one removed before (#268) -- gets
// -2, -3 and so on instead.
export function suggestedName(t: Template, today: Date = new Date(), taken: string[] = []): string {
    const day = today.toISOString().slice(0, 10).replaceAll('-', '')
    const base = t.name.replace(/-\d{8}(-\d+)?$/, '')
    const first = `${base}-${day}`
    if (!taken.includes(first)) return first
    for (let n = 2; ; n++) {
        const next = `${first}-${n}`
        if (!taken.includes(next)) return next
    }
}

// reusedNameNote warns that name was a removed template's (#268), or is ''.
export function reusedNameNote(name: string, removed: string[]): string {
    if (!removed.includes(name.trim())) return ''
    return `A template called ${name.trim()} was removed before. If a backup tool such as zrepl `
        + `replicated it, the backup still holds that copy, which has no snapshot in common with `
        + `the new one: replication of this name stops until the old copy is destroyed on the backup side.`
}
