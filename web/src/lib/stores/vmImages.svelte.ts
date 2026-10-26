import { api } from '../services/api'
import type { VMBhyveState } from './vms.svelte'

// A VM image template: a cloud image's disk in a zvol, sealed with an @base
// snapshot that guests' disks are cloned from (JD-037). Nothing is stored by
// Jail Deck: ready is whether @base exists, and source and sha512 are user
// properties on the zvol.

// What the server is doing for an image right now. Held in its memory, never
// written down (JD-008).
export interface ImageRunning {
    source: string
    step: string
    since: string
}

export type ImageState = 'fetching' | 'writing' | 'written' | 'ready'

export interface Image {
    name: string
    // The catalog entry it was written from; absent for a zvol Jail Deck
    // did not write.
    source?: string
    dataset?: string
    ready: boolean
    snapshot?: string
    // written is a zvol with no @base: a write that stopped.
    state: ImageState
    running?: ImageRunning
    // The image's disk, and the smallest a guest cloned from it can have.
    size?: number
    // What it costs the pool: sparse and compressed.
    used?: number
    // The checksum of the archive it was written from, which names the build.
    sha512?: string
    // Guests whose disk is a clone of @base. Present and empty, not absent.
    guests: string[]
}

// A source the server can prepare an image from.
export interface ImageSource {
    name: string
    description: string
    url: string
    // Its verified archive is already on the host, so preparing starts at
    // the write.
    fetched: boolean
}

export interface ImageListing {
    vm_bhyve: VMBhyveState
    // Why images cannot be prepared here, when they cannot.
    problem?: string
    dataset?: string
    images: Image[]
    sources: ImageSource[]
}

export interface ImageRemoval {
    name: string
    dataset: string
}

export async function listImages(): Promise<ImageListing> {
    return await api<ImageListing>('/vm-images')
}

// prepareImage answers at once, 202 with the state the work begins in, or 200
// with an image that was already ready; the image is then polled (JD-008).
export async function prepareImage(name: string, source: string): Promise<Image> {
    return await api<Image>(`/vm-images/${encodeURIComponent(name)}`, {
        method: 'PUT',
        body: JSON.stringify({ source }),
    })
}

// A refusal -- a guest's disk is cloned from it -- is a 409 with a message
// naming the guests, and arrives as an ApiError like any other.
export async function removeImage(name: string): Promise<ImageRemoval> {
    const query = new URLSearchParams({ confirm: name })
    return await api<ImageRemoval>(`/vm-images/${encodeURIComponent(name)}?${query}`, { method: 'DELETE' })
}
