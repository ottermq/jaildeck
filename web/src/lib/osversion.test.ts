import { describe, expect, it } from 'vitest'
import { behindCount, patchOf, userlandLabel, userlandTitle, userlandTone, type Standing } from './osversion'

const host = '15.1-RELEASE-p6'

describe('osversion', () => {
    it('shortens a version to its patch level', () => {
        expect(patchOf('15.1-RELEASE-p2')).toBe('p2')
        expect(patchOf('15.1-RELEASE')).toBe('15.1-RELEASE')
        expect(patchOf('16.0-CURRENT')).toBe('16.0-CURRENT')
    })

    it('labels and colours each status', () => {
        const cases: [Standing, string, string][] = [
            [{ version: host, status: 'current', host }, 'p6 · current', 'ok'],
            [{ version: '15.1-RELEASE-p2', status: 'behind', levels: 4, host }, 'p2 · 4 behind', 'warn'],
            [{ version: '15.1-RELEASE-p7', status: 'ahead', levels: 1, host }, 'p7 · ahead', 'muted'],
            [{ version: '14.3-RELEASE-p4', status: 'other-release', host }, '14.3-RELEASE-p4', 'muted'],
            [{ version: '', status: 'unknown', host }, 'unknown', 'muted'],
        ]
        for (const [standing, label, tone] of cases) {
            expect(userlandLabel(standing)).toBe(label)
            expect(userlandTone(standing)).toBe(tone)
        }
    })

    it('explains behind differently for a jail and a template', () => {
        const behind: Standing = { version: '15.1-RELEASE-p5', status: 'behind', levels: 1, host }
        expect(userlandTitle(behind, 'jail')).toMatch(/1 patch level behind the host's 15.1-RELEASE-p6\. Updating the host doesn't update a jail/)
        expect(userlandTitle(behind, 'template')).toMatch(/Every jail cloned from this template starts at this level/)
        expect(userlandTitle({ ...behind, levels: 4 }, 'jail')).toMatch(/4 patch levels behind/)
    })

    it('counts what is behind', () => {
        expect(behindCount([
            { name: 'a', version: host, status: 'current', host },
            { name: 'b', version: '15.1-RELEASE-p2', status: 'behind', levels: 4, host },
            { name: 'c', version: '', status: 'unknown', host },
        ])).toBe(1)
    })
})
