import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import type { Detail, Listing, OperationResult, VMOperation } from '../stores/vms.svelte'
import { ApiError } from '../services/api'
import VMs from './VMs.svelte'
import VM from './VM.svelte'

// vm-bhyve's three answers before any guest, and the two guests
// capture-vm-guests-induced.sh made on isengard (JD-035).

let listing: Listing
let detail: Detail | Error
let operated: { name: string, op: VMOperation }[]
let answer: OperationResult | Error
let confirmed: boolean

vi.mock('../stores/vms.svelte', async (original) => ({
    ...(await original<typeof import('../stores/vms.svelte')>()),
    listVMs: vi.fn(async () => listing),
    getVM: vi.fn(async () => {
        if (detail instanceof Error) throw detail
        return detail
    }),
    runVMOperation: vi.fn(async (name: string, op: VMOperation) => {
        operated.push({ name, op })
        if (answer instanceof Error) throw answer
        return answer
    }),
}))

const asked: string[][] = []
vi.mock('../stores/confirm.svelte', () => ({
    ask: vi.fn(async (c: { title: string, lines?: string[] }) => {
        asked.push([c.title, ...(c.lines ?? [])])
        return confirmed
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
    operated = []
    answer = { name: 'jd-cap-running', datastore: 'default', state: 'stopped', autostart: true }
    confirmed = true
    asked.length = 0
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
        expect(rows[0]).toEqual(['running', 'jd-cap-running', '2', '512M', 'uefi', 'default', 'Yes, #1', 'Stop Restart'])
        expect(rows[1]).toEqual(['stopped', 'jd-cap-stopped', '—', '—', 'uefi', 'default', 'No', 'Start'])
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

describe('starting and stopping a guest', () => {
    // Start is not destructive, so it is not asked about.
    it('starts a stopped guest without asking', async () => {
        render(VMs)
        await fireEvent.click(await screen.findByRole('button', { name: 'Start jd-cap-stopped' }))
        await waitFor(() => expect(operated).toEqual([{ name: 'jd-cap-stopped', op: 'start' }]))
        expect(asked).toHaveLength(0)
    })

    // The deadline is said before the stop, because past it the plug is
    // pulled (JD-036).
    it('asks before a stop, and says the guest may be powered off', async () => {
        render(VMs)
        await fireEvent.click(await screen.findByRole('button', { name: 'Stop jd-cap-running' }))
        await waitFor(() => expect(operated).toEqual([{ name: 'jd-cap-running', op: 'stop' }]))
        expect(asked[0][0]).toBe('Stop jd-cap-running?')
        expect(asked[0].join(' ')).toMatch(/powered off .* anything it had not written to disk is lost/)
    })

    it('runs nothing when the stop is not confirmed', async () => {
        confirmed = false
        render(VMs)
        await fireEvent.click(await screen.findByRole('button', { name: 'Restart jd-cap-running' }))
        await waitFor(() => expect(asked).toHaveLength(1))
        expect(operated).toHaveLength(0)
    })

    it('says so when a stop had to power the guest off', async () => {
        answer = { name: 'jd-cap-running', datastore: 'default', state: 'stopped', autostart: true, forced: true }
        render(VMs)
        await fireEvent.click(await screen.findByRole('button', { name: 'Stop jd-cap-running' }))
        expect(await screen.findByText(/did not answer the power button within a minute and was powered off/)).toBeTruthy()
    })

    it("shows vm-bhyve's refusal under the guest's row", async () => {
        answer = new ApiError(500, 'could not start "jd-cap-stopped": incomplete virtual machine configuration')
        render(VMs)
        await fireEvent.click(await screen.findByRole('button', { name: 'Start jd-cap-stopped' }))
        expect(await screen.findByText(/incomplete virtual machine configuration/)).toBeTruthy()
    })

    // A guest in its boot loader cannot be stopped, and one locked by another
    // host cannot be touched: no button, and the reason instead.
    it('offers nothing for a guest booting or held elsewhere, and says why', async () => {
        listing.vms = [
            { name: 'booting', datastore: 'default', state: 'bootloader', pid: 9, autostart: false },
            { name: 'shared', datastore: 'nfs', state: 'locked', locked_by: 'orthanc', autostart: false },
        ]
        render(VMs)
        expect(await screen.findByText(/Booting — try again once it is running/)).toBeTruthy()
        expect(screen.getByText('Held by orthanc')).toBeTruthy()
        expect(screen.queryAllByRole('button').filter((b) => /Start|Stop|Restart/.test(b.textContent ?? ''))).toHaveLength(0)
    })

    it('offers Resume for a suspended guest', async () => {
        listing.vms = [{ name: 'napping', datastore: 'default', state: 'suspended', autostart: false }]
        render(VMs)
        expect((await screen.findByRole('button', { name: 'Start napping' })).textContent?.trim()).toBe('Resume')
    })

    it("reads the guest's page again after an operation on it", async () => {
        render(VM, { name: 'jd-cap-running' })
        const stop = await screen.findByRole('button', { name: 'Stop jd-cap-running' })
        // What the next read will find.
        detail = { ...running, state: 'stopped', pid: undefined, consoles: undefined, memory_resident: undefined }
        await fireEvent.click(stop)
        expect(await screen.findByText(/None while the guest is not running/)).toBeTruthy()
    })
})
