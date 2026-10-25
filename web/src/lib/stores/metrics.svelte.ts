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

export interface MetricsReport {
    host: HostMetrics
    jails: JailUsage[]
    // Every jail the host has, running or not, so "3 of 5" needs no second
    // request.
    configured: number
}

export async function getMetrics(): Promise<MetricsReport> {
    return await api<MetricsReport>('/metrics')
}
