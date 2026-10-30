import { api } from '../services/api'

// Ticks are kern.cp_time, cumulative since boot. They are always present,
// so a client that keeps its own baseline is not waiting on the server's.
export interface Ticks {
    user: number
    nice: number
    system: number
    interrupt: number
    idle: number
}

export interface CPU {
    cpus: number
    ticks: Ticks
    // Absent on the first reading after a restart: kern.cp_time is cumulative
    // and a percentage exists only between two readings, so zero would be a
    // lie (JD-031).
    percent?: number
    // How long the interval behind `percent` actually was, so a reading five
    // seconds after the last one and one five minutes after are told apart.
    over_seconds?: number
}

export interface LoadAverage {
    one: number
    five: number
    fifteen: number
}

// A breakdown rather than one "used" figure: on a ZFS host most of `wired` is
// the ARC, which is reclaimable, and a single number would read as pressure
// that is not there.
export interface Memory {
    physical: number
    free: number
    active: number
    inactive: number
    wired: number
}

export interface Pool {
    name: string
    size: number
    allocated: number
    free: number
    capacity: number
    health: string
}

export interface HostMetrics {
    load: LoadAverage
    cpu: CPU
    memory: Memory
    pools: Pool[]
}

// Where the usage was read from. `rctl` answers more than `ps` does, which is
// why the field exists: a missing `vmemoryuse` is explained rather than
// guessed at.
export type UsageSource = 'rctl' | 'ps'

export interface JailUsage {
    jail: string
    source: UsageSource
    // Percent of ONE core, so a jail using two whole cores reads 200.
    pcpu: number
    memoryuse: number
    processes: number
    vmemoryuse?: number
    openfiles?: number
    cputime?: number
    // This one jail could not be read; the rest of the report still stands.
    error?: string
}

// An NVMe has no SMART attribute table at all, so the two kinds carry
// different evidence and the report says which it is rather than leaving a
// missing field to be guessed at.
export type DiskKind = 'ata' | 'nvme'

// One SMART attribute smartctl flagged. `raw` is the string it printed, never
// a number: vendors pack several fields into that integer.
export interface Attribute {
    id: number
    name: string
    value: number
    worst: number
    threshold: number
    raw: string
}

export interface NVMeHealth {
    critical_warning: number
    percentage_used: number
    available_spare: number
    spare_threshold: number
    media_errors: number
    error_log_entries: number
}

export interface SelfTest {
    type: string
    status: string
    passed: boolean
    // The drive's power-on hour count when the test ran, which is how a
    // self-test log dates itself.
    hours: number
}

export interface Disk {
    device: string
    kind: DiskKind
    pool?: string
    vdev?: string
    // Present only when smartctl has to be given a different path than
    // /dev/<device> — an NVMe is nda0 everywhere else but /dev/nvme0 to
    // smartctl.
    smart_path?: string
    model: string
    serial: string
    capacity: number
    rotation_rate?: number
    // The drive's OWN verdict. It is not a summary of the attributes below
    // and routinely disagrees with them.
    passed: boolean
    temperature_c?: number
    power_on_hours?: number
    // smartctl's judgment, carried as given: below threshold now, and ever.
    failing?: Attribute[]
    failed?: Attribute[]
    nvme?: NVMeHealth
    self_test?: SelfTest
}

// A running guest's cost to the host, from its bhyve process (#126).
export interface GuestUsage {
    guest: string
    // Percent of ONE core, as a jail's is.
    pcpu: number
    // Guest memory the host has given it so far, from bhyvectl. It grows as
    // the guest touches memory and does not shrink when it frees it.
    memory_resident: number
    error?: string
}

export interface MetricsReport {
    host: HostMetrics
    jails: JailUsage[]
    guests: GuestUsage[]
    guests_unavailable?: string
    // Every jail the host has, running or not, so "3 of 5" needs no second
    // request.
    configured: number
    // Empty on a host without smartmontools, which is a package rather than
    // base.
    disks: Disk[]
    // Why disks is empty, when it is not because the host has none: smartctl
    // not installed, not on the service's PATH, or refusing to list devices.
    disks_unavailable?: string
}

export async function getMetrics(): Promise<MetricsReport> {
    return await api<MetricsReport>('/metrics')
}
