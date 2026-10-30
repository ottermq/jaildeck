import { describe, expect, it } from 'vitest'
import {
    fromJail, fromVM, grouped, labelKeys, labelTerms, matches, noFilters, readFilters, skipReason, visible, writeFilters,
} from './instances'
import type { Filters, Instance } from './instances'

const web = fromJail({ jid: '1', name: 'web', status: 'running', hostname: '', ip: '', path: '/j/web',
    properties: { 'otter:tenant': 'acme', 'otter:tier': 'frontend' } },
{ jail: 'web', source: 'rctl', pcpu: 12.5, memoryuse: 1024, processes: 3 })
const db = fromJail({ jid: '', name: 'db', status: 'stopped', hostname: '', ip: '', path: '/j/db',
    properties: { 'otter:tenant': 'globex' } })
const guest = fromVM({ name: 'web', datastore: 'default', state: 'stopped', cpu: '2', memory: '512M', autostart: false,
    properties: { 'otter:tenant': 'acme', 'otter:note': 'has a space' } })
const locked = fromVM({ name: 'shared', datastore: 'nfs', state: 'locked', locked_by: 'orthanc', autostart: false, properties: {} })
const all = [web, db, guest, locked]

const f = (over: Partial<Filters>): Filters => ({ ...noFilters, ...over })

describe('instances', () => {
    it('keys a jail and a guest of the same name apart', () => {
        expect(web.key).not.toBe(guest.key)
        expect(guest.href).toBe('/vms/web')
        expect(web.usage).toEqual({ pcpu: 12.5, memory: 1024 })
        expect(db.usage).toBeUndefined()
    })

    it('round-trips the filters through the URL, writing only what is not a default', () => {
        expect(writeFilters(noFilters)).toBe('')
        const set = f({ q: 'we', kind: 'vm', state: 'stopped', labels: 'otter:tenant=acme, otter:tier', group: 'otter:tenant', sort: 'state', desc: true })
        expect(readFilters(writeFilters(set))).toEqual(set)
        expect(readFilters('?kind=container&sort=cpu')).toEqual(noFilters)
    })

    it('reads label terms as key=value or a bare key, values with spaces included', () => {
        expect(labelTerms(' otter:tenant = acme ,otter:tier,, otter:note=has a space')).toEqual([
            { key: 'otter:tenant', value: 'acme' }, { key: 'otter:tier' }, { key: 'otter:note', value: 'has a space' },
        ])
    })

    it('needs every filter to hold', () => {
        expect(all.filter(i => matches(i, f({ q: 'WE' })))).toEqual([web, guest])
        expect(all.filter(i => matches(i, f({ kind: 'vm' })))).toEqual([guest, locked])
        expect(all.filter(i => matches(i, f({ state: 'stopped' })))).toEqual([db, guest])
        expect(all.filter(i => matches(i, f({ labels: 'otter:tenant=acme' })))).toEqual([web, guest])
        expect(all.filter(i => matches(i, f({ labels: 'otter:tenant=acme, otter:tier' })))).toEqual([web])
        expect(all.filter(i => matches(i, f({ labels: 'otter:note=has a space' })))).toEqual([guest])
        expect(all.filter(i => matches(i, f({ labels: 'otter:tenant=acm' })))).toEqual([])
    })

    it('sorts by name, then kind, either way', () => {
        expect(visible(all, noFilters).map(i => i.key)).toEqual(['jail:db', 'vm:shared', 'jail:web', 'vm:web'])
        expect(visible(all, f({ desc: true })).map(i => i.key)).toEqual(['vm:web', 'jail:web', 'vm:shared', 'jail:db'])
        expect(visible(all, f({ sort: 'state' })).map(i => i.state)).toEqual(['locked', 'running', 'stopped', 'stopped'])
    })

    it('groups by a label value, with the instances lacking it last', () => {
        const groups = grouped(visible(all, noFilters), 'otter:tenant')
        expect(groups.map(g => [g.value, g.rows.map(r => r.key)])).toEqual([
            ['acme', ['jail:web', 'vm:web']],
            ['globex', ['jail:db']],
            [null, ['vm:shared']],
        ])
        expect(grouped(all, '')).toEqual([{ value: null, rows: all }])
        expect(labelKeys(all)).toEqual(['otter:note', 'otter:tenant', 'otter:tier'])
    })

    it('says per instance why an operation does not apply', () => {
        expect(skipReason(web, 'start')).toBe('Already running.')
        expect(skipReason(db, 'start')).toBeNull()
        expect(skipReason(db, 'stop')).toBe('Not running.')
        expect(skipReason(web, 'restart')).toBeNull()
        expect(skipReason(locked, 'start')).toBe('Held by orthanc.')
        const booting: Instance = { ...guest, state: 'bootloader' }
        expect(skipReason(booting, 'stop')).toContain('Booting')
        const suspended: Instance = { ...guest, state: 'suspended' }
        expect(skipReason(suspended, 'start')).toBeNull()
        expect(skipReason(db, 'snapshot')).toBeNull()
        expect(skipReason(web, 'snapshot')).toBeNull()
        expect(skipReason(guest, 'snapshot')).toBeNull()
        expect(skipReason({ ...guest, state: 'running' }, 'snapshot')).toBeNull()
    })
})
