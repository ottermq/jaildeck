import { api } from "../services/api";

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
