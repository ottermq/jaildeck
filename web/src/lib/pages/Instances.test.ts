import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import type { JailData, Operation, SnapshotResult } from '../stores/jails.svelte'
import type { Listing, VMOperation } from '../stores/vms.svelte'
import { ApiError } from '../services/api'
import Instances from './Instances.svelte'

// Two jails and two guests, a jail and a guest sharing a name, labelled for
// two tenants (#129).

let jails: JailData[]
let listing: Listing
let calls: string[]
let failing: Record<string, Error>
let snapshotAnswer: SnapshotResult
let confirmed: boolean
let asked: { title: string, items?: string[], lines?: string[] }[]

vi.mock('../stores/jails.svelte', async (original) => ({
    ...(await original<typeof import('../stores/jails.svelte')>()),
    listJails: vi.fn(async () => structuredClone(jails)),
    runJailOperation: vi.fn(async (name: string, op: Operation) => {
        calls.push(`jail ${op} ${name}`)
        if (failing[name]) throw failing[name]
        return jails.find(j => j.name === name)
    }),
    createSnapshot: vi.fn(async (name: string, child: string, short: string) => {
        calls.push(`jail snapshot ${name}@${short}${child ? ' ' + child : ''}`)
        return snapshotAnswer
    }),
}))
vi.mock('../stores/vms.svelte', async (original) => ({
    ...(await original<typeof import('../stores/vms.svelte')>()),
    listVMs: vi.fn(async () => structuredClone(listing)),
    runVMOperation: vi.fn(async (name: string, op: VMOperation) => {
        calls.push(`vm ${op} ${name}`)
        return { ...listing.vms.find(v => v.name === name)!, forced: name === 'web' }
    }),
}))
vi.mock('../stores/metrics.svelte', () => ({
    getMetrics: vi.fn(async () => ({ jails: [{ jail: 'web', source: 'rctl', pcpu: 12.5, memoryuse: 41943040, processes: 3 }] })),
}))
vi.mock('../stores/confirm.svelte', () => ({
    ask: vi.fn(async (c: { title: string, items?: string[] }) => {
        asked.push(c)
        return confirmed
    }),
}))

beforeEach(() => {
    jails = [
        { jid: '1', name: 'web', status: 'running', hostname: 'web', ip: '', path: '/j/web', properties: { 'otter:tenant': 'acme' } },
        { jid: '', name: 'db', status: 'stopped', hostname: 'db', ip: '', path: '/j/db', properties: { 'otter:tenant': 'globex' } },
    ]
    listing = {
        vm_bhyve: 'ready', switches: [], vms: [
            { name: 'web', datastore: 'default', state: 'running', cpu: '2', memory: '512M', autostart: false, properties: { 'otter:tenant': 'acme' } },
            { name: 'batch', datastore: 'default', state: 'stopped', cpu: '1', memory: '256M', autostart: false, properties: {} },
        ],
    }
    calls = []
    failing = {}
    confirmed = true
    asked = []
    snapshotAnswer = { snapshot: 'p/j/db@x', created: ['p/j/db@x'], destroyed: [] }
    history.replaceState({}, '', '/instances')
})

function rowNames(): string[] {
    return screen.getAllByRole('checkbox', { name: /^Select (?!every)/ }).map(c => c.getAttribute('aria-label')!.slice(7))
}

async function selectAll() {
    await fireEvent.click(await screen.findByRole('checkbox', { name: 'Select every instance shown' }))
}

describe('Instances', () => {
    it('lists jails and guests together, a jail\'s usage beside a guest\'s size', async () => {
        render(Instances)
        await screen.findByText('Showing 4 of 4')
        expect(rowNames()).toEqual(['batch', 'db', 'web', 'web'])
        expect(screen.getByText('12.5%')).toBeTruthy()
        expect(screen.getByText('40M', { exact: false })).toBeTruthy()
        expect(screen.getByText('512M')).toBeTruthy()
    })

    it('starts from the filters in the address and keeps them there', async () => {
        history.replaceState({}, '', '/instances?label=otter%3Atenant%3Dacme&group=otter%3Atenant')
        render(Instances)
        await screen.findByText('Showing 2 of 4')
        expect(screen.getByRole('heading', { name: /otter:tenant=acme/ })).toBeTruthy()
        await fireEvent.change(screen.getByLabelText('Kind'), { target: { value: 'vm' } })
        await waitFor(() => expect(window.location.search).toContain('kind=vm'))
        expect(window.location.search).toContain('label=otter%3Atenant%3Dacme')
        expect(screen.getByText('Showing 1 of 4')).toBeTruthy()
    })

    it('adds a label to the filter when it is clicked', async () => {
        render(Instances)
        await screen.findByText('Showing 4 of 4')
        await fireEvent.click(screen.getAllByRole('button', { name: 'otter:tenant=globex' })[0])
        expect(screen.getByText('Showing 1 of 4')).toBeTruthy()
        expect((screen.getByLabelText('Labels') as HTMLInputElement).value).toBe('otter:tenant=globex')
    })

    it('stops what is running after one confirmation, and reports each one', async () => {
        failing.web = new ApiError(500, 'service jail stop web: exit status 1')
        render(Instances)
        await selectAll()
        await fireEvent.click(screen.getByRole('button', { name: 'Stop' }))
        await screen.findByText('Stop of 4 instances')
        expect(asked).toHaveLength(1)
        expect(asked[0].title).toBe('Stop 2 instances?')
        expect(asked[0].items).toEqual(['web (jail)', 'web (guest)'])
        await waitFor(() => expect(screen.getByText(/1 failed/)).toBeTruthy())
        expect(calls.sort()).toEqual(['jail stop web', 'vm stop web'])
        expect(screen.getByText('service jail stop web: exit status 1')).toBeTruthy()
        expect(screen.getByText(/powered off/)).toBeTruthy()
        expect(screen.getAllByText('Not running.')).toHaveLength(2)
    })

    it('runs nothing when the confirmation is declined', async () => {
        confirmed = false
        render(Instances)
        await selectAll()
        await fireEvent.click(screen.getByRole('button', { name: 'Restart' }))
        await waitFor(() => expect(asked).toHaveLength(1))
        expect(calls).toEqual([])
        expect(screen.queryByText(/Restart of/)).toBeNull()
    })

    it('snapshots the jails only, and says why each guest was left out', async () => {
        render(Instances)
        await selectAll()
        await fireEvent.click(screen.getByRole('button', { name: 'Snapshot…' }))
        await fireEvent.input(screen.getByLabelText('Snapshot name'), { target: { value: 'nightly' } })
        await fireEvent.click(screen.getByRole('button', { name: 'Snapshot' }))
        await screen.findByText('Snapshot of 4 instances')
        expect(asked[0].title).toBe('Snapshot 2 jails as @nightly?')
        await waitFor(() => expect(calls.sort()).toEqual(['jail snapshot db@nightly', 'jail snapshot web@nightly']))
        expect(screen.getAllByText(/Guest snapshots are not in the API yet/)).toHaveLength(2)
    })

    it('drops from the selection what a filter hides', async () => {
        render(Instances)
        await selectAll()
        expect(screen.getByText('4 selected')).toBeTruthy()
        await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'web' } })
        await waitFor(() => expect(screen.getByText('2 selected')).toBeTruthy())
        await fireEvent.input(screen.getByLabelText('Name'), { target: { value: '' } })
        expect(screen.getByText('2 selected')).toBeTruthy()
    })
})
