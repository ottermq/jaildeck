// Vulnerable packages (#230): the last audit of the host's, each jail's and
// each template's packages against the host's vulnerability database. The
// server audits on a timer of its own; this file only says it in words.

export type VulnStatus = 'clean' | 'vulnerable' | 'unknown' | 'unchecked'

// Summary is one tree's audit in numbers: what a row carries.
export interface VulnSummary {
    status: VulnStatus
    packages: number
    vulnerable: number
    checked?: string
}

export interface VulnIssue {
    description: string
    cves: string[]
    url: string
    affected: string[]
}

export interface VulnFinding {
    name: string
    version: string
    issues: VulnIssue[]
    // Installed packages that depend on this one. Shown, never counted.
    reverse_dependencies: string[]
}

export interface VulnReport extends VulnSummary {
    findings: VulnFinding[]
    // Why the status is unknown or unchecked.
    reason?: string
}

export interface NamedVulnReport extends VulnReport {
    name: string
}

export interface VulnDatabase {
    path: string
    present: boolean
    updated?: string
    // Over three days old: an old database finds nothing new.
    stale: boolean
}

// VulnSnapshot is GET /api/vulnerabilities.
export interface VulnSnapshot {
    checked?: string
    database: VulnDatabase
    host: VulnReport
    jails: NamedVulnReport[]
    templates: NamedVulnReport[]
}

export interface NamedVulnSummary extends VulnSummary {
    name: string
}

// VulnCounts is the same audit without its findings, in GET /api/metrics.
export interface VulnCounts {
    checked?: string
    database: VulnDatabase
    host: VulnSummary
    jails: NamedVulnSummary[]
    templates: NamedVulnSummary[]
}

// vulnLabel is what the badge says.
export function vulnLabel(s: VulnSummary): string {
    switch (s.status) {
        case 'clean':
            return 'clean'
        case 'vulnerable':
            return `${s.vulnerable} vulnerable`
        case 'unknown':
            return 'unknown'
        default:
            return 'not checked'
    }
}

// vulnTone is the badge's colour: a warning only for vulnerable. Unknown and
// unchecked are said plainly, and never look like clean.
export function vulnTone(s: VulnSummary): 'ok' | 'warn' | 'muted' {
    switch (s.status) {
        case 'clean':
            return 'ok'
        case 'vulnerable':
            return 'warn'
        default:
            return 'muted'
    }
}

function packages(n: number): string {
    return n === 1 ? '1 package' : `${n} packages`
}

// vulnTitle is the sentence behind the badge.
export function vulnTitle(s: VulnSummary, what: 'host' | 'jail' | 'template'): string {
    switch (s.status) {
        case 'clean':
            return s.packages === 0
                ? 'No packages installed.'
                : `None of its ${packages(s.packages)} is in the vulnerability database.`
        case 'vulnerable': {
            const tail = what === 'template' ? ' Every jail cloned from this template starts with them.' : ''
            return `${s.vulnerable} of its ${packages(s.packages)} ${s.vulnerable === 1 ? 'is' : 'are'} in the vulnerability database.${tail}`
        }
        case 'unknown':
            return 'Could not be audited: its packages could not be listed, or there is no vulnerability database.'
        default:
            return what === 'host'
                ? 'Not audited yet: the first audit runs when Jail Deck starts.'
                : `Not audited yet: the audit runs every hour, and this ${what} is newer than the last one.`
    }
}

// vulnerableCount is how many of a list have a vulnerable package.
export function vulnerableCount(list: VulnSummary[]): number {
    return list.filter((s) => s.status === 'vulnerable').length
}

// age says how long ago an ISO time was, in the largest unit that fits.
export function age(iso: string, now: Date = new Date()): string {
    const minutes = Math.max(0, Math.floor((now.getTime() - new Date(iso).getTime()) / 60000))
    if (minutes < 1) return 'just now'
    if (minutes < 60) return `${minutes} min ago`
    const hours = Math.floor(minutes / 60)
    if (hours < 48) return `${hours} h ago`
    return `${Math.floor(hours / 24)} days ago`
}

// databaseNote says what the database is worth: missing and stale are
// warnings, because both make "nothing found" mean less than it says.
export function databaseNote(db: VulnDatabase, now: Date = new Date()): { text: string, warn: boolean } {
    if (!db.present || !db.updated) {
        return { text: `No vulnerability database at ${db.path}: the daily security run fetches it.`, warn: true }
    }
    if (db.stale) {
        return { text: `Vulnerability database fetched ${age(db.updated, now)}: stale. Is the daily security run on?`, warn: true }
    }
    return { text: `Vulnerability database fetched ${age(db.updated, now)}.`, warn: false }
}
