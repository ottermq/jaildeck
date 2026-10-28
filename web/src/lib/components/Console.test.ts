import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, waitFor } from '@testing-library/svelte'
import Console from './Console.svelte'

// xterm.js needs a real browser's layout, so it is replaced by a terminal that
// records what it is given; the WebSocket by one the test drives (JD-042).

const terms: FakeTerminal[] = []

class FakeTerminal {
    cols = 100
    rows = 30
    written: Uint8Array[] = []
    data?: (d: string) => void
    resized?: (size: { cols: number, rows: number }) => void
    constructor() { terms.push(this) }
    loadAddon() {}
    open() {}
    focus() {}
    dispose() {}
    write(b: Uint8Array) { this.written.push(b) }
    onData(cb: (d: string) => void) { this.data = cb }
    onResize(cb: (size: { cols: number, rows: number }) => void) { this.resized = cb }
}

vi.mock('@xterm/xterm', () => ({ Terminal: FakeTerminal }))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))
vi.mock('@xterm/xterm/css/xterm.css', () => ({}))

class FakeSocket {
    static OPEN = 1
    static last: FakeSocket
    readyState = 0
    binaryType = ''
    sent: (string | Uint8Array)[] = []
    onopen?: () => void
    onmessage?: (e: { data: unknown }) => void
    onclose?: () => void
    constructor(public url: string) { FakeSocket.last = this }
    send(d: string | Uint8Array) { this.sent.push(d) }
    close() { this.readyState = 3 }
    opened() { this.readyState = 1; this.onopen?.() }
}

beforeEach(() => {
    terms.length = 0
    vi.stubGlobal('WebSocket', FakeSocket)
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
})
afterEach(() => vi.unstubAllGlobals())

async function opened(onclosed = vi.fn()) {
    render(Console, { path: '/jails/nginx/console', onclosed })
    await waitFor(() => expect(terms.length).toBe(1))
    await waitFor(() => expect(FakeSocket.last).toBeTruthy())
    FakeSocket.last.opened()
    return { term: terms[0], socket: FakeSocket.last, onclosed }
}

describe('the console', () => {
    it('connects with the terminal’s size, on this host', async () => {
        const { socket } = await opened()
        expect(socket.url).toBe(`ws://${location.host}/api/jails/nginx/console?cols=100&rows=30`)
        expect(socket.binaryType).toBe('arraybuffer')
    })

    it('sends what is typed as bytes, and a resize as JSON', async () => {
        const { term, socket } = await opened()
        term.data?.('ls\r')
        term.resized?.({ cols: 120, rows: 40 })
        expect(new TextDecoder().decode(socket.sent[0] as Uint8Array)).toBe('ls\r')
        expect(JSON.parse(socket.sent[1] as string)).toEqual({ type: 'resize', cols: 120, rows: 40 })
    })

    it('writes the shell’s bytes, and passes the server’s reason on when it closes', async () => {
        const { term, socket, onclosed } = await opened()
        socket.onmessage?.({ data: new TextEncoder().encode('root@nginx:/ # ').buffer })
        expect(new TextDecoder().decode(term.written[0])).toBe('root@nginx:/ # ')
        socket.onmessage?.({ data: '{"type":"closed","reason":"jail-stopped"}' })
        socket.onclose?.()
        expect(onclosed).toHaveBeenCalledWith('jail-stopped')
    })

    it('says nothing it was not told: a connection that failed has no reason', async () => {
        const { socket, onclosed } = await opened()
        socket.onclose?.()
        expect(onclosed).toHaveBeenCalledWith('')
    })

    it('sends nothing before the socket is open', async () => {
        render(Console, { path: '/jails/nginx/console' })
        await waitFor(() => expect(terms.length).toBe(1))
        terms[0].data?.('x')
        expect(FakeSocket.last.sent).toEqual([])
    })
})
