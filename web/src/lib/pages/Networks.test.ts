import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/svelte'
import type { Topology } from '../stores/networks.svelte'
import { ApiError } from '../services/api'
import Networks from './Networks.svelte'

// The shape capture-topology-induced.sh read on isengard (#128): the default
// network with a guest on it, a tenant network, and a switch not made yet.

let topology: Topology | Error

vi.mock('../stores/networks.svelte', async (original) => ({
    ...(await original<typeof import('../stores/networks.svelte')>()),
    getTopology: vi.fn(async () => {
        if (topology instanceof Error) throw topology
        return topology
    }),
}))

beforeEach(() => {
    topology = {
        bridges: [
            {
                name: 'vm-jaildeck', exists: true, kind: 'default', switch: 'jaildeck', switch_type: 'standard',
                addresses: ['10.88.0.1/24'], nat: 'loaded',
                members: [
                    { interface: 'tap0', kind: 'vm', instance: 'web', nic: '0', private: true },
                    { interface: 'epair0a', kind: 'jail', instance: 'nginx', private: false },
                    { interface: 'em1', kind: 'other', private: false },
                ],
            },
            {
                name: 'jdn0', exists: true, kind: 'tenant', switch: 'jdn-acme', switch_type: 'manual',
                network: 'acme', tenant: 'acme', addresses: ['10.89.0.1/24'], nat: 'not-loaded', members: [],
            },
            {
                name: '', exists: false, kind: 'operator', switch: 'vmlan', switch_type: 'standard',
                addresses: [], nat: 'unknown', members: [],
            },
        ],
    }
})

describe('the networks page', () => {
    it('shows each network, what is on it, and links the instances', async () => {
        render(Networks)
        const def = await screen.findByRole('region', { name: 'jaildeck' })
        expect(def.textContent).toContain('default network')
        expect(def.textContent).toContain('10.88.0.1/24')
        expect(screen.getByRole('link', { name: 'web' }).getAttribute('href')).toBe('/vms/web')
        expect(screen.getByRole('link', { name: 'nginx' }).getAttribute('href')).toBe('/jails/nginx')
        expect(def.textContent).toContain("not a jail's or a guest's")
        expect(def.textContent).toContain('NIC 0')
    })

    it('names a tenant network by its name and says nothing is on it', async () => {
        render(Networks)
        const acme = await screen.findByRole('region', { name: 'acme' })
        expect(acme.textContent).toContain('tenant network')
        expect(acme.textContent).toContain('no NAT')
        expect(acme.textContent).toContain('Nothing is on it.')
    })

    it('says a switch with no bridge gets one at vm init', async () => {
        render(Networks)
        const lan = await screen.findByRole('region', { name: 'vmlan' })
        expect(lan.textContent).toContain('vm init')
        expect(lan.textContent).toContain('NAT unknown')
    })

    it('says so when the host has no networks', async () => {
        topology = { bridges: [] }
        render(Networks)
        expect(await screen.findByText(/no bridges and no vm-bhyve switches/)).toBeTruthy()
    })

    it("shows the server's refusal, with a retry", async () => {
        topology = new ApiError(500, 'ifconfig failed')
        render(Networks)
        await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('ifconfig failed'))
        expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy()
    })
})
