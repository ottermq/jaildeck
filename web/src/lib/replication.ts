// ZFS replication (#247): when each dataset a zrepl job replicates last
// reached the other side, from zrepl's replication cursors. The server reads
// them every minute; this file only says it in words. Jail Deck sets no
// threshold -- how old is too old depends on the job's interval, which lives
// in zrepl's configuration -- so nothing here is coloured as a warning.

export type ReplicationStatus = 'replicating' | 'none' | 'unknown' | 'unchecked'

export interface ReplicatedDataset {
    name: string
    // When the newest snapshot that reached the other side was taken.
    replicated: string
}

export interface ReplicationJob {
    name: string
    // The earliest replicated among its datasets.
    oldest: string
    datasets: ReplicatedDataset[]
}

export interface ReplicationSnapshot {
    status: ReplicationStatus
    checked?: string
    reason?: string
    jobs: ReplicationJob[]
}

// since says how long ago, in at most two units: "9 min", "1 h 14 min",
// "3 days".
export function since(iso: string, now: Date = new Date()): string {
    const minutes = Math.max(0, Math.floor((now.getTime() - new Date(iso).getTime()) / 60000))
    if (minutes < 1) return 'under a minute'
    if (minutes < 60) return `${minutes} min`
    const hours = Math.floor(minutes / 60)
    if (hours < 48) return minutes % 60 === 0 ? `${hours} h` : `${hours} h ${minutes % 60} min`
    return `${Math.floor(hours / 24)} days`
}

// furthestBehind is the job whose oldest dataset is oldest: the card's
// headline.
export function furthestBehind(snap: ReplicationSnapshot): ReplicationJob | undefined {
    return snap.jobs.reduce<ReplicationJob | undefined>(
        (worst, job) => (!worst || job.oldest < worst.oldest ? job : worst), undefined)
}

// jobsFor is each job that replicates any of datasets, with the oldest
// replicated time among them: a jail's line.
export function jobsFor(snap: ReplicationSnapshot, datasets: string[]): { name: string, oldest: string, count: number }[] {
    const mine = new Set(datasets)
    const out: { name: string, oldest: string, count: number }[] = []
    for (const job of snap.jobs) {
        const hits = job.datasets.filter((d) => mine.has(d.name))
        if (hits.length === 0) continue
        const oldest = hits.reduce((a, d) => (d.replicated < a ? d.replicated : a), hits[0].replicated)
        out.push({ name: job.name, oldest, count: hits.length })
    }
    return out
}

export const replicationTitle =
    'How long ago the newest snapshot to reach the other side was taken, for the dataset furthest behind, from the replication cursors zrepl keeps on this host. '
    + 'A job another host pulls from is included. How old is too old depends on the job\'s interval.'
