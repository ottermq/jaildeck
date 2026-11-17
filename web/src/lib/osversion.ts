// FreeBSD versions (#229): the host's, and each jail's and template's userland
// judged against the host's. The server does the judging, so the API, /metrics
// and this page always agree; this file only says it in words.

export type UserlandStatus = 'current' | 'behind' | 'ahead' | 'other-release' | 'unknown'

// Standing is a jail's or a template's userland against the host's.
export interface Standing {
    // "" when it could not be read, or was not shaped like a version.
    version: string
    status: UserlandStatus
    // Patch levels behind or ahead, for those two statuses only.
    levels?: number
    // The host userland it was compared with.
    host?: string
}

export interface HostVersions {
    kernel_running: string
    kernel_installed: string
    userland: string
    // The installed kernel is not the running one: an update waits for a
    // reboot.
    reboot_pending: boolean
}

export interface NamedStanding extends Standing {
    name: string
}

export interface OSVersions {
    host: HostVersions
    jails: NamedStanding[]
    templates: NamedStanding[]
}

// patchOf is the short form a list has room for: "p2" for 15.1-RELEASE-p2, the
// whole version for one with no patch level.
export function patchOf(version: string): string {
    const m = /-(p[0-9]+)$/.exec(version)
    return m ? m[1] : version
}

// userlandLabel is what the badge says.
export function userlandLabel(s: Standing): string {
    switch (s.status) {
        case 'current':
            return `${patchOf(s.version)} · current`
        case 'behind':
            return `${patchOf(s.version)} · ${s.levels} behind`
        case 'ahead':
            return `${patchOf(s.version)} · ahead`
        case 'other-release':
            return s.version
        default:
            return 'unknown'
    }
}

// userlandTone is the badge's colour: a warning only for "behind", the one
// state that asks for an update. Another release, ahead and unknown are said
// plainly, without alarm.
export function userlandTone(s: Standing): 'ok' | 'warn' | 'muted' {
    switch (s.status) {
        case 'current':
            return 'ok'
        case 'behind':
            return 'warn'
        default:
            return 'muted'
    }
}

// userlandTitle is the sentence behind the badge, for its tooltip and for a
// detail page.
export function userlandTitle(s: Standing, what: 'jail' | 'template'): string {
    const host = s.host ? `the host's ${s.host}` : "the host's userland"
    switch (s.status) {
        case 'current':
            return `${s.version}, the same as ${host}.`
        case 'behind': {
            const levels = s.levels === 1 ? '1 patch level' : `${s.levels} patch levels`
            return what === 'jail'
                ? `${s.version}, ${levels} behind ${host}. Updating the host doesn't update a jail: its userland is its own copy.`
                : `${s.version}, ${levels} behind ${host}. Every jail cloned from this template starts at this level.`
        }
        case 'ahead':
            return `${s.version}, newer than ${host}: updated after the host last was.`
        case 'other-release':
            return `${s.version}, a different release from ${host}, so patch levels don't compare.`
        default:
            return what === 'jail'
                ? 'Could not read this jail\'s version: it has no path, or its bin/freebsd-version is missing or not a version.'
                : 'Could not read this template\'s version.'
    }
}

// behindCount is how many of a list are behind the host.
export function behindCount(list: NamedStanding[]): number {
    return list.filter((s) => s.status === 'behind').length
}
