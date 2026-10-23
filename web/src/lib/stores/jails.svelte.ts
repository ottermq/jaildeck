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
