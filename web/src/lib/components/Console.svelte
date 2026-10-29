<script lang="ts">
import { onMount, onDestroy } from 'svelte'
import type { Terminal } from '@xterm/xterm'
import type { FitAddon } from '@xterm/addon-fit'

// A terminal over the console WebSocket (JD-042): binary frames are the
// terminal's bytes, text frames JSON control -- resize from here, closed from
// the server. xterm.js is loaded only when a console opens, so no other page
// pays for it.
let { path, onclosed }: { path: string, onclosed?: (reason: string, detail: string) => void } = $props()

let host: HTMLDivElement
let term: Terminal | null = null
let fit: FitAddon | null = null
let socket: WebSocket | null = null
let observer: ResizeObserver | null = null
let reason = ''
let detail = ''
let state = $state<'loading' | 'open' | 'closed'>('loading')

const encoder = new TextEncoder()

function send(data: string | Uint8Array<ArrayBuffer>) {
    if (socket?.readyState === WebSocket.OPEN) socket.send(data)
}

onMount(async () => {
    const [{ Terminal }, { FitAddon }] = await Promise.all([import('@xterm/xterm'), import('@xterm/addon-fit')])
    await import('@xterm/xterm/css/xterm.css')
    term = new Terminal({
        cursorBlink: true,
        fontFamily: getComputedStyle(document.documentElement).getPropertyValue('--mono') || 'monospace',
        fontSize: 13,
        scrollback: 5000,
        theme: { background: '#111418', foreground: '#e6e9ee', cursor: '#e6e9ee' },
    })
    fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host)
    fit.fit()

    const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:'
    socket = new WebSocket(`${scheme}//${location.host}/api${path}?cols=${term.cols}&rows=${term.rows}`)
    socket.binaryType = 'arraybuffer'
    socket.onopen = () => {
        state = 'open'
        term?.focus()
    }
    socket.onmessage = (event: MessageEvent) => {
        if (typeof event.data === 'string') {
            try {
                const message = JSON.parse(event.data)
                if (message.type === 'closed') {
                    reason = message.reason ?? ''
                    detail = message.detail ?? ''
                }
            } catch {
                // Not ours to understand; nothing to do.
            }
            return
        }
        term?.write(new Uint8Array(event.data as ArrayBuffer))
    }
    socket.onclose = () => {
        state = 'closed'
        onclosed?.(reason, detail)
    }
    term.onData(data => send(encoder.encode(data)))
    term.onResize(({ cols, rows }) => send(JSON.stringify({ type: 'resize', cols, rows })))
    observer = new ResizeObserver(() => fit?.fit())
    observer.observe(host)
})

onDestroy(() => {
    observer?.disconnect()
    socket?.close()
    term?.dispose()
})
</script>

<div class="console" class:console--closed={state === 'closed'} bind:this={host} role="application"
     aria-label="Terminal" aria-busy={state === 'loading'}></div>

<style>
.console {
    height: 26rem;
    padding: var(--space-2);
    background: #111418;
    border-radius: var(--radius);
    overflow: hidden;
}

.console--closed {
    opacity: 0.6;
}

@media (max-width: 640px) {
    .console {
        height: 20rem;
    }
}
</style>
