import { describe, expect, it } from 'vitest'
import { runAll } from './bulk'
import type { Outcome } from './bulk'

describe('runAll', () => {
    it('runs every item, never more than the limit at once, and a failure is only its own', async () => {
        let inFlight = 0
        let most = 0
        const last: Record<number, Outcome> = {}
        const seen: Record<number, string[]> = {}
        await runAll(
            [1, 2, 3, 4, 5, 6, 7],
            async n => {
                inFlight++
                most = Math.max(most, inFlight)
                await new Promise(r => setTimeout(r, 5 * (n % 3)))
                inFlight--
                if (n === 3) throw new Error('refused')
                if (n === 5) return { detail: 'powered off', warn: true }
            },
            (n, outcome) => {
                last[n] = outcome
                seen[n] = [...(seen[n] ?? []), outcome.status]
            },
            err => (err as Error).message,
            3,
        )
        expect(most).toBe(3)
        expect(last[3]).toEqual({ status: 'failed', detail: 'refused' })
        expect(last[5]).toEqual({ status: 'done', detail: 'powered off', warn: true })
        for (const n of [1, 2, 4, 6, 7]) expect(last[n]).toEqual({ status: 'done' })
        for (const n of [1, 2, 3, 4, 5, 6, 7]) expect(seen[n].slice(0, 2)).toEqual(['queued', 'running'])
    })

    it('does nothing for nothing', async () => {
        await runAll([], async () => { throw new Error('called') }, () => { throw new Error('reported') }, String)
    })
})
