import type { JailSpec, NetworkMode, ChildDatasetSpec } from './stores/jails.svelte'

// The create form's one translation: what the controls hold into what
// POST /api/jails takes. Out of the component so it can be tested without
// rendering one -- it is the place a control's value becomes the API's, and
// v0.9.0's NaN-to-unlimited bug lived here (#77).

// Rows carry an id of their own rather than being keyed by index. Measured
// (#77): with every field bound, index keys still show and send the right
// values -- Svelte 5 re-points a reused element's binding at the row now in
// its place. What they move is element identity: remove the first of three
// and the input that held row b holds row c, so a person's focus, caret or
// half-typed word jumps rows. The test for it is in NewJail.test.ts.
export interface ChildRow {
    id: number
    name: string
    properties: Record<string, string>
}

export interface TagRow {
    id: number
    name: string
    value: string
}

// What the form holds, exactly as the controls give it. paramValues is
// string | number | null because a number input binds a number and an
// emptied one binds null; typed() does the one conversion.
export interface SpecForm {
    name: string
    template: string
    hostname: string
    mode: NetworkMode
    ip4: string
    iface: string
    bridge: string
    isPrivate: boolean
    gateway: string
    autostart: boolean
    start: boolean
    limits: Record<string, string>
    paramValues: Record<string, string | number | null>
    children: ChildRow[]
    tags: TagRow[]
}

export const limitFields = [
    { name: 'memoryuse', label: 'memoryuse', hint: 'Resident memory, as 640M or 1G.' },
    { name: 'vmemoryuse', label: 'vmemoryuse', hint: 'Address space, as 1G.' },
    { name: 'maxproc', label: 'maxproc', hint: 'How many processes the jail may have.' },
    { name: 'openfiles', label: 'openfiles', hint: 'How many file descriptors.' },
    { name: 'pcpu', label: 'pcpu', hint: 'Percent of one core: 100 is a whole one.' },
    { name: 'cpuset', label: 'cpuset', hint: 'The cpus to pin it to: 0-3, or 0-3,8.' },
]

export const numericLimits = ['maxproc', 'openfiles', 'pcpu']

export const parameterFields: {
    name: string
    kind: 'number' | 'choice' | 'flag'
    choices?: string[]
    hint: string
}[] = [
    {
        name: 'devfs_ruleset', kind: 'number',
        hint: 'A ruleset the kernel has loaded. One it has not gives the jail the host’s whole /dev, so Jail Deck refuses it.',
    },
    { name: 'securelevel', kind: 'number', hint: '-1 to 3. 3 stops the jail lowering it or changing its firewall rules.' },
    { name: 'enforce_statfs', kind: 'number', hint: '0 shows every mount, 2 only the jail’s own.' },
    { name: 'sysvmsg', kind: 'choice', choices: ['disable', 'new'], hint: 'new gives the jail its own SysV message queues.' },
    { name: 'sysvsem', kind: 'choice', choices: ['disable', 'new'], hint: 'new gives the jail its own SysV semaphores.' },
    { name: 'sysvshm', kind: 'choice', choices: ['disable', 'new'], hint: 'new gives the jail its own SysV shared memory.' },
    { name: 'allow.chflags', kind: 'flag', hint: 'Lets the jail’s root change file flags inside itself.' },
    { name: 'allow.set_hostname', kind: 'flag', hint: 'Lets the jail change its own hostname.' },
]

export const datasetProperties = [
    { name: 'mountpoint', hint: 'A path on the host, inside the jail. Left empty it inherits <jail path>/<name>, which is /<name> inside.' },
    { name: 'refquota', hint: 'The limit this dataset may use, as 2G or 512M. Where JD-007 puts a jail’s quota.' },
    { name: 'recordsize', hint: 'A power of two from 512B to 16M. Leave it alone unless the workload asks.' },
    { name: 'compression', hint: 'lz4, zstd, off — whatever this ZFS supports.' },
    { name: 'atime', hint: 'on or off. Off stops a read from being a write.' },
]

// asNumber keeps something that is not a number as it was typed, so the API
// refuses it by name. Number('abc') is NaN, which JSON writes as null, which
// the server reads as "no limit asked for" -- a typo that silently leaves a
// jail unlimited is the one outcome worth going out of the way to avoid.
export function asNumber(value: string): string | number {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : value
}

// typed() is the one place a control's value becomes what the API takes.
export function typed(value: string | number | null | undefined): string {
    return value === null || value === undefined ? '' : String(value).trim()
}

// buildSpec sends only what was filled in. An empty box is "say nothing
// about this", not "set it empty": the API treats a field set for a mode
// that ignores it as a mistake, and an empty string is a value.
export function buildSpec(form: SpecForm): JailSpec {
    const { name, template, hostname, mode, ip4, iface, bridge, isPrivate, gateway,
        autostart, start, limits, paramValues, children, tags } = form
    const network: JailSpec['network'] = { mode }
    if (mode === 'alias') {
        network.ip4 = ip4.trim()
        network.interface = iface.trim()
    }
    if (mode === 'vnet') {
        network.bridge = bridge.trim()
        if (isPrivate) network.private = true
        if (ip4.trim()) network.ip4 = ip4.trim()
        if (gateway.trim()) network.gateway = gateway.trim()
    }

    const spec: JailSpec = { name: name.trim(), template, network }
    if (hostname.trim()) spec.hostname = hostname.trim()
    if (autostart) spec.autostart = true
    if (!start) spec.start = false

    const asked: Record<string, string | number> = {}
    for (const field of limitFields) {
        const value = (limits[field.name] ?? '').trim()
        if (!value) continue
        asked[field.name] = numericLimits.includes(field.name) ? asNumber(value) : value
    }
    if (Object.keys(asked).length > 0) spec.limits = asked as JailSpec['limits']

    const params: Record<string, string | number | boolean> = {}
    for (const field of parameterFields) {
        const value = typed(paramValues[field.name])
        if (!value) continue
        if (field.kind === 'number') params[field.name] = Number(value)
        else if (field.kind === 'flag') params[field.name] = value === 'true'
        else params[field.name] = value
    }
    if (Object.keys(params).length > 0) spec.params = params

    const datasets: ChildDatasetSpec[] = []
    for (const child of children) {
        if (!child.name.trim()) continue
        const properties: Record<string, string> = {}
        for (const property of datasetProperties) {
            const value = (child.properties[property.name] ?? '').trim()
            if (value) properties[property.name] = value
        }
        const entry: ChildDatasetSpec = { name: child.name.trim() }
        if (Object.keys(properties).length > 0) entry.properties = properties
        datasets.push(entry)
    }
    if (datasets.length > 0) spec.datasets = datasets

    const properties: Record<string, string> = {}
    for (const tag of tags) {
        if (tag.name.trim()) properties[tag.name.trim()] = tag.value
    }
    if (Object.keys(properties).length > 0) spec.properties = properties

    return spec
}
