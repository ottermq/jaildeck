import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import type { DefaultNetwork, Ensured } from '../stores/networks.svelte'
import { ApiError } from '../services/api'
import VMNetwork from './VMNetwork.svelte'

// A fresh host as the fake and the measurements have it: no switch, no
// forwarding, PF not loaded (JD-040).

let net: DefaultNetwork
let ensured: Ensured | Error
let ensureCalls: number

vi.mock('../stores/networks.svelte', () => ({
    getDefaultNetwork: vi.fn(async () => structuredClone(net)),
    ensureDefaultNetwork: vi.fn(async () => {
        ensureCalls++
        if (ensured instanceof Error) throw ensured
        return ensured
    }),
}))

const pfRemedy = 'nat-anchor "jaildeck/*"\nanchor "jaildeck/*"\nload anchor "jaildeck/vmnet" from "/usr/local/etc/jaildeck/pf/vmnet.conf"\n# then: service pf reload'

function fresh(): DefaultNetwork {
    return {
        switch: 'jaildeck', address: '10.88.0.1/24', subnet: '10.88.0.0/24', gateway: '10.88.0.1', egress: 'em0',
        usable: false, ready: false,
        parts: [
            { id: 'switch', title: 'A vm-bhyve switch for the network', status: 'fail', owner: 'jaildeck', detail: 'There is no switch named jaildeck.' },
            { id: 'forwarding', title: 'IP forwarding', status: 'fail', owner: 'jaildeck', observed: '0' },
            { id: 'pf', title: 'PF enabled', status: 'fail', owner: 'operator', observed: 'not loaded', remedy: 'sysrc pf_enable=YES && service pf start' },
            { id: 'pf_rules', title: 'pf.conf includes Jail Deck\'s anchors', status: 'fail', owner: 'operator', remedy: pfRemedy },
        ],
    }
}

beforeEach(() => {
    net = fresh()
    ensured = {
        ...fresh(), usable: true, changed: true,
        done: ['switch', 'address', 'private', 'forwarding', 'forwarding_at_boot', 'anchor_file'],
        note: 'Private ports apply to guests as they next start.',
        parts: fresh().parts.filter(p => p.owner === 'operator'),
    }
    ensureCalls = 0
})

describe('the default network panel', () => {
    it('lists what is missing, says whose each part is, and gives the operator its remedy', async () => {
        render(VMNetwork)
        expect(await screen.findByText('not set up')).toBeTruthy()
        expect(screen.getByText('There is no switch named jaildeck.')).toBeTruthy()
        expect(screen.getAllByText('Jail Deck').length).toBe(2)
        expect(screen.getAllByText('you').length).toBe(2)
        expect(screen.getByText('sysrc pf_enable=YES && service pf start')).toBeTruthy()
        expect(screen.getByText(/load anchor "jaildeck\/vmnet"/)).toBeTruthy()
    })

    it("sets up Jail Deck's parts, says what it did, and tells the page", async () => {
        const changed = vi.fn()
        render(VMNetwork, { onchanged: changed })
        await fireEvent.click(await screen.findByRole('button', { name: 'Set up' }))
        await waitFor(() => expect(ensureCalls).toBe(1))
        expect(await screen.findByText(/Done: switch, address, private/)).toBeTruthy()
        expect(screen.getByText(/as they next start/)).toBeTruthy()
        expect(screen.getByText('host only')).toBeTruthy()
        expect(changed).toHaveBeenCalled()
        // Only the operator's parts are left, and nothing more for the button to do.
        expect(screen.queryByRole('button', { name: 'Set up' })).toBeNull()
    })

    it('offers no button when only the operator has something left to do', async () => {
        net = { ...fresh(), usable: true, parts: fresh().parts.filter(p => p.owner === 'operator') }
        render(VMNetwork)
        expect(await screen.findByText('host only')).toBeTruthy()
        expect(screen.queryByRole('button', { name: 'Set up' })).toBeNull()
    })

    it('is one line when ready, with the parts behind a toggle', async () => {
        net = { ...fresh(), usable: true, ready: true, parts: fresh().parts.map(p => ({ ...p, status: 'pass' as const, remedy: undefined })) }
        render(VMNetwork)
        expect(await screen.findByText('ready')).toBeTruthy()
        expect(screen.queryByText('IP forwarding')).toBeNull()
        await fireEvent.click(screen.getByRole('button', { name: 'Show the parts' }))
        expect(screen.getByText('IP forwarding')).toBeTruthy()
    })

    it("shows the server's refusal", async () => {
        ensured = new ApiError(409, 'A switch named jaildeck exists and is a manual switch')
        render(VMNetwork)
        await fireEvent.click(await screen.findByRole('button', { name: 'Set up' }))
        expect((await screen.findByRole('alert')).textContent).toContain('manual switch')
    })
})
