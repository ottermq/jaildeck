import type { VMState } from './stores/vms.svelte'

// vm-bhyve's states on the outcome badges. Only running is green. The two in
// between -- a boot loader still running, a guest another host holds -- are
// amber; stopped and suspended are both at rest.
export const stateBadge: Record<VMState, string> = {
    running: 'running',
    bootloader: 'timed-out',
    stopped: 'stopped',
    suspended: 'stopped',
    locked: 'timed-out',
}
