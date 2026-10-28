import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import type { Created, CreateRequest } from '../stores/vms.svelte'
import type { ImageListing } from '../stores/vmImages.svelte'
import { ApiError } from '../services/api'
import NewVM from './NewVM.svelte'

// The guest and switch from the live check on isengard (JD-038).

let images: ImageListing
let sent: CreateRequest[]
let answer: Created | Error

vi.mock('../stores/vmImages.svelte', () => ({ listImages: vi.fn(async () => images) }))
let defaultNet: unknown
vi.mock('../stores/networks.svelte', () => ({
    getDefaultNetwork: vi.fn(async () => {
        if (defaultNet instanceof Error) throw defaultNet
        return defaultNet
    }),
}))
vi.mock('../stores/vms.svelte', async (original) => ({
    ...(await original<typeof import('../stores/vms.svelte')>()),
    createVM: vi.fn(async (req: CreateRequest) => {
        sent.push(req)
        if (answer instanceof Error) throw answer
        return answer
    }),
}))

const key = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFMz6uwq6uCySEimJJLIKB1qaSuK3GpjZ0BSxasBIfDG jd-live'

beforeEach(() => {
    images = {
        vm_bhyve: 'ready', dataset: 'tank/vm/.images', sources: [],
        images: [
            { name: 'debian-13', source: 'debian-13', ready: true, state: 'ready', size: 3221225472, guests: [] },
            { name: 'half', ready: false, state: 'written', guests: [] },
        ],
    }
    sent = []
    defaultNet = new Error('no default network')
    answer = { name: 'jd-live-web', datastore: 'default', state: 'running', autostart: false, image: 'debian-13' }
})

function form(switches = ['jd-livesw']) {
    const created = vi.fn()
    render(NewVM, { switches, oncreated: created, oncancel: vi.fn() })
    return created
}

async function type(label: string, value: string) {
    await fireEvent.input(screen.getByLabelText(label), { target: { value } })
}

describe('creating a virtual machine', () => {
    it('offers only ready images, and the image size as the disk', async () => {
        form()
        const select = await screen.findByLabelText('Image') as HTMLSelectElement
        expect([...select.options].map(o => o.value)).toEqual(['debian-13'])
        expect((screen.getByLabelText('Disk') as HTMLInputElement).placeholder).toBe('3.0G')
    })

    it('sends a static address, the keys one per line, and the switch', async () => {
        const created = form()
        await screen.findByLabelText('Image')
        await type('Name', ' jd-live-web ')
        await type('CPUs', '2')
        await type('Disk', '10G')
        await fireEvent.click(screen.getByLabelText('Static'))
        await type('Address', '10.78.0.10/24')
        await type('Gateway', '10.78.0.1')
        await type('Nameservers', '10.78.0.1, 9.9.9.9')
        await type('SSH keys', `${key}\n\n  ${key}  \n`)
        await fireEvent.click(screen.getByRole('button', { name: 'Create' }))
        await waitFor(() => expect(created).toHaveBeenCalledWith(answer))
        expect(sent).toEqual([{
            name: 'jd-live-web', image: 'debian-13', cpu: 2, memory: '1G', disk: '10G', start: true,
            switch: 'jd-livesw', ssh_keys: [key, key],
            network: { address: '10.78.0.10/24', gateway: '10.78.0.1', nameservers: ['10.78.0.1', '9.9.9.9'] },
        }])
    })

    it('leaves the disk and the address to the server when not given', async () => {
        form()
        await screen.findByLabelText('Image')
        await type('Name', 'web')
        await fireEvent.click(screen.getByRole('button', { name: 'Create' }))
        await waitFor(() => expect(sent.length).toBe(1))
        expect(sent[0]).toEqual({ name: 'web', image: 'debian-13', cpu: 1, memory: '1G', start: true, ssh_keys: [], switch: 'jd-livesw' })
    })

    it('sends a pin as host CPU numbers', async () => {
        form()
        await screen.findByLabelText('Image')
        await type('Name', 'web')
        await type('CPUs', '2')
        await type('Pin to CPUs', ' 2, 3 ')
        await fireEvent.click(screen.getByRole('button', { name: 'Create' }))
        await waitFor(() => expect(sent.length).toBe(1))
        expect(sent[0].cpuset).toEqual([2, 3])
    })

    it('says what a pin is instead of sending one that is not numbers', async () => {
        form()
        await screen.findByLabelText('Image')
        await type('Name', 'web')
        await type('Pin to CPUs', '2-3')
        await fireEvent.click(screen.getByRole('button', { name: 'Create' }))
        expect(await screen.findByText(/Pin to host CPUs is their numbers/)).toBeTruthy()
        expect(sent).toEqual([])
        // And the form is not left busy.
        expect((screen.getByRole('button', { name: 'Create' }) as HTMLButtonElement).disabled).toBe(false)
    })

    it('sends no switch for no network, and says so when there are none', async () => {
        form([])
        await screen.findByLabelText('Image')
        expect(screen.getByText(/has no switches, so a guest made now has no network/)).toBeTruthy()
        expect(screen.queryByText('Static')).toBeNull()
        await type('Name', 'web')
        await fireEvent.click(screen.getByLabelText('Start it once it is made'))
        await fireEvent.click(screen.getByRole('button', { name: 'Create' }))
        await waitFor(() => expect(sent.length).toBe(1))
        expect(sent[0].switch).toBeUndefined()
        expect(sent[0].network).toBeUndefined()
        expect(sent[0].start).toBe(false)
    })

    it('says a guest without a key is reachable only on its console', async () => {
        form()
        expect(await screen.findByText(/without a key the guest is reachable only on its console/)).toBeTruthy()
    })

    it("shows the server's refusal and keeps the form", async () => {
        answer = new ApiError(400, 'vm-bhyve has no switch named "public" (it has jd-livesw)')
        const created = form()
        await screen.findByLabelText('Image')
        await type('Name', 'web')
        await fireEvent.click(screen.getByRole('button', { name: 'Create' }))
        expect((await screen.findByRole('alert')).textContent).toContain('no switch named')
        expect(created).not.toHaveBeenCalled()
    })

    it('offers the default network first, with no DHCP and the host as gateway', async () => {
        defaultNet = { switch: 'jaildeck', address: '10.88.0.1/24', subnet: '10.88.0.0/24', gateway: '10.88.0.1',
            usable: true, ready: true, parts: [] }
        form(['jd-livesw', 'jaildeck'])
        const net = await screen.findByLabelText('Network') as HTMLSelectElement
        await waitFor(() => expect(net.value).toBe('jaildeck'))
        expect(net.options[0].textContent).toBe('on the default network (jaildeck)')
        expect(screen.queryByLabelText('DHCP')).toBeNull()
        expect((screen.getByLabelText('Gateway') as HTMLInputElement).value).toBe('10.88.0.1')
        expect(screen.getByText(/The default network has no DHCP/)).toBeTruthy()
        expect((screen.getByLabelText('Address') as HTMLInputElement).placeholder).toBe('10.88.0.10/24')
        await type('Name', 'web')
        await type('Address', '10.88.0.10/24')
        await fireEvent.click(screen.getByRole('button', { name: 'Create' }))
        await waitFor(() => expect(sent.length).toBe(1))
        expect(sent[0].switch).toBe('jaildeck')
        expect(sent[0].network).toEqual({ address: '10.88.0.10/24', gateway: '10.88.0.1' })
    })

    it('points at the images when none is ready', async () => {
        images.images = [images.images[1]]
        form()
        expect(await screen.findByText(/no ready image/)).toBeTruthy()
        expect(screen.queryByRole('button', { name: 'Create' })).toBeNull()
    })
})
