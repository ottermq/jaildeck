// A bulk action is the existing per-instance call made once per instance,
// each audited as it always is (#129). Nothing new on the server: the client
// that asked for ten starts is the one that knows they belong together.

export type Outcome =
    | { status: 'queued' }
    | { status: 'running' }
    | { status: 'done', detail?: string, warn?: boolean }
    | { status: 'failed', detail: string }
    | { status: 'skipped', detail: string }

// A guest's stop can take its whole minute, so one at a time would make a
// set of twenty take twenty minutes; all at once would put twenty `vm stop`s
// on the host together. Four is a few of each.
export const concurrency = 4

// runAll calls work for every item, at most `limit` at a time, and reports
// each one's outcome as it changes. A failure is that item's outcome and
// nothing more: one failing must not hide what happened to the others.
export async function runAll<T>(
    items: T[],
    work: (item: T) => Promise<{ detail?: string, warn?: boolean } | void>,
    report: (item: T, outcome: Outcome) => void,
    describe: (err: unknown) => string,
    limit = concurrency,
): Promise<void> {
    for (const item of items) report(item, { status: 'queued' })
    let next = 0
    async function worker() {
        while (next < items.length) {
            const item = items[next++]
            report(item, { status: 'running' })
            try {
                const result = await work(item)
                report(item, { status: 'done', ...(result ?? {}) })
            } catch (err) {
                report(item, { status: 'failed', detail: describe(err) })
            }
        }
    }
    await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker))
}
