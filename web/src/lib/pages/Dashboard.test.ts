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
        guests: [],
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

// The FreeBSD card (#229): the host's userland, a pending reboot, and what is
// behind the host.
describe('the dashboard FreeBSD card', () => {
    const host = '15.1-RELEASE-p6'
    const standing = (name: string, version: string, status: 'current' | 'behind' | 'unknown', levels?: number) =>
        ({ name, version, status, levels, host })

    it('says a reboot is pending and counts what is behind', async () => {
        report.os = {
            host: { kernel_running: '15.1-RELEASE-p5', kernel_installed: host, userland: host, reboot_pending: true },
            jails: [standing('web', host, 'current'), standing('db', '15.1-RELEASE-p2', 'behind', 4),
                standing('cache', '15.1-RELEASE-p5', 'behind', 1), standing('odd', '', 'unknown')],
            templates: [standing('15.1-RELEASE', '15.1-RELEASE-p5', 'behind', 1)],
        }
        const { container } = render(Dashboard)
        expect(await screen.findByText('Reboot pending')).toBeTruthy()
        const card = container.querySelector('[data-card="freebsd"]')!
        expect(card.textContent).toMatch(/Running kernel p5, installed\s+p6/)
        expect(card.textContent).toMatch(/2 jails\s+and\s+1 template\s+behind the host/)
        expect(card.querySelector('a[href="/jails"]')).toBeTruthy()
        expect(card.querySelector('a[href="/templates"]')).toBeTruthy()
    })

    it('shows the running kernel and nothing behind when all is current', async () => {
        report.os = {
            host: { kernel_running: host, kernel_installed: host, userland: host, reboot_pending: false },
            jails: [standing('web', host, 'current')],
            templates: [],
        }
        const { container } = render(Dashboard)
        expect(await screen.findByText(`kernel ${host}`)).toBeTruthy()
        expect(screen.queryByText('Reboot pending')).toBeNull()
        expect(container.querySelector('[data-card="freebsd"]')!.textContent)
            .toMatch(/No jail or template is behind the host/)
    })

    it('says why when the versions could not be read', async () => {
        report.os_unavailable = 'sysctl: permission denied'
        render(Dashboard)
        expect(await screen.findByText(/Could not read the versions: sysctl: permission denied/)).toBeTruthy()
    })

    it('has no card where nothing reads the versions', async () => {
        const { container } = render(Dashboard)
        await screen.findByText(/found no disk it can read/)
        expect(container.querySelector('[data-card="freebsd"]')).toBeNull()
    })
})

// The vulnerable packages card (#230): the host's count, which jails and
// templates have any, and how old the database is.
describe('the dashboard vulnerable packages card', () => {
    const db = { path: '/var/db/pkg/vuln.xml', present: true, updated: new Date(Date.now() - 3 * 3600_000).toISOString(), stale: false }
    const counted = (name: string, status: 'clean' | 'vulnerable' | 'unknown', vulnerable = 0) =>
        ({ name, status, packages: 20, vulnerable })

    it('counts the host and links to what has vulnerable packages', async () => {
        report.vulnerabilities = {
            checked: new Date().toISOString(), database: db,
            host: { status: 'vulnerable', packages: 400, vulnerable: 2 },
            jails: [counted('db', 'vulnerable', 5), counted('api', 'clean'), counted('odd', 'unknown')],
            templates: [counted('pg18', 'vulnerable', 5)],
        }
        const { container } = render(Dashboard)
        await screen.findByText(/with vulnerable packages/)
        const card = container.querySelector('[data-card="packages"]')!
        expect(card.querySelector('.figure')!.textContent).toMatch(/2\s*on the host/)
        expect(card.textContent).toMatch(/1 jail\s+and\s+1 template\s+with vulnerable packages/)
        expect(card.querySelector('a[href="/jails"]')).toBeTruthy()
        expect(card.textContent).toMatch(/Vulnerability database fetched 3 h ago\./)
        expect(card.textContent).toMatch(/1 jail or template not audited/)
    })

    it('says nothing is vulnerable only after an audit', async () => {
        report.vulnerabilities = {
            checked: new Date().toISOString(), database: db,
            host: { status: 'clean', packages: 400, vulnerable: 0 },
            jails: [counted('api', 'clean')], templates: [],
        }
        const { container } = render(Dashboard)
        expect(await screen.findByText(/No jail or template has a vulnerable package/)).toBeTruthy()
        expect(container.querySelector('[data-card="packages"] .figure')!.textContent).toMatch(/0\s*on the host/)
    })

    it('shows no count and warns when there is no database', async () => {
        report.vulnerabilities = {
            checked: new Date().toISOString(), database: { path: '/var/db/pkg/vuln.xml', present: false, stale: false },
            host: { status: 'unknown', packages: 0, vulnerable: 0 },
            jails: [counted('api', 'unknown')], templates: [],
        }
        const { container } = render(Dashboard)
        const warning = await screen.findByText(/No vulnerability database at/)
        expect(warning.classList.contains('under--warn')).toBe(true)
        const card = container.querySelector('[data-card="packages"]')!
        expect(card.querySelector('.figure')!.textContent!.trim()).toBe('—')
        // Unknown is never said the way clean is.
        expect(card.textContent).not.toMatch(/No jail or template has a vulnerable package/)
        expect(card.textContent).toMatch(/1 jail or template not audited/)
    })

    it('says the first audit has not finished', async () => {
        report.vulnerabilities = {
            database: db, host: { status: 'unchecked', packages: 0, vulnerable: 0 }, jails: [], templates: [],
        }
        render(Dashboard)
        expect(await screen.findByText(/The first audit hasn't finished/)).toBeTruthy()
    })
})
