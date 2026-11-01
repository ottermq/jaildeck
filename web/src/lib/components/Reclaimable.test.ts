import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte'
import { ApiError } from '../services/api'
import type { Reclaimable as Report, ReclaimableItem } from '../stores/host.svelte'
import Reclaimable from './Reclaimable.svelte'

let confirmed: boolean
let asked: { title: string, items?: string[] }[]
let report: Report
let ages: (number | undefined)[]
let removed: string[]
let refuse: Record<string, ApiError>

vi.mock('../stores/confirm.svelte', () => ({
    ask: vi.fn(async (c: { title: string, items?: string[] }) => {
        asked.push(c)
        return confirmed
    }),
}))

vi.mock('../stores/host.svelte', () => ({
    getReclaimable: vi.fn(async (age?: number) => {
        ages.push(age)
        return structuredClone(report)
    }),
    reclaim: vi.fn(async (item: ReclaimableItem) => {
        removed.push(item.remove!.path)
        if (refuse[item.name]) throw refuse[item.name]
        report.items = report.items.filter(i => i.name !== item.name)
    }),
}))

const M = 1 << 20

function item(kind: ReclaimableItem['kind'], name: string, size: number, path?: string): ReclaimableItem {
    return { kind, name, size, why: `${name} is unused`, remove: path ? { method: 'DELETE', path } : undefined }
}

beforeEach(() => {
    confirmed = true
    asked = []
    ages = []
    removed = []
    refuse = {}
    report = {
        items: [
            item('template', '14.3-RELEASE', 380 * M, '/api/templates/14.3-RELEASE?confirm=14.3-RELEASE'),
            item('template', 'half', 10 * M, '/api/templates/half?confirm=half'),
            item('retained', 'kept', 2 * M, '/api/retained/kept?confirm=kept'),
            item('retained', 'also-kept', 1 * M, '/api/retained/also-kept?confirm=also-kept'),
            item('archive', '/usr/local/jails/media/15.1-RELEASE/base.txz', 157 * M),
        ],
        total: 550 * M,
        kinds: { template: 390 * M, retained: 3 * M, archive: 157 * M },
        snapshot_age_days: 30,
        skipped_snapshots: { 'zrepl_': 12 },
        problems: [],
    }
})

function group(title: string): HTMLElement {
    return screen.getByRole('heading', { name: title }).closest('.group') as HTMLElement
}

describe('Reclaimable', () => {
    it('groups what is unused by kind, with the total and what was left out', async () => {
        render(Reclaimable)
        expect(await screen.findByText('550M')).toBeTruthy()
        expect(screen.getAllByRole('heading', { level: 3 }).map(h => h.textContent))
            .toEqual(['Templates', 'Retained datasets', 'Downloaded archives'])
        expect(screen.getByText(/12 old snapshots that another tool/)).toBeTruthy()
        // An archive has no endpoint; retained datasets go one at a time.
        expect(within(group('Downloaded archives')).queryByRole('button')).toBeNull()
        expect(within(group('Retained datasets')).queryByRole('button', { name: /Remove all/ })).toBeNull()
        expect(within(group('Templates')).getByRole('button', { name: 'Remove all 2' })).toBeTruthy()
    })

    it('removes a kind one request at a time and shows what was refused', async () => {
        refuse['half'] = new ApiError(409, 'half has clones: web')
        render(Reclaimable)
        await fireEvent.click(await screen.findByRole('button', { name: 'Remove all 2' }))
        await waitFor(() => expect(removed).toHaveLength(2))
        expect(asked[0].items).toEqual(['14.3-RELEASE', 'half'])
        expect(removed).toEqual(['/api/templates/14.3-RELEASE?confirm=14.3-RELEASE', '/api/templates/half?confirm=half'])
        expect(await screen.findByText('half has clones: web')).toBeTruthy()
        expect(screen.queryByText('14.3-RELEASE')).toBeNull()
    })

    it('removes nothing when the confirmation is declined', async () => {
        confirmed = false
        render(Reclaimable)
        await fireEvent.click(await screen.findByRole('button', { name: 'Remove kept' }))
        expect(asked).toHaveLength(1)
        expect(removed).toEqual([])
    })

    it('asks again with the snapshot age it is given', async () => {
        render(Reclaimable)
        const input = await screen.findByLabelText('Snapshot age in days')
        await fireEvent.input(input, { target: { value: '90' } })
        await fireEvent.change(input)
        await waitFor(() => expect(ages).toEqual([30, 90]))
    })

    it('names a source it could not read and still lists the rest', async () => {
        report.problems = ['templates: zfs list: exit 1']
        render(Reclaimable)
        expect(await screen.findByText('Could not read templates: zfs list: exit 1')).toBeTruthy()
        expect(screen.getByText('kept')).toBeTruthy()
    })
})
