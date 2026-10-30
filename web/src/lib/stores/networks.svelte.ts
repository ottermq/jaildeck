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

// An address of a Jail Deck network's subnet that something has (JD-047):
// a jail's or a guest's by its configuration, or one the bridge has only seen.
export interface AddressUse {
    address: string
    kind: 'jail' | 'guest' | 'seen'
    name?: string
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
    // null when unreadable, or the switch has no address yet.
    addresses?: AddressUse[] | null
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

// A tenant network (JD-043): GET /api/networks.
export interface TenantNetwork {
    name: string
    tenant?: string
    bridge: string
    subnet: string
    gateway: string
    members: string[]
    addresses?: AddressUse[] | null
}

export interface NetworkList {
    range: string
    networks: TenantNetwork[]
}

export async function listNetworks(): Promise<NetworkList> {
    return await api<NetworkList>('/networks')
}

// OwnNetwork is one of Jail Deck's networks as a create form offers it: the
// ones whose addresses Jail Deck gives (JD-047).
export interface OwnNetwork {
    name: string
    bridge: string
    switch: string
    subnet: string
    gateway: string
}

// ownNetworks reads the default network, when it is usable, and every tenant
// network. Either that cannot be read is left out.
export async function ownNetworks(): Promise<OwnNetwork[]> {
    const [def, tenants] = await Promise.allSettled([getDefaultNetwork(), listNetworks()])
    const own: OwnNetwork[] = []
    if (def.status === 'fulfilled' && def.value.usable) {
        own.push({ name: 'default', bridge: `vm-${def.value.switch}`, switch: def.value.switch,
            subnet: def.value.subnet, gateway: def.value.gateway })
    }
    if (tenants.status === 'fulfilled') {
        for (const n of tenants.value.networks) {
            own.push({ name: n.name, bridge: n.bridge, switch: n.name, subnet: n.subnet, gateway: n.gateway })
        }
    }
    return own
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
    // A Jail Deck network's addresses in use (JD-047).
    in_use?: AddressUse[]
}

export interface Topology {
    bridges: TopologyBridge[]
}

export async function getTopology(): Promise<Topology> {
    return api<Topology>('/topology')
}
