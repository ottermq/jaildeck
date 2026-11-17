import { describe, expect, it } from 'vitest'
import { furthestBehind, jobsFor, since, type ReplicationSnapshot } from './replication'

const now = new Date('2026-11-17T17:00:00Z')

const snap: ReplicationSnapshot = {
    status: 'replicating', checked: '2026-11-17T16:59:30Z',
    jobs: [
        { name: 'local_push', oldest: '2026-11-17T16:36:00Z', datasets: [
            { name: 'zroot/jails/nginx', replicated: '2026-11-17T16:51:00Z' },
            { name: 'zroot/jails/nginx/logs', replicated: '2026-11-17T16:36:00Z' },
            { name: 'zroot/jails/redis', replicated: '2026-11-17T16:51:00Z' },
        ] },
        { name: 'offsite', oldest: '2026-11-17T11:48:00Z', datasets: [
            { name: 'zroot/jails/redis', replicated: '2026-11-17T11:48:00Z' },
        ] },
    ],
}

describe('replication', () => {
    it('says how long ago in at most two units', () => {
        expect(since('2026-11-17T16:59:40Z', now)).toBe('under a minute')
        expect(since('2026-11-17T16:51:00Z', now)).toBe('9 min')
        expect(since('2026-11-17T15:46:00Z', now)).toBe('1 h 14 min')
        expect(since('2026-11-17T15:00:00Z', now)).toBe('2 h')
        expect(since('2026-11-14T17:00:00Z', now)).toBe('3 days')
    })

    it('heads with the job furthest behind', () => {
        expect(furthestBehind(snap)?.name).toBe('offsite')
        expect(furthestBehind({ status: 'none', jobs: [] })).toBeUndefined()
    })

    it('finds the jobs replicating a jail and its oldest dataset', () => {
        expect(jobsFor(snap, ['zroot/jails/nginx', 'zroot/jails/nginx/logs'])).toEqual([
            { name: 'local_push', oldest: '2026-11-17T16:36:00Z', count: 2 },
        ])
        expect(jobsFor(snap, ['zroot/jails/redis']).map((j) => j.name)).toEqual(['local_push', 'offsite'])
        expect(jobsFor(snap, ['zroot/jails/other'])).toEqual([])
    })
})
