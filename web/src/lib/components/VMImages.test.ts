import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import type { Image, ImageListing } from '../stores/vmImages.svelte'
import { ApiError } from '../services/api'
import VMImages from './VMImages.svelte'

// The image and the guest cloned from it are the ones the live check made on
// isengard (JD-037); the build is the archive it fetched.

let listing: ImageListing
let prepared: { name: string, source: string }[]
let prepareAnswer: Image | Error
let removed: string[]
let removeAnswer: Error | null
let confirmed: boolean
const asked: { title: string, typed?: string }[] = []

const listImages = vi.fn(async () => structuredClone(listing))
vi.mock('../stores/vmImages.svelte', () => ({
    listImages: () => listImages(),
    prepareImage: vi.fn(async (name: string, source: string) => {
        prepared.push({ name, source })
        if (prepareAnswer instanceof Error) throw prepareAnswer
        return prepareAnswer
    }),
    removeImage: vi.fn(async (name: string) => {
        removed.push(name)
        if (removeAnswer) throw removeAnswer
        return { name, dataset: `tank/vm/.images/${name}` }
    }),
}))

vi.mock('../stores/confirm.svelte', () => ({
    ask: vi.fn(async (c: { title: string, typed?: string }) => {
        asked.push(c)
        return confirmed
    }),
}))

const sha = '893deb857d128c3f' + '0'.repeat(112)

const ready: Image = {
    name: 'jd-live-debian13', source: 'debian-13', dataset: 'tank/vm/.images/jd-live-debian13',
    ready: true, snapshot: 'tank/vm/.images/jd-live-debian13@base', state: 'ready',
    size: 3221225472, used: 567754752, sha512: sha, guests: ['jd-live'],
}

const debian = {
    name: 'debian-13', description: 'Debian 13 (trixie), genericcloud, amd64, with cloud-init',
    url: 'https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.tar.xz',
    fetched: false,
}

beforeEach(() => {
    listing = { vm_bhyve: 'ready', dataset: 'tank/vm/.images', images: [ready], sources: [{ ...debian }] }
    prepared = []
    prepareAnswer = {
        name: 'debian-13', source: 'debian-13', ready: false, state: 'fetching', guests: [],
        running: { source: 'debian-13', step: 'fetching debian-13', since: new Date().toISOString() },
    }
    removed = []
    removeAnswer = null
    confirmed = true
    asked.length = 0
    listImages.mockClear()
})

afterEach(() => {
    vi.useRealTimers()
})

describe('the images panel', () => {
    it('lists each image with its state, its build, its sizes and the guests cloned from it', async () => {
        listing.images.push({ name: 'half', source: 'debian-13', ready: false, state: 'written', guests: [], size: 3221225472 })
        render(VMImages)
        expect(await screen.findByText('jd-live-debian13')).toBeTruthy()
        expect(screen.getByText('893deb857d12')).toBeTruthy()
        // As zfs prints them: the disk, and what it costs the pool.
        expect(screen.getAllByText('3.0G').length).toBe(2)
        expect(screen.getByText('541M')).toBeTruthy()
        expect(screen.getByRole('link', { name: 'jd-live' }).getAttribute('href')).toBe('/vms/jd-live')
        expect(screen.getByText('written', { selector: '.badge' })).toBeTruthy()
    })

    it('says why images cannot be prepared, and offers nothing', async () => {
        listing = {
            vm_bhyve: 'ready', images: [], sources: [{ ...debian }],
            problem: "vm-bhyve's default datastore, /vm, is not on ZFS; an image is a zvol, so it needs one that is",
        }
        render(VMImages)
        expect(await screen.findByText(/is not on ZFS/)).toBeTruthy()
        expect(screen.queryByRole('button', { name: 'Prepare an image' })).toBeNull()
    })

    it('says there are none yet', async () => {
        listing.images = []
        render(VMImages)
        expect(await screen.findByText(/No images yet/)).toBeTruthy()
    })
})

describe('preparing an image', () => {
    it('names the first one after its source and says what it will download', async () => {
        render(VMImages)
        await fireEvent.click(await screen.findByRole('button', { name: 'Prepare an image' }))
        expect((screen.getByLabelText('Name') as HTMLInputElement).value).toBe('debian-13')
        expect(screen.getByText(debian.url)).toBeTruthy()

        await fireEvent.input(screen.getByLabelText('Name'), { target: { value: ' trixie ' } })
        prepareAnswer = { ...prepareAnswer as Image, name: 'trixie' }
        listing.images.push(prepareAnswer)
        await fireEvent.click(screen.getByRole('button', { name: 'Prepare' }))
        await waitFor(() => expect(prepared).toEqual([{ name: 'trixie', source: 'debian-13' }]))
        expect(await screen.findByText(/It keeps going if you leave this page/)).toBeTruthy()
    })

    it('says an archive already on the host is only written', async () => {
        listing.sources[0].fetched = true
        render(VMImages)
        await fireEvent.click(await screen.findByRole('button', { name: 'Prepare an image' }))
        expect(screen.getByText(/already on this host/)).toBeTruthy()
        expect(screen.queryByText(debian.url)).toBeNull()
    })

    it("shows the server's refusal in the form", async () => {
        prepareAnswer = new ApiError(409, 'jd-live-debian13 already has a zvol and no @base snapshot')
        render(VMImages)
        await fireEvent.click(await screen.findByRole('button', { name: 'Prepare an image' }))
        await fireEvent.click(screen.getByRole('button', { name: 'Prepare' }))
        expect((await screen.findByRole('alert')).textContent).toContain('no @base snapshot')
    })

    it('reads the listing again while something runs, and stops when nothing does', async () => {
        vi.useFakeTimers()
        listing.images = [{ ...prepareAnswer as Image }]
        render(VMImages)
        await vi.waitFor(() => expect(screen.getByText(/fetching debian-13 ·/)).toBeTruthy())

        const before = listImages.mock.calls.length
        await vi.advanceTimersByTimeAsync(2000)
        expect(listImages.mock.calls.length).toBe(before + 1)

        listing.images = [ready]
        await vi.advanceTimersByTimeAsync(2000)
        const settled = listImages.mock.calls.length
        await vi.advanceTimersByTimeAsync(10000)
        expect(listImages.mock.calls.length).toBe(settled)
    })
})

describe('how a preparation came out', () => {
    async function prepareAndSettle(after: Image) {
        vi.useFakeTimers()
        listing.images = []
        render(VMImages)
        await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Prepare an image' })).toBeTruthy())
        await fireEvent.click(screen.getByRole('button', { name: 'Prepare an image' }))
        listing.images = [{ ...prepareAnswer as Image }]
        await fireEvent.click(screen.getByRole('button', { name: 'Prepare' }))
        await vi.waitFor(() => expect(screen.getByText(/It keeps going/)).toBeTruthy())
        listing.images = [after]
        await vi.advanceTimersByTimeAsync(2000)
    }

    it('says so when the image it started is ready', async () => {
        await prepareAndSettle({ ...ready, name: 'debian-13', guests: [] })
        expect(screen.getByText('debian-13 is ready.')).toBeTruthy()
        expect(screen.queryByText(/It keeps going/)).toBeNull()
    })

    it('says so, and where to look, when it stopped short', async () => {
        await prepareAndSettle({ name: 'debian-13', source: 'debian-13', ready: false, state: 'written', guests: [] })
        expect(screen.getByText(/debian-13 stopped before it was ready, at written/)).toBeTruthy()
        expect(screen.getByRole('link', { name: 'Operations' }).getAttribute('href')).toBe('/operations')
        expect(screen.queryByText(/It keeps going/)).toBeNull()
    })
})

describe('removing an image', () => {
    it('asks for the name typed, then removes it', async () => {
        render(VMImages)
        await fireEvent.click(await screen.findByRole('button', { name: 'Remove' }))
        await waitFor(() => expect(removed).toEqual(['jd-live-debian13']))
        expect(asked[0].typed).toBe('jd-live-debian13')
        expect(await screen.findByText('Removed jd-live-debian13.')).toBeTruthy()
    })

    it('removes nothing when not confirmed', async () => {
        confirmed = false
        render(VMImages)
        await fireEvent.click(await screen.findByRole('button', { name: 'Remove' }))
        await waitFor(() => expect(asked.length).toBe(1))
        expect(removed).toEqual([])
    })

    it("shows the refusal naming the guest under the image's row", async () => {
        removeAnswer = new ApiError(409, 'jd-live-debian13 is cloned for the disk of jd-live; destroy the guests first')
        render(VMImages)
        await fireEvent.click(await screen.findByRole('button', { name: 'Remove' }))
        expect((await screen.findByRole('alert')).textContent).toContain('the disk of jd-live')
    })

    it('cannot remove an image while it is being prepared', async () => {
        listing.images = [{ ...prepareAnswer as Image }]
        render(VMImages)
        const button = await screen.findByRole('button', { name: 'Remove' })
        expect((button as HTMLButtonElement).disabled).toBe(true)
    })
})
