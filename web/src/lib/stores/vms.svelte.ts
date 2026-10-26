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
}

export async function listVMs(): Promise<Listing> {
    return await api<Listing>('/vms')
}

export async function getVM(name: string): Promise<Detail> {
    return await api<Detail>(`/vms/${encodeURIComponent(name)}`)
}
