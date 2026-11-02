// The live stream (#124, JD-052): one EventSource for the whole app, opened
// once someone is signed in. An event is a hint, not state: it names what
// changed, and a page that cares reads it again through the endpoints it
// already uses. While the stream is down, pages behave as they did before it
// existed -- they read on open and poll during long work -- so the stream
// only ever makes a page fresher, never wrong.

export type LiveType = 'operation' | 'state' | 'progress' | 'reset'

export interface LiveEvent {
    type: LiveType
    // The instance, template or image it names: an operation's target, or a
    // state or progress event's name. Absent from a reset.
    name?: string
    // 'jail' or 'vm' for state, 'template' or 'image' for progress.
    kind?: string
    step?: string
}

export const live = $state({ connected: false })

type Listener = (e: LiveEvent) => void
const listeners = new Set<Listener>()
let source: EventSource | null = null
let retry: ReturnType<typeof setTimeout> | undefined

// After the server refused the stream outright -- 503 when it is full, or a
// session that ended -- EventSource gives up for good. Try again this much
// later; until then the pages poll as they always did.
const RETRY_CLOSED = 30_000

export function connectLive(): void {
    if (source !== null || typeof EventSource === 'undefined') return
    const es = new EventSource('/api/events')
    source = es
    es.onopen = () => { live.connected = true }
    es.onerror = () => {
        live.connected = false
        if (es.readyState === EventSource.CLOSED) {
            source = null
            retry = setTimeout(connectLive, RETRY_CLOSED)
        }
        // Otherwise EventSource reconnects by itself, with Last-Event-ID.
    }
    for (const type of ['operation', 'state', 'progress', 'reset'] as const) {
        es.addEventListener(type, msg => dispatch(type, (msg as MessageEvent).data))
    }
}

export function disconnectLive(): void {
    clearTimeout(retry)
    source?.close()
    source = null
    live.connected = false
}

function dispatch(type: LiveType, raw: string): void {
    let data: { target?: string, name?: string, kind?: string, step?: string } = {}
    try {
        data = JSON.parse(raw)
    } catch {
        // A hint that cannot be read is still a hint that something changed.
    }
    const e: LiveEvent = { type, name: data.target ?? data.name, kind: data.kind, step: data.step }
    for (const fn of listeners) fn(e)
}

// onLive calls reload when an event the page wants arrives, and on every
// reset, which means "read everything again". A burst -- a stop that is
// audited and then observed, several progress steps -- becomes one reload.
// It returns the function that stops listening.
export function onLive(wanted: (e: LiveEvent) => boolean, reload: () => void, wait = 300): () => void {
    let timer: ReturnType<typeof setTimeout> | undefined
    const fn: Listener = e => {
        if (e.type !== 'reset' && !wanted(e)) return
        clearTimeout(timer)
        timer = setTimeout(reload, wait)
    }
    listeners.add(fn)
    return () => {
        clearTimeout(timer)
        listeners.delete(fn)
    }
}

// What a page about instances wants: anything but progress, which is about
// templates and images.
export const aboutInstances = (e: LiveEvent): boolean => e.type !== 'progress'

export const aboutInstance = (name: string) => (e: LiveEvent): boolean =>
    e.type !== 'progress' && e.name === name

// Templates and images change through operations and report progress.
export const aboutWork = (kind: 'template' | 'image') => (e: LiveEvent): boolean =>
    e.type === 'operation' || (e.type === 'progress' && e.kind === kind)
