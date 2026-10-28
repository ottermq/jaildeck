import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import ConsolePanel from './ConsolePanel.svelte'

// The terminal underneath is faked as in Console.test.ts: what matters here is
// the panel around it -- when it offers a console, and what it says when one
// closes (JD-042).

vi.mock('@xterm/xterm', () => ({
    Terminal: class {
        cols = 80; rows = 24
        loadAddon() {} open() {} focus() {} dispose() {} write() {} onData() {} onResize() {}
    },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))
vi.mock('@xterm/xterm/css/xterm.css', () => ({}))

class FakeSocket {
    static OPEN = 1
    static last: FakeSocket | null = null
    readyState = 0
    binaryType = ''
    onopen?: () => void
    onmessage?: (e: { data: unknown }) => void
    onclose?: () => void
    constructor(public url: string) { FakeSocket.last = this }
    send() {}
    close() {}
}

beforeEach(() => {
    FakeSocket.last = null
    vi.stubGlobal('WebSocket', FakeSocket)
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
})
afterEach(() => vi.unstubAllGlobals())

async function closedWith(reason: string) {
    await waitFor(() => expect(FakeSocket.last).not.toBeNull())
    FakeSocket.last!.onmessage?.({ data: JSON.stringify({ type: 'closed', reason }) })
    FakeSocket.last!.onclose?.()
}

describe('the console panel', () => {
    it('offers a console only while there is something to attach to', () => {
        render(ConsolePanel, { path: '/vms/web/console', running: false, openLabel: 'Open the console' })
        expect(screen.queryByRole('button', { name: 'Open the console' })).toBeNull()
    })

    it('says why a guest’s console closed, tells the page, and offers no reopening of a stopped guest', async () => {
        const onended = vi.fn()
        const { rerender } = render(ConsolePanel, { path: '/vms/web/console', running: true, openLabel: 'Open the console', onended })
        await fireEvent.click(screen.getByRole('button', { name: 'Open the console' }))
        await closedWith('guest-stopped')
        expect(await screen.findByText('The guest stopped, and its console with it.')).toBeTruthy()
        expect(onended).toHaveBeenCalledWith('guest-stopped')
        await rerender({ path: '/vms/web/console', running: false, openLabel: 'Open the console', onended })
        expect(screen.queryByRole('button', { name: 'Open again' })).toBeNull()
        expect(screen.getByRole('button', { name: 'Dismiss' })).toBeTruthy()
    })

    it('opens again while the jail still runs, with a fresh connection', async () => {
        render(ConsolePanel, { path: '/jails/nginx/console', running: true, openLabel: 'Open a shell' })
        await fireEvent.click(screen.getByRole('button', { name: 'Open a shell' }))
        await closedWith('idle')
        expect(await screen.findByText('Closed: nothing was typed for a while.')).toBeTruthy()
        const first = FakeSocket.last
        await fireEvent.click(screen.getByRole('button', { name: 'Open again' }))
        await waitFor(() => expect(FakeSocket.last).not.toBe(first))
        expect(screen.queryByText('Closed: nothing was typed for a while.')).toBeNull()
    })

    it('says so when it never connected', async () => {
        render(ConsolePanel, { path: '/vms/web/console', running: true, openLabel: 'Open the console' })
        await fireEvent.click(screen.getByRole('button', { name: 'Open the console' }))
        await waitFor(() => expect(FakeSocket.last).not.toBeNull())
        FakeSocket.last!.onclose?.()
        expect(await screen.findByText('The connection ended, or could not be made.')).toBeTruthy()
    })
})
