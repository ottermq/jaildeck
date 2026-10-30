import type { JailData } from './stores/jails.svelte'
import type { JailUsage } from './stores/metrics.svelte'
import type { VM } from './stores/vms.svelte'

// One row of the instances view (#129): a jail or a guest, read from its own
// listing and nothing stored beside it. The two kinds keep their own words
// for state and their own idea of CPU and memory -- a jail's is what it is
// using, a guest's what it was given -- and the page says which is which.
export type Kind = 'jail' | 'vm'

export interface Instance {
    kind: Kind
    name: string
    // A jail and a guest may share a name; the key never does.
    key: string
    href: string
    // The kind's own word: running or stopped for a jail, vm-bhyve's five for
    // a guest.
    state: string
    // ZFS user properties on its dataset (JD-023, JD-044).
    labels: Record<string, string>
    // A running jail's CPU in percent of one core and memory in use, read
    // from the metrics report; absent for a stopped one.
    usage?: { pcpu: number, memory: number }
    // A guest's configuration as vm-bhyve writes it: "2", "512M".
    vcpus?: string
    memory?: string
    // Who holds a locked guest's run.lock.
    lockedBy?: string
}

export function fromJail(j: JailData, usage?: JailUsage): Instance {
    return {
        kind: 'jail',
        name: j.name,
        key: `jail:${j.name}`,
        href: `/jails/${encodeURIComponent(j.name)}`,
        state: j.status,
        labels: j.properties ?? {},
        usage: usage && !usage.error ? { pcpu: usage.pcpu, memory: usage.memoryuse } : undefined,
    }
}

export function fromVM(vm: VM): Instance {
    return {
        kind: 'vm',
        name: vm.name,
        key: `vm:${vm.name}`,
        href: `/vms/${encodeURIComponent(vm.name)}`,
        state: vm.state,
        labels: vm.properties ?? {},
        vcpus: vm.cpu,
        memory: vm.memory,
        lockedBy: vm.locked_by,
    }
}

export type SortKey = 'name' | 'kind' | 'state'

// Everything the view is narrowed by, all of it kept in the URL so a filtered
// view can be bookmarked and sent to someone.
export interface Filters {
    // A substring of the name, any case.
    q: string
    kind: '' | Kind
    state: string
    // Comma-separated terms, all of which must hold: `key=value` for that
    // value, a bare `key` for any value. Commas rather than spaces because a
    // ZFS property's value may contain a space.
    labels: string
    // A label key to group by; empty for one list.
    group: string
    sort: SortKey
    desc: boolean
}

export const noFilters: Filters = { q: '', kind: '', state: '', labels: '', group: '', sort: 'name', desc: false }

const sortKeys: SortKey[] = ['name', 'kind', 'state']

export function readFilters(search: string): Filters {
    const p = new URLSearchParams(search)
    const kind = p.get('kind')
    const sort = p.get('sort') as SortKey | null
    return {
        q: p.get('q') ?? '',
        kind: kind === 'jail' || kind === 'vm' ? kind : '',
        state: p.get('state') ?? '',
        labels: p.get('label') ?? '',
        group: p.get('group') ?? '',
        sort: sort && sortKeys.includes(sort) ? sort : 'name',
        desc: p.get('desc') === '1',
    }
}

// Only what differs from the defaults, so the plain view is a plain URL.
export function writeFilters(f: Filters): string {
    const p = new URLSearchParams()
    if (f.q) p.set('q', f.q)
    if (f.kind) p.set('kind', f.kind)
    if (f.state) p.set('state', f.state)
    if (f.labels) p.set('label', f.labels)
    if (f.group) p.set('group', f.group)
    if (f.sort !== 'name') p.set('sort', f.sort)
    if (f.desc) p.set('desc', '1')
    const s = p.toString()
    return s ? `?${s}` : ''
}

interface LabelTerm { key: string, value?: string }

export function labelTerms(labels: string): LabelTerm[] {
    return labels.split(',').map(t => t.trim()).filter(Boolean).map(t => {
        const at = t.indexOf('=')
        return at < 0 ? { key: t } : { key: t.slice(0, at).trim(), value: t.slice(at + 1).trim() }
    })
}

export function matches(i: Instance, f: Filters): boolean {
    if (f.q && !i.name.toLowerCase().includes(f.q.trim().toLowerCase())) return false
    if (f.kind && i.kind !== f.kind) return false
    if (f.state && i.state !== f.state) return false
    return labelTerms(f.labels).every(({ key, value }) =>
        key in i.labels && (value === undefined || i.labels[key] === value))
}

function compare(a: Instance, b: Instance, key: SortKey): number {
    const byName = a.name.localeCompare(b.name) || a.kind.localeCompare(b.kind)
    if (key === 'name') return byName
    return a[key].localeCompare(b[key]) || byName
}

export function visible(all: Instance[], f: Filters): Instance[] {
    const rows = all.filter(i => matches(i, f)).sort((a, b) => compare(a, b, f.sort))
    return f.desc ? rows.reverse() : rows
}

export interface Group {
    // The label's value, or null for the instances without the label.
    value: string | null
    rows: Instance[]
}

// Groups by one label key, values in order and the instances without it
// last: those are usually the ones worth a second look.
export function grouped(rows: Instance[], key: string): Group[] {
    if (!key) return [{ value: null, rows }]
    const by = new Map<string, Instance[]>()
    const without: Instance[] = []
    for (const row of rows) {
        if (key in row.labels) {
            const value = row.labels[key]
            by.set(value, [...(by.get(value) ?? []), row])
        } else {
            without.push(row)
        }
    }
    const groups: Group[] = [...by.keys()].sort((a, b) => a.localeCompare(b))
        .map(value => ({ value, rows: by.get(value)! }))
    if (without.length > 0) groups.push({ value: null, rows: without })
    return groups
}

// Every label key in use, for the group-by choice.
export function labelKeys(all: Instance[]): string[] {
    return [...new Set(all.flatMap(i => Object.keys(i.labels)))].sort((a, b) => a.localeCompare(b))
}

export type BulkOperation = 'start' | 'stop' | 'restart' | 'snapshot'

// Whether an operation applies to an instance, and if not, why -- said per
// instance, because "skipped" is not an answer to "what happened to db1".
// Decided here rather than left to the API to refuse: a bulk start of ten
// instances, three of them already running, is a start of seven.
export function skipReason(i: Instance, op: BulkOperation): string | null {
    if (op === 'snapshot') {
        return i.kind === 'vm'
            ? 'Guest snapshots are not in the API yet; vm-bhyve refuses a running guest without a force.'
            : null
    }
    const running = i.state === 'running'
    if (i.kind === 'vm' && i.state === 'bootloader') return 'Booting — try again once it is running.'
    if (i.kind === 'vm' && i.state === 'locked') return `Held by ${i.lockedBy ?? 'another host'}.`
    if (op === 'start') return running ? 'Already running.' : null
    return running ? null : 'Not running.'
}
