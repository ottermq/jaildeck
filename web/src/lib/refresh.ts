// Refreshing a template (#252): whether one may be, and what to call the new
// one. The server decides; this only says so before the operator asks.

import type { Template } from './stores/templates.svelte'

// refreshRefusal is why a template cannot be refreshed, or '' when it can.
// The same rule as the server's: a refresh runs freebsd-update and pkg as
// root on the tree, so only a tree Jail Deck built itself qualifies.
export function refreshRefusal(t: Template): string {
    if (t.running) return 'Something is already working on this template.'
    if (!t.ready) return 'It has no @base snapshot to refresh from.'
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
// date, so the new template sorts next to its parent and says when.
export function suggestedName(t: Template, today: Date = new Date()): string {
    const day = today.toISOString().slice(0, 10).replaceAll('-', '')
    const base = t.name.replace(/-\d{8}$/, '')
    return `${base}-${day}`
}
