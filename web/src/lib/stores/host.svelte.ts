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

export type ReclaimableKind = 'template' | 'image' | 'retained' | 'snapshot' | 'archive'

export interface ReclaimableItem {
    kind: ReclaimableKind
    // What the removal takes: a name, a dataset@snapshot, an archive's path.
    name: string
    jail?: string
    size: number
    why: string
    created?: string
    // The existing endpoint that removes it, confirm included. Absent for an
    // archive, which no endpoint removes.
    remove?: { method: string, path: string }
}

export interface Reclaimable {
    items: ReclaimableItem[]
    total: number
    kinds: Partial<Record<ReclaimableKind, number>>
    snapshot_age_days: number
    skipped_snapshots: Record<string, number>
    problems: string[]
}

// Read now, like the readiness report (JD-011, JD-049).
export async function getReclaimable(olderThanDays?: number): Promise<Reclaimable> {
    const query = olderThanDays ? `?older_than_days=${olderThanDays}` : ''
    return await api<Reclaimable>(`/host/reclaimable${query}`, { cache: 'no-store' })
}

// reclaim sends the request the report gave, as it gave it: the refusals are
// that endpoint's, so a template something was cloned from since the report
// was read is still refused.
export async function reclaim(item: ReclaimableItem): Promise<void> {
    if (!item.remove) throw new Error(`${item.name} has no endpoint that removes it`)
    await api<unknown>(item.remove.path.replace(/^\/api/, ''), { method: item.remove.method })
}
