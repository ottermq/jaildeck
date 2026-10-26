import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/svelte'
import type { Detail, Listing } from '../stores/vms.svelte'
import { ApiError } from '../services/api'
import VMs from './VMs.svelte'
import VM from './VM.svelte'

// vm-bhyve's three answers before any guest, and the two guests
// capture-vm-guests-induced.sh made on isengard (JD-035).

let listing: Listing
let detail: Detail | Error

vi.mock('../stores/vms.svelte', async (original) => ({
    ...(await original<typeof import('../stores/vms.svelte')>()),
    listVMs: vi.fn(async () => listing),
    getVM: vi.fn(async () => {
        if (detail instanceof Error) throw detail
        return detail
    }),
}))

const running: Detail = {
    name: 'jd-cap-running', datastore: 'default', loader: 'uefi', cpu: '2', memory: '512M',
    state: 'running', pid: 16783, autostart: true, autostart_order: 1,
    uuid: 'ed117915-d146-11f1-b463-e80688cecc46',
    memory_resident: 41967616,
    consoles: { com1: '/dev/nmdm-jd-cap-running.1B' },
    networks: [{
        number: 0, emulation: 'virtio-net', switch: 'jd-capsw', mac: '58:9c:fc:02:fa:82',
        device: 'tap0', bridge: 'vm-jd-capsw', bytes_in: 0, bytes_out: 553,
    }],
    disks: [{
        number: 0, device_type: 'sparse-zvol', emulation: 'virtio-blk',
        path: '/dev/zvol/tank/vm/jd-cap-running/disk0', size: 1073741824, used: 57344,
    }],
}

beforeEach(() => {
    listing = {
        vm_bhyve: 'ready',
        vms: [
            running,
            { name: 'jd-cap-stopped', datastore: 'default', loader: 'uefi', state: 'stopped', autostart: false },
        ],
    }
    detail = running
})

describe('the virtual machines page', () => {
    // The common case, and not an error.
    it('says vm-bhyve is not installed without alarm, and points at Host Setup', async () => {
        listing = { vm_bhyve: 'not_installed', vms: [] }
        render(VMs)
        expect(await screen.findByText(/vm-bhyve is not installed/)).toBeTruthy()
        expect(screen.getByRole('link', { name: 'Host Setup' }).getAttribute('href')).toBe('/host')
        expect(screen.queryByRole('alert')).toBeNull()
        expect(screen.queryByRole('table')).toBeNull()
    })

    it("gives vm-bhyve's own words when it is not configured", async () => {
        listing = { vm_bhyve: 'not_configured', problem: '$vm_enable is not enabled in /etc/rc.conf!', vms: [] }
        render(VMs)
        expect(await screen.findByText('$vm_enable is not enabled in /etc/rc.conf!')).toBeTruthy()
        expect(screen.queryByRole('alert')).toBeNull()
    })

    it('says a ready vm-bhyve with no guests has none', async () => {
        listing = { vm_bhyve: 'ready', vms: [] }
        render(VMs)
        expect(await screen.findByText(/has no guests/)).toBeTruthy()
    })

    // CPU and memory unknown -- vm-bhyve's table could not say which was
    // missing -- are a dash, never a blank cell or "undefined".
    it('lists each guest with its state, boot order and a dash for what is unknown', async () => {
        render(VMs)
        const link = await screen.findByRole('link', { name: 'jd-cap-running' })
        expect(link.getAttribute('href')).toBe('/vms/jd-cap-running')
        const rows = screen.getAllByRole('row').slice(1).map((r) =>
            [...r.querySelectorAll('td')].map((td) => (td.textContent ?? '').replace(/\s+/g, ' ').trim()))
        expect(rows[0]).toEqual(['running', 'jd-cap-running', '2', '512M', 'uefi', 'default', 'Yes, #1'])
        expect(rows[1]).toEqual(['stopped', 'jd-cap-stopped', '—', '—', 'uefi', 'default', 'No'])
    })

    it('names the host holding a locked guest', async () => {
        listing.vms = [{ name: 'shared', datastore: 'nfs', state: 'locked', locked_by: 'orthanc', autostart: false }]
        render(VMs)
        expect(await screen.findByText('by orthanc')).toBeTruthy()
    })
})

describe('one virtual machine', () => {
    it('shows the console, the network with its tap, and the disk in bytes a person reads', async () => {
        render(VM, { name: 'jd-cap-running' })
        expect(await screen.findByText('/dev/nmdm-jd-cap-running.1B')).toBeTruthy()
        expect(screen.getByText('40M resident')).toBeTruthy()
        expect(screen.getByText('tap0')).toBeTruthy()
        expect(screen.getByText('1.0G')).toBeTruthy()
        expect(screen.getByText(/#1 in vm_list/)).toBeTruthy()
    })

    // A stopped guest has no bhyve instance, so no console and no resident
    // memory: said, not left as an empty section.
    it('explains the missing console of a stopped guest', async () => {
        detail = {
            name: 'jd-cap-stopped', datastore: 'default', loader: 'uefi', cpu: '1', memory: '256M',
            state: 'stopped', autostart: false, networks: [], disks: [],
        }
        render(VM, { name: 'jd-cap-stopped' })
        expect(await screen.findByText(/None while the guest is not running/)).toBeTruthy()
        expect(screen.getByText('No network interface is configured.')).toBeTruthy()
        expect(screen.queryByText(/resident/)).toBeNull()
    })

    it("shows the server's 404 as it said it", async () => {
        detail = new ApiError(404, 'vm-bhyve has no virtual machine named "gone"')
        render(VM, { name: 'gone' })
        const alert = await screen.findByRole('alert')
        expect(alert.textContent).toMatch(/vm-bhyve has no virtual machine named "gone"/)
    })
})
