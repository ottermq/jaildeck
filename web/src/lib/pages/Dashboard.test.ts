import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/svelte'
import type { MetricsReport } from '../stores/metrics.svelte'
import Dashboard from './Dashboard.svelte'

// The disk section's empty states. The dashboard used to explain an empty
// list itself -- "comes from the smartmontools package" -- and said it to a
// host that had smartmontools installed but not on the service's PATH (#95).
// It shows the server's reason now, and only guesses when there is none.

let report: MetricsReport

vi.mock('../stores/metrics.svelte', async (original) => ({
    ...(await original<typeof import('../stores/metrics.svelte')>()),
    getMetrics: vi.fn(async () => report),
}))

beforeEach(() => {
    report = {
        host: {
            load: { one: 0.1, five: 0.1, fifteen: 0.1 },
            cpu: { cpus: 16, ticks: { user: 1, nice: 0, system: 1, interrupt: 0, idle: 98 } },
            memory: { physical: 1 << 30, free: 1 << 29, active: 0, inactive: 0, wired: 0 },
            pools: [],
        },
        jails: [],
        configured: 0,
        disks: [],
    }
})

describe('the dashboard disk section', () => {
    it('shows the reason the server gives for having no disk health', async () => {
        report.disks_unavailable =
            'smartctl is not available: smartctl is not on PATH /sbin:/bin:/usr/sbin:/usr/bin'
        render(Dashboard)
        const notice = await screen.findByText(/No disk health:/)
        expect(notice.textContent).toMatch(/not on PATH \/sbin:\/bin:\/usr\/sbin:\/usr\/bin/)
        expect(screen.queryByText(/smartmontools package/)).toBeNull()
    })

    it('says smartctl found nothing when it answered and there is no reason', async () => {
        render(Dashboard)
        expect(await screen.findByText(/found no disk it can read/)).toBeTruthy()
    })
})
