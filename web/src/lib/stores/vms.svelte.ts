import { api } from '../services/api'

// What vm-bhyve says about itself before it lists anything (JD-035). None is
// an error: a host without vm-bhyve is the common case.
export type VMBhyveState = 'ready' | 'not_installed' | 'not_configured'

// vm-bhyve's five states, lower-cased, and no others.
export type VMState = 'running' | 'bootloader' | 'stopped' | 'suspended' | 'locked'

// One row of `vm list`. cpu and memory are the guest's configuration as
// written ("2", "512M"), not re-parsed -- and absent when vm-bhyve's table
// could not say which of the two was missing.
export interface VM {
    name: string
    datastore: string
    loader?: string
    cpu?: string
    memory?: string
    vnc?: string
    state: VMState
    // bhyve's, or the boot loader's while state is bootloader.
    pid?: number
    // The host whose run.lock holds the guest, in a shared datastore.
    locked_by?: string
    // In rc.conf's vm_list, which rc.d/vm starts at boot, at this place.
    autostart: boolean
    autostart_order?: number
}

export interface Listing {
    vm_bhyve: VMBhyveState
    // vm-bhyve's own words when it is not configured.
    problem?: string
    vms: VM[]
    // vm-bhyve's switches, which a created guest's NIC can be on.
    switches: string[]
}

export interface Network {
    number: number
    emulation?: string
    switch?: string
    mac?: string
    // The tap of the current run: a new one on every start.
    device?: string
    bridge?: string
    bytes_in?: number
    bytes_out?: number
}

export interface Disk {
    number: number
    device_type?: string
    emulation?: string
    path?: string
    size?: number
    used?: number
}

export interface Detail extends VM {
    uuid?: string
    // Only while the guest's bhyve instance exists.
    memory_resident?: number
    // "com1" -> "/dev/nmdm-<name>.1B", the host's end. Running guests only.
    consoles?: Record<string, string>
    networks: Network[]
    disks: Disk[]
    // The host CPU each vCPU is pinned to, vCPU 0 first (JD-041).
    cpuset?: number[]
}

export async function listVMs(): Promise<Listing> {
    return await api<Listing>('/vms')
}

export async function getVM(name: string): Promise<Detail> {
    return await api<Detail>(`/vms/${encodeURIComponent(name)}`)
}

export type VMOperation = 'start' | 'stop' | 'restart'

// What a start, stop or restart answers: the guest as vm-bhyve lists it
// afterwards.
export interface OperationResult extends VM {
    // The guest ignored the power button for 60 seconds and was powered off
    // -- the plug pulled (JD-036). A restart says so too, though the guest
    // is running again.
    forced?: boolean
}

export async function runVMOperation(name: string, op: VMOperation): Promise<OperationResult> {
    return await api<OperationResult>(`/vms/${encodeURIComponent(name)}/${op}`, { method: 'POST' })
}

// A guest made from an image template (JD-038). Empty fields take the
// server's defaults: one CPU, 1G of memory, the image's own disk size, no NIC,
// DHCP when there is one.
export interface CreateRequest {
    name: string
    image: string
    cpu?: number
    // One host CPU per vCPU, vCPU 0 first. Absent leaves the guest unpinned.
    cpuset?: number[]
    memory?: string
    disk?: string
    switch?: string
    network?: { address: string, gateway?: string, nameservers?: string[] }
    ssh_keys?: string[]
    console_password?: string
    start?: boolean
}

export interface Created extends VM {
    image: string
}

export async function createVM(req: CreateRequest): Promise<Created> {
    return await api<Created>('/vms', { method: 'POST', body: JSON.stringify(req) })
}

// Destroys a stopped guest and its disk. confirm is its name.
export async function destroyVM(name: string): Promise<{ name: string }> {
    const query = new URLSearchParams({ confirm: name })
    return await api<{ name: string }>(`/vms/${encodeURIComponent(name)}?${query}`, { method: 'DELETE' })
}
