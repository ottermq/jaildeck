import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/svelte'
import type { VulnSnapshot } from '../vulns'
import VulnPanel from './VulnPanel.svelte'

let snapshot: VulnSnapshot | Error

vi.mock('../stores/vulns.svelte', () => ({
    getVulnerabilities: vi.fn(async () => {
        if (snapshot instanceof Error) throw snapshot
        return snapshot
    }),
}))

const checked = new Date().toISOString()

beforeEach(() => {
    snapshot = {
        checked,
        database: { path: '/var/db/pkg/vuln.xml', present: true, updated: checked, stale: false },
        host: { status: 'clean', packages: 1, vulnerable: 0, findings: [] },
        jails: [
            {
                name: 'db', status: 'vulnerable', packages: 20, vulnerable: 2, checked, findings: [
                    {
                        name: 'postgresql18-server', version: '18.6', reverse_dependencies: [],
                        issues: [{
                            description: 'PostgreSQL -- multiple vulnerabilities',
                            cves: ['CVE-2026-6460', 'CVE-2026-6464'], affected: ['< 18.7'],
                            url: 'https://vuxml.FreeBSD.org/freebsd/1a672925.html',
                        }],
                    },
                    {
                        name: 'python312', version: '3.12.15', reverse_dependencies: ['py312-packaging'],
                        issues: [{
                            description: 'Python -- crafted', cves: ['CVE-2025-15366'], affected: ['>= 0'],
                            url: 'javascript:alert(1)',
                        }],
                    },
                ],
            },
            { name: 'broken', status: 'unknown', packages: 0, vulnerable: 0, findings: [],
                reason: 'its packages could not be listed: pkg: Invalid rootdir' },
        ],
        templates: [],
    }
})

describe('VulnPanel', () => {
    it('lists each vulnerable package with its issues', async () => {
        const { container } = render(VulnPanel, { name: 'db', what: 'jail' })
        expect(await screen.findByText('postgresql18-server')).toBeTruthy()
        const pg = container.querySelector('[data-package="postgresql18-server"]')!
        const link = pg.querySelector('a')!
        expect(link.getAttribute('href')).toBe('https://vuxml.FreeBSD.org/freebsd/1a672925.html')
        expect(link.getAttribute('rel')).toMatch(/noopener/)
        expect(pg.textContent).toMatch(/< 18\.7/)
        expect(pg.textContent).toMatch(/2 CVEs/)
        expect(container.querySelector('[data-package="python312"]')!.textContent).toMatch(/py312-packaging/)
        expect(screen.getByText(/2 of its 20 packages are in the vulnerability database/)).toBeTruthy()
    })

    // The description and the link come from a database, and a jail's
    // package names from that jail: only an https link is a link.
    it('never links anything but https', async () => {
        const { container } = render(VulnPanel, { name: 'db', what: 'jail' })
        await screen.findByText('python312')
        const py = container.querySelector('[data-package="python312"]')!
        expect(py.querySelector('a')).toBeNull()
        expect(py.textContent).toMatch(/Python -- crafted/)
    })

    it('says why a jail could not be audited', async () => {
        render(VulnPanel, { name: 'broken', what: 'jail' })
        expect(await screen.findByText(/its packages could not be listed: pkg: Invalid rootdir/)).toBeTruthy()
        expect(screen.getByText('unknown')).toBeTruthy()
    })

    it('calls a jail the audit did not include unchecked, not clean', async () => {
        render(VulnPanel, { name: 'created-since', what: 'jail' })
        expect(await screen.findByText('not checked')).toBeTruthy()
        expect(screen.queryByText('clean')).toBeNull()
    })

    it('costs only itself when the audit cannot be read', async () => {
        snapshot = new Error('down')
        render(VulnPanel, { name: 'db', what: 'jail' })
        expect(await screen.findByText(/Could not read the last package audit/)).toBeTruthy()
    })
})
