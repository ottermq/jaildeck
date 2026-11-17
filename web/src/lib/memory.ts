// The dashboard's memory bar (#224): what is held, and the two caches the
// kernel gives back on demand, inactive pages and the ZFS ARC. The server
// computes held, so the API and this page agree; this file only splits the
// bar and says what each part is.

import type { Memory } from './stores/metrics.svelte'

export interface Segment {
    key: 'held' | 'inactive' | 'arc'
    label: string
    bytes: number
    // Width as a percent of physical memory.
    percent: number
    title: string
}

// heldBytes is the server's figure, or physical less free from a server too
// old to send one: the headline it showed before.
export function heldBytes(m: Memory): number {
    return m.held ?? Math.max(0, m.physical - m.free)
}

// segments is the bar, left to right: held, then what is given back on
// demand. A host without ZFS has no ARC segment. Widths never add up to more
// than the bar, even when counters read a moment apart overshoot.
export function segments(m: Memory): Segment[] {
    const parts: Omit<Segment, 'percent'>[] = [
        {
            key: 'held', label: m.held !== undefined ? 'held' : 'used', bytes: heldBytes(m),
            title: 'Neither free nor given back on demand: physical memory less free, inactive and the ZFS ARC.',
        },
    ]
    if (m.held !== undefined) {
        parts.push({
            key: 'inactive', label: 'inactive', bytes: m.inactive,
            title: 'Pages not used lately. Clean ones are reclaimed without any I/O when something needs memory.',
        })
        if (m.arc !== undefined) {
            parts.push({
                key: 'arc', label: 'ARC', bytes: m.arc,
                title: 'The ZFS cache, part of wired. ZFS shrinks it when something needs memory'
                    + (m.arc_max !== undefined ? '; it may grow to the size shown under the bar.' : '.'),
            })
        }
    }
    let left = 100
    return parts.map((p) => {
        const want = m.physical > 0 ? (p.bytes / m.physical) * 100 : 0
        const percent = Math.max(0, Math.min(left, want))
        left -= percent
        return { ...p, percent }
    })
}
