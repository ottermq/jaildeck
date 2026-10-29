import { api } from '../services/api'

// The default VM network (JD-040): a private vm-bhyve switch Jail Deck owns,
// the host's address on it as the guests' gateway, and outbound NAT through
// a PF anchor. Each part is read off the host on every request (JD-011).

export type PartStatus = 'pass' | 'fail' | 'unknown'

export interface Part {
    id: string
    title: string
    status: PartStatus
    // Who puts it in place: Jail Deck, on PUT, or the operator, with remedy.
    owner: 'jaildeck' | 'operator'
    observed?: string
    detail?: string
    remedy?: string
}

export interface DefaultNetwork {
    switch: string
    address: string
    subnet: string
    gateway: string
    egress?: string
    // A guest on it reaches the host and no other guest.
    usable: boolean
    // Every part passes, NAT included.
    ready: boolean
    parts: Part[]
}

export interface Ensured extends DefaultNetwork {
    changed: boolean
    done: string[]
    note?: string
}

export async function getDefaultNetwork(): Promise<DefaultNetwork> {
    return await api<DefaultNetwork>('/networks/default')
}

export async function ensureDefaultNetwork(): Promise<Ensured> {
    return await api<Ensured>('/networks/default', { method: 'PUT' })
}

// Every bridge and vm-bhyve switch, and what is on each (#128): GET /api/topology.
export interface TopologyMember {
    interface: string
    kind: 'jail' | 'vm' | 'other'
    instance?: string
    nic?: string
    private: boolean
}

export interface TopologyBridge {
    // Empty, with exists false, for a switch whose bridge is not made yet.
    name: string
    exists: boolean
    kind: 'default' | 'tenant' | 'operator'
    switch?: string
    switch_type?: string
    network?: string
    tenant?: string
    description?: string
    addresses: string[]
    nat: 'loaded' | 'not-loaded' | 'unknown'
    members: TopologyMember[]
}

export interface Topology {
    bridges: TopologyBridge[]
}

export async function getTopology(): Promise<Topology> {
    return api<Topology>('/topology')
}
