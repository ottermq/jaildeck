import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import type { FirewallReport, LogReport } from '../stores/firewall.svelte'
import Firewall from './Firewall.svelte'

// The page leads with PF's state because the same rules mean different things
// in each (JD-034). These pin what each state says; how it looks is checked
// by looking at it (docs/UI.md).

let report: FirewallReport
let log: LogReport

vi.mock('../stores/firewall.svelte', async (original) => ({
    ...(await original<typeof import('../stores/firewall.svelte')>()),
    getFirewall: vi.fn(async () => report),
    getFirewallLog: vi.fn(async () => log),
    getFirewallTable: vi.fn(async (name: string) => ({
        name,
        addresses: name === 'blocked' ? ['192.0.2.10', '198.51.100.0/24'] : [],
    })),
}))

function rule(number: number, text: string, packets = 0) {
    return {
        number, text, action: text.split(' ')[0], evaluations: 1, packets,
        bytes: packets * 84, states: 0, state_creations: 0,
    }
}

function loaded(state: 'enabled' | 'disabled'): FirewallReport {
    return {
        state,
        for_seconds: 1279,
        state_table: { current: 2, searches: 8, inserts: 2, removals: 0 },
        counters: { match: 2 },
        rules: [rule(0, 'pass in all flags S/SA keep state', 12), rule(1, 'anchor "jaildeck/*" all')],
        nat: [],
        anchors: [
            { name: 'jaildeck', rules: [], nat: [] },
            { name: 'jaildeck/web', rules: [rule(0, 'pass in proto tcp to port = http')], nat: [] },
        ],
        tables: [
            { name: 'blocked', flags: '-pa-r--', persistent: true, active: true, referenced: true },
            { name: 'empty', flags: '-pa----', persistent: true, active: true, referenced: false },
        ],
    }
}

beforeEach(() => {
    report = loaded('enabled')
    log = { file: '/var/log/pflog', found: true, flush_delay_seconds: 60, lines: [] }
})

describe('the firewall page', () => {
    // The common case on the reference hosts, and an answer rather than an
    // error.
    it('says plainly when PF is not loaded, and shows no policy', async () => {
        report = { state: 'not_loaded', rules: [], nat: [], anchors: [], tables: [] }
        render(Firewall)
        expect(await screen.findByText('Not loaded')).toBeTruthy()
        expect(screen.getByText(/so it is filtering nothing/)).toBeTruthy()
        expect(screen.queryByRole('heading', { name: /^Rules/ })).toBeNull()
        expect(screen.queryByRole('alert')).toBeNull()
    })

    // Rules that read exactly as enforced ones would, so the state is said
    // before them -- and pfctl's clock is not presented as "disabled for".
    it('says a disabled ruleset is not being applied, before the rules', async () => {
        report = loaded('disabled')
        render(Firewall)
        const banner = await screen.findByRole('status')
        // The markup wraps its sentences, so compare words, not line breaks.
        const said = (banner.textContent ?? '').replace(/\s+/g, ' ')
        expect(said).toMatch(/none of these rules is being applied/)
        expect(said).toMatch(/or since boot if it has never been on/)
        expect(said).not.toMatch(/Disabled for/)
        // The banner comes before the rules in the document.
        const rules = screen.getByRole('heading', { name: /^Rules/ })
        expect(banner.compareDocumentPosition(rules) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    })

    it('lists every anchor with its own rules, an empty one last', async () => {
        render(Firewall)
        await screen.findByRole('heading', { name: /^Anchors/ })
        const anchors = screen.getAllByRole('heading', { level: 3 }).map((h) => h.textContent)
        expect(anchors).toEqual(['jaildeck/web', 'jaildeck'])
        expect(screen.getByText('Empty.')).toBeTruthy()
    })

    // No count until a table is opened: pfctl's own count is wrong on 15.1.
    it('reads a table only when it is opened, and counts what it read', async () => {
        const { getFirewallTable } = await import('../stores/firewall.svelte')
        render(Firewall)
        const buttons = await screen.findAllByRole('button', { name: 'Show addresses' })
        expect(getFirewallTable).not.toHaveBeenCalled()

        await fireEvent.click(buttons[0])
        expect(await screen.findByText('2 entries')).toBeTruthy()
        expect(screen.getByText('198.51.100.0/24')).toBeTruthy()

        await fireEvent.click(screen.getAllByRole('button', { name: 'Show addresses' })[0])
        expect(await screen.findByText('Empty.', { selector: 'span' })).toBeTruthy()
    })

    it('shows no lines from a log too large to read to its end, and says why', async () => {
        log = {
            file: '/var/log/pflog', found: true, flush_delay_seconds: 60, truncated: true,
            lines: [{ raw: 'stale', time: '2026-10-25 01:00:00.000000', rule: '0/0', rule_number: 0,
                action: 'pass', direction: 'out', interface: 'vtnet0', packet: 'old packet' }],
        }
        render(Firewall)
        expect(await screen.findByText(/the ones that were read are the oldest/)).toBeTruthy()
        expect(screen.queryByText('old packet')).toBeNull()
    })

    // An anchor is named by its last component in pflog; the page says so
    // rather than guessing the full name.
    it('marks a log line from inside an anchor by the name pflog gave it', async () => {
        log = {
            file: '/var/log/pflog', found: true, flush_delay_seconds: 60,
            lines: [{ raw: 'x', time: '2026-10-25 18:04:44.974139', rule: '1.web.0/0', rule_number: 1,
                anchor: 'web', action: 'pass', direction: 'out', interface: 'vtnet0', packet: 'udp' }],
        }
        render(Firewall)
        await screen.findByText('1.web.0/0')
        expect(screen.getByText(/in an anchor named/).textContent).toMatch(/web/)
        expect(screen.getByText(/writes the file every 60 s/)).toBeTruthy()
    })

    it('says when there is no log file at all', async () => {
        log = { file: '/var/log/pflog', found: false, flush_delay_seconds: 60, lines: [] }
        render(Firewall)
        expect(await screen.findByText(/nothing has been logged, or pflogd is not running/)).toBeTruthy()
    })
})
