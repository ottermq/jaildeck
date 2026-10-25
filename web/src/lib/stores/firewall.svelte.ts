import { api } from '../services/api'

// PF's three states (JD-034). None is an error: not loaded is the common case
// on the reference hosts, and disabled-with-rules is a policy that is loaded
// and not being applied.
export type FirewallState = 'not_loaded' | 'disabled' | 'enabled'

export interface StateTable {
    current: number
    searches: number
    inserts: number
    removals: number
}

// A rule as pfctl printed it. `text` is pfctl's own rendering, never
// re-parsed; only the numbers, the label and the first word are taken out.
export interface Rule {
    // The @N pflog refers to rules by.
    number: number
    text: string
    action: string
    label?: string
    evaluations: number
    packets: number
    bytes: number
    states: number
    state_creations: number
    last_active?: string
}

export interface Anchor {
    name: string
    rules: Rule[]
    nat: Rule[]
    error?: string
}

// No address count: pfctl -s Tables -vv's is wrong on 15.1, so the count is
// the length of the table's contents, read when it is opened.
export interface Table {
    name: string
    flags: string
    persistent: boolean
    active: boolean
    referenced: boolean
}

export interface FirewallReport {
    state: FirewallState
    for_seconds?: number
    state_table?: StateTable
    counters?: Record<string, number>
    rules: Rule[]
    nat: Rule[]
    anchors: Anchor[]
    tables: Table[]
    notices?: string[]
}

export interface TableContents {
    name: string
    addresses: string[]
}

export interface LogLine {
    raw: string
    time?: string
    rule?: string
    // The main-ruleset number, which is unambiguous. `anchor` is only the
    // last component of the anchor's name -- all tcpdump prints.
    rule_number?: number
    anchor?: string
    action?: string
    direction?: string
    interface?: string
    packet?: string
}

export interface LogReport {
    file: string
    found: boolean
    // pflogd buffers; the file may be this far behind the packets.
    flush_delay_seconds: number
    // Too large to read to its end. Comes with no lines: the ones read would
    // be the oldest, not the most recent.
    truncated?: boolean
    lines: LogLine[]
}

export async function getFirewall(): Promise<FirewallReport> {
    return await api<FirewallReport>('/firewall')
}

export async function getFirewallTable(name: string): Promise<TableContents> {
    return await api<TableContents>(`/firewall/tables/${encodeURIComponent(name)}`)
}

export async function getFirewallLog(lines = 100): Promise<LogReport> {
    return await api<LogReport>(`/firewall/log?lines=${lines}`)
}
