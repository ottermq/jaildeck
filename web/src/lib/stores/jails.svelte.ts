import { api, ApiError } from "../services/api";

// Dataset is one ZFS dataset belonging to a jail. The sizes are exact bytes,
// not the "1.41G" zfs prints: formatBytes spells them for display.
export interface Dataset {
    name: string
    mountpoint: string
    used: number
    referenced: number
    available: number
}

export interface JailData {
    jid: string
    name: string
    status: "running" | "stopped"
    hostname: string
    ip: string
    path: string
    // Both are absent, not empty, for a jail with no dataset of its own --
    // and also when the pool could not be read, which the API logs rather
    // than reporting (#2).
    datasets?: Dataset[]
    properties?: Record<string, string>
}

// Snapshot is one snapshot of one of a jail's datasets. Ordered by create_txg
// rather than by created: a timestamp has second granularity and two snapshots
// taken in the same second tie on it, which is exactly when getting the order
// wrong matters (JD-024).
export interface Snapshot {
    name: string
    dataset: string
    short: string
    used: number
    referenced: number
    created: string
    create_txg: number
}

export type Operation = 'start'|'stop'|'restart'


export async function listJails(): Promise<JailData[]>{    
        return await api<JailData[]>('/jails');
}

export async function getJail(name:string): Promise<JailData>{
        return await api<JailData>(`/jails/${encodeURIComponent(name)}`);
}

export async function runJailOperation(name:string, op:Operation): Promise<JailData>{
    return await api<JailData>(`/jails/${encodeURIComponent(name)}/${op}`, {method: 'POST'})
}

export async function listSnapshots(name: string): Promise<Snapshot[]> {
    return await api<Snapshot[]>(`/jails/${encodeURIComponent(name)}/snapshots`)
}

// DatasetChange is what creating or converging a child dataset answers with:
// what was applied, and -- when it exists somewhere else already -- what stands
// in the way instead of a move (JD-023).
export interface PropertyConflict {
    property: string
    want: string
    have: string
}

export interface DatasetChange {
    dataset: string
    created: boolean
    applied: Record<string, string>
    conflict?: PropertyConflict
}

// DatasetMove is a dataset leaving a jail or coming back. `remounted` says the
// filesystem itself moved and not just the name, which is what a running jail
// notices (JD-025).
export interface DatasetMove {
    from: string
    to: string
    mountpoint: string
    remounted: boolean
}

// setTags merges user properties into the jail's own dataset. A null value
// removes that property -- `zfs inherit` -- which is not the same as setting it
// empty: "this jail has no instance" against "its instance is the empty
// string". PATCH because these merge; a replacing write would strip a tag
// another tool set.
export async function setTags(
    name: string,
    tags: Record<string, string | null>,
): Promise<Record<string, string>> {
    return await api<Record<string, string>>(`/jails/${encodeURIComponent(name)}/properties`, {
        method: 'PATCH',
        body: JSON.stringify(tags),
    })
}

// ensureDataset creates a child dataset or converges an existing one. The
// properties are native ZFS ones, and only five may be set (JD-023); the server
// names the list when one is not among them.
export async function ensureDataset(
    name: string,
    child: string,
    properties: Record<string, string>,
): Promise<DatasetChange> {
    return await api<DatasetChange>(
        `/jails/${encodeURIComponent(name)}/datasets/${encodeURIComponent(child)}`,
        { method: 'PUT', body: JSON.stringify({ properties }) },
    )
}

// retainDataset moves a child dataset out of the jail, to
// <pool>/jails/retained/<as>. Nothing is destroyed; the jail simply stops being
// its parent, which is what makes "destroy the jail, keep the data" possible.
export async function retainDataset(name: string, child: string, as: string): Promise<DatasetMove> {
    return await api<DatasetMove>(
        `/jails/${encodeURIComponent(name)}/datasets/${encodeURIComponent(child)}/retain`,
        { method: 'POST', body: JSON.stringify({ name: as }) },
    )
}

// SnapshotBlock says why a snapshot operation refused, and names what is in the
// way. Newer can be accepted -- that is what destroy_newer means. Clones and
// holds cannot be, through this API at all: taking a clone with its origin
// needs `zfs destroy -R`, which Jail Deck never passes, and a hold exists
// precisely to say "not this one" (JD-024).
export interface SnapshotBlock {
    reason: string
    newer?: string[]
    clones?: string[]
    holds?: string[]
}

// SnapshotResult is what a snapshot call did. `created` and `destroyed` are
// always present; `blocked` means nothing ran.
export interface SnapshotResult {
    snapshot: string
    created: string[]
    destroyed: string[]
    // The snapshot was already there and nothing ran -- the answer to the
    // retry a client makes after a timeout (JD-016).
    existed?: boolean
    blocked?: SnapshotBlock
}

// Where a snapshot lives in the API. An empty child is the jail's own dataset,
// which is also the recursive route: a snapshot taken there covers every child
// at one point in time, and destroying it there takes every child's snapshot
// of the same name with it (JD-024).
function snapshotPath(name: string, child: string, short: string): string {
    const jail = `/jails/${encodeURIComponent(name)}`
    const base = child === '' ? jail : `${jail}/datasets/${encodeURIComponent(child)}`
    return `${base}/snapshots/${encodeURIComponent(short)}`
}

// A refusal arrives as 409 with the result as its body: nothing ran, and the
// body says what stood in the way. Returned rather than thrown, because it is
// an answer and not a failure -- the caller has something to show, and for
// newer snapshots something to offer.
async function snapshotCall(path: string, init: RequestInit): Promise<SnapshotResult> {
    try {
        return await api<SnapshotResult>(path, init)
    } catch (err) {
        if (err instanceof ApiError && err.statusCode === 409 && isSnapshotResult(err.body)) {
            return err.body
        }
        throw err
    }
}

function isSnapshotResult(body: unknown): body is SnapshotResult {
    return typeof body === 'object' && body !== null && 'snapshot' in body && 'blocked' in body
}

export async function createSnapshot(name: string, child: string, short: string): Promise<SnapshotResult> {
    return await snapshotCall(snapshotPath(name, child, short), { method: 'PUT' })
}

// confirm is the full dataset@snapshot. The API refuses anything else, and
// deliberately so: `nightly` names a snapshot on every dataset that has one
// (JD-024).
export async function destroySnapshot(
    name: string,
    child: string,
    short: string,
    confirm: string,
): Promise<SnapshotResult> {
    const path = `${snapshotPath(name, child, short)}?confirm=${encodeURIComponent(confirm)}`
    return await snapshotCall(path, { method: 'DELETE' })
}

// Never recursive: `zfs rollback` cannot descend into a dataset's children at
// all, so a jail whose data lives in a child rolls its root back and leaves
// that child alone (JD-024).
export async function rollbackSnapshot(
    name: string,
    child: string,
    short: string,
    confirm: string,
    destroyNewer: boolean,
): Promise<SnapshotResult> {
    const query = new URLSearchParams({ confirm })
    if (destroyNewer) query.set('destroy_newer', 'true')
    const path = `${snapshotPath(name, child, short)}/rollback?${query}`
    return await snapshotCall(path, { method: 'POST' })
}
