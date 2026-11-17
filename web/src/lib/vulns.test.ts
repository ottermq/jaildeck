import { describe, expect, it } from 'vitest'
import { age, databaseNote, vulnerableCount, vulnLabel, vulnTitle, vulnTone, type VulnSummary } from './vulns'

const now = new Date('2026-11-17T12:00:00Z')

describe('vulns', () => {
    it('labels and colours each status', () => {
        const cases: [VulnSummary, string, string][] = [
            [{ status: 'clean', packages: 12, vulnerable: 0 }, 'clean', 'ok'],
            [{ status: 'vulnerable', packages: 20, vulnerable: 5 }, '5 vulnerable', 'warn'],
            [{ status: 'unknown', packages: 0, vulnerable: 0 }, 'unknown', 'muted'],
            [{ status: 'unchecked', packages: 0, vulnerable: 0 }, 'not checked', 'muted'],
        ]
        for (const [summary, label, tone] of cases) {
            expect(vulnLabel(summary)).toBe(label)
            expect(vulnTone(summary)).toBe(tone)
        }
    })

    it('never says unknown or unchecked the way it says clean', () => {
        for (const status of ['unknown', 'unchecked'] as const) {
            const s: VulnSummary = { status, packages: 0, vulnerable: 0 }
            expect(vulnTone(s)).not.toBe('ok')
            expect(vulnTitle(s, 'jail')).not.toMatch(/None of|No packages/)
        }
    })

    it('explains a vulnerable template in terms of its clones', () => {
        const s: VulnSummary = { status: 'vulnerable', packages: 20, vulnerable: 1 }
        expect(vulnTitle(s, 'jail')).toBe('1 of its 20 packages is in the vulnerability database.')
        expect(vulnTitle(s, 'template')).toMatch(/Every jail cloned from this template starts with them/)
        expect(vulnTitle({ status: 'clean', packages: 0, vulnerable: 0 }, 'jail')).toBe('No packages installed.')
    })

    it('counts what is vulnerable', () => {
        expect(vulnerableCount([
            { status: 'vulnerable', packages: 3, vulnerable: 2 },
            { status: 'clean', packages: 3, vulnerable: 0 },
            { status: 'unknown', packages: 0, vulnerable: 0 },
        ])).toBe(1)
    })

    it('says how long ago', () => {
        expect(age('2026-11-17T11:59:40Z', now)).toBe('just now')
        expect(age('2026-11-17T11:15:00Z', now)).toBe('45 min ago')
        expect(age('2026-11-17T04:45:00Z', now)).toBe('7 h ago')
        expect(age('2026-11-13T12:00:00Z', now)).toBe('4 days ago')
        // A clock a little ahead of the server's is not "in the future".
        expect(age('2026-11-17T12:00:30Z', now)).toBe('just now')
    })

    it('warns about a missing or stale database', () => {
        const path = '/var/db/pkg/vuln.xml'
        expect(databaseNote({ path, present: true, updated: '2026-11-17T04:45:00Z', stale: false }, now))
            .toEqual({ text: 'Vulnerability database fetched 7 h ago.', warn: false })
        expect(databaseNote({ path, present: true, updated: '2026-11-13T12:00:00Z', stale: true }, now).warn).toBe(true)
        const missing = databaseNote({ path, present: false, stale: false }, now)
        expect(missing.warn).toBe(true)
        expect(missing.text).toMatch(/No vulnerability database at \/var\/db\/pkg\/vuln\.xml/)
    })
})
