import type { MetricsReport } from './stores/metrics.svelte'

// The trailing window behind the instance list's sparklines (#126): the last
// few readings of each running instance, kept in this tab's memory and
// nowhere else. Jail Deck keeps no history (JD-011, JD-031); this is what
// the page has seen since it was opened, and a reload starts it again.
// Anything longer is Prometheus's.

// Twenty-four readings five seconds apart: two minutes.
export const SAMPLES = 24
export const INTERVAL = 5000

export interface Series {
    cpu: number[]
    memory: number[]
}

export class UsageWindow {
    private series = new Map<string, Series>()

    constructor(private readonly size = SAMPLES) {}

    // record adds one report. An instance not in it -- stopped, or one that
    // could not be read -- loses its window, so a restart is not drawn as
    // continuous with the run before it.
    record(report: MetricsReport): void {
        const seen = new Set<string>()
        const add = (key: string, cpu: number, memory: number) => {
            seen.add(key)
            const s = this.series.get(key) ?? { cpu: [], memory: [] }
            s.cpu = [...s.cpu, cpu].slice(-this.size)
            s.memory = [...s.memory, memory].slice(-this.size)
            this.series.set(key, s)
        }
        for (const j of report.jails) if (!j.error) add(`jail:${j.jail}`, j.pcpu, j.memoryuse)
        for (const g of report.guests ?? []) if (!g.error) add(`vm:${g.guest}`, g.pcpu, g.memory_resident)
        for (const key of [...this.series.keys()]) if (!seen.has(key)) this.series.delete(key)
    }

    get(key: string): Series | undefined {
        return this.series.get(key)
    }
}
