import { describe, expect, it } from 'vitest'
import type { MetricsReport } from './stores/metrics.svelte'
import { UsageWindow } from './usageWindow'

function report(jails: [string, number][], guests: [string, number][]): MetricsReport {
    return {
        jails: jails.map(([jail, pcpu]) => ({ jail, source: 'rctl', pcpu, memoryuse: pcpu * 10, processes: 1 })),
        guests: guests.map(([guest, pcpu]) => ({ guest, pcpu, memory_resident: pcpu * 100 })),
    } as unknown as MetricsReport
}

describe('UsageWindow', () => {
    it('keeps the last readings of each instance, a jail and a guest apart', () => {
        const w = new UsageWindow(3)
        for (const v of [1, 2, 3, 4]) w.record(report([['web', v]], [['web', v * 2]]))
        expect(w.get('jail:web')).toEqual({ cpu: [2, 3, 4], memory: [20, 30, 40] })
        expect(w.get('vm:web')?.cpu).toEqual([4, 6, 8])
    })

    it('forgets an instance that stops, so a restart starts a new line', () => {
        const w = new UsageWindow()
        w.record(report([['web', 1]], []))
        w.record(report([], []))
        w.record(report([['web', 5]], []))
        expect(w.get('jail:web')?.cpu).toEqual([5])
    })

    it('leaves out an instance that could not be read', () => {
        const w = new UsageWindow()
        const r = report([['web', 1]], [])
        r.jails[0].error = 'rctl: exit 1'
        w.record(r)
        expect(w.get('jail:web')).toBeUndefined()
    })
})
