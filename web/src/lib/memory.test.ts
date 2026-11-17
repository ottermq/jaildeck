import { describe, expect, it } from 'vitest'
import { heldBytes, segments } from './memory'
import type { Memory } from './stores/metrics.svelte'

const gib = 2 ** 30

describe('memory', () => {
    it('splits held from what is given back on demand', () => {
        const m: Memory = {
            physical: 128 * gib, free: 101 * gib, active: 3 * gib, inactive: 14 * gib,
            wired: 7 * gib, laundry: 0, arc: 5 * gib, arc_max: 127 * gib, held: 8 * gib,
        }
        const s = segments(m)
        expect(s.map((x) => x.key)).toEqual(['held', 'inactive', 'arc'])
        expect(s[0].bytes).toBe(8 * gib)
        expect(s[0].percent).toBeCloseTo(6.25)
        expect(s[2].title).toMatch(/may grow/)
    })

    it('has no ARC segment on a host without ZFS', () => {
        const s = segments({ physical: 16 * gib, free: 4 * gib, active: 1, inactive: 2 * gib, wired: 1, held: 10 * gib })
        expect(s.map((x) => x.key)).toEqual(['held', 'inactive'])
    })

    it('never draws past the end of the bar', () => {
        const s = segments({ physical: 10, free: 0, active: 0, inactive: 6, wired: 0, arc: 6, held: 5 })
        expect(s.reduce((sum, x) => sum + x.percent, 0)).toBeLessThanOrEqual(100)
        expect(s[2].percent).toBe(0)
    })

    it('falls back to the old figure from a server that sends no held', () => {
        const m: Memory = { physical: 16 * gib, free: 4 * gib, active: 0, inactive: 2 * gib, wired: 0 }
        expect(heldBytes(m)).toBe(12 * gib)
        expect(segments(m).map((x) => x.key)).toEqual(['held'])
    })
})
