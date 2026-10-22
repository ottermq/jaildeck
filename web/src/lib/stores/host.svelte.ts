import { api } from "../services/api";

export type CheckStatus = 'pass' | 'fail' | 'unknown'
export type CapabilityStatus = 'ready' | 'unavailable' | 'unknown'

export interface Check {
    id: string
    title: string
    status: CheckStatus
    // What the host actually said; empty when nothing could be observed.
    observed: string
    detail: string
    // Set on every failed check and only there.
    remedy: string
    gates: string[]
}

export interface Capability {
    id: string
    title: string
    status: CapabilityStatus
    gated_by: string[]
}

export interface Readiness {
    capabilities: Capability[]
    checks: Check[]
}

// The report is derived state, recomputed by the server on every request
// (JD-011), so nothing here keeps a copy and the browser is told not to either.
export async function getReadiness(): Promise<Readiness> {
    return await api<Readiness>('/host/readiness', { cache: 'no-store' })
}
