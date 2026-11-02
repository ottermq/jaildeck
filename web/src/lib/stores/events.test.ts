import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { aboutInstance, aboutInstances, aboutWork, connectLive, disconnectLive, live, onLive } from './events.svelte'

// Just enough of EventSource to drive the store: it opens, errors, closes
// and delivers named events.
class FakeSource {
    static CLOSED = 2
    static last: FakeSource | null = null
    readyState = 0
    url: string
    onopen: (() => void) | null = null
    onerror: (() => void) | null = null
    handlers = new Map<string, (m: MessageEvent) => void>()
    constructor(url: string) {
        this.url = url
        FakeSource.last = this
    }
    addEventListener(type: string, fn: (m: MessageEvent) => void) { this.handlers.set(type, fn) }
    close() { this.readyState = FakeSource.CLOSED }
    emit(type: string, data: unknown) {
        this.handlers.get(type)?.({ data: JSON.stringify(data) } as MessageEvent)
    }
}

beforeEach(() => {
    vi.useFakeTimers()
    vi.stubGlobal('EventSource', FakeSource)
})

afterEach(() => {
    disconnectLive()
    vi.unstubAllGlobals()
    vi.useRealTimers()
})

describe('the live stream', () => {
    it('says whether it is live, and reloads once for a burst', () => {
        connectLive()
        const es = FakeSource.last!
        expect(es.url).toBe('/api/events')
        expect(live.connected).toBe(false)
        es.onopen!()
        expect(live.connected).toBe(true)

        const reload = vi.fn()
        const stop = onLive(aboutInstances, reload)
        es.emit('operation', { operation: 'stop', target: 'web', success: true })
        es.emit('state', { kind: 'jail', name: 'web', from: 'running', to: 'stopped', seen: 'observed' })
        es.emit('progress', { kind: 'image', name: 'noble', step: 'writing' })
        vi.advanceTimersByTime(300)
        expect(reload).toHaveBeenCalledTimes(1)

        stop()
        es.emit('operation', { operation: 'start', target: 'web' })
        vi.advanceTimersByTime(300)
        expect(reload).toHaveBeenCalledTimes(1)

        // Dropping and reconnecting by itself is not live in between.
        es.onerror!()
        expect(live.connected).toBe(false)
    })

    it('reloads on a reset whatever the page wanted', () => {
        connectLive()
        const reload = vi.fn()
        onLive(() => false, reload)
        FakeSource.last!.emit('reset', {})
        vi.advanceTimersByTime(300)
        expect(reload).toHaveBeenCalledOnce()
    })

    it('tries again later after the server refused it', () => {
        connectLive()
        const first = FakeSource.last!
        first.readyState = FakeSource.CLOSED
        first.onerror!()
        vi.advanceTimersByTime(29_000)
        expect(FakeSource.last).toBe(first)
        vi.advanceTimersByTime(1_000)
        expect(FakeSource.last).not.toBe(first)
    })

    it('picks what each page cares about', () => {
        expect(aboutInstance('web')({ type: 'state', name: 'web' })).toBe(true)
        expect(aboutInstance('web')({ type: 'operation', name: 'db' })).toBe(false)
        expect(aboutWork('image')({ type: 'progress', kind: 'image', name: 'noble' })).toBe(true)
        expect(aboutWork('image')({ type: 'progress', kind: 'template', name: '15.0' })).toBe(false)
        expect(aboutWork('template')({ type: 'state', kind: 'vm', name: 'batch' })).toBe(false)
    })
})
