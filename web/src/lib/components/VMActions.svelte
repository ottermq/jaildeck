<script lang="ts">
import { runVMOperation } from '../stores/vms.svelte'
import type { VM, VMOperation, OperationResult } from '../stores/vms.svelte'
import { ApiError } from '../services/api'
import { ask } from '../stores/confirm.svelte'

// Start, stop and restart for one guest (JD-036). The page that holds it says
// what happened: ondone gets the guest as it is afterwards, or why not.
let { vm, ondone }: {
    vm: VM
    ondone: (outcome: { result?: OperationResult, error?: string }) => void
} = $props()

let pending = $state<VMOperation | null>(null)

const busy: Record<VMOperation, string> = {
    start: 'Starting…',
    stop: 'Stopping…',
    restart: 'Restarting…',
}

// The deadline is the one thing worth stopping over: past it the guest is
// powered off, and whatever it had not written is gone.
const stopping = [
    'It is sent the ACPI power button, as if someone pressed it, and given a minute to shut down.',
    'A guest that has not stopped by then is powered off — the plug pulled — and anything it had not written to disk is lost.',
]

async function run(op: VMOperation) {
    if (pending) return
    if (op !== 'start') {
        const confirmed = await ask({
            title: `${op === 'stop' ? 'Stop' : 'Restart'} ${vm.name}?`,
            lines: stopping,
            verb: op === 'stop' ? 'Stop' : 'Restart',
        })
        if (!confirmed) return
    }
    pending = op
    try {
        ondone({ result: await runVMOperation(vm.name, op) })
    } catch (err) {
        ondone({ error: describe(err, op) })
    } finally {
        pending = null
    }
}

function describe(err: unknown, op: VMOperation): string {
    if (!(err instanceof ApiError)) return 'Could not reach Jail Deck'
    if (err.statusCode === 504) {
        return `The ${op} of ${vm.name} timed out and may still be under way.`
    }
    return err.statusText ?? `Could not ${op} ${vm.name}.`
}
</script>

<div class="actions">
    {#if vm.state === 'running'}
        <button type="button" class="btn btn--small btn--danger" disabled={!!pending} aria-busy={!!pending}
                aria-label={`Stop ${vm.name}`} onclick={() => run('stop')}>
            {pending === 'stop' ? busy.stop : 'Stop'}
        </button>
        <button type="button" class="btn btn--small btn--danger" disabled={!!pending} aria-busy={!!pending}
                aria-label={`Restart ${vm.name}`} onclick={() => run('restart')}>
            {pending === 'restart' ? busy.restart : 'Restart'}
        </button>
    {:else if vm.state === 'stopped' || vm.state === 'suspended'}
        <button type="button" class="btn btn--small btn--primary" disabled={!!pending} aria-busy={!!pending}
                aria-label={`Start ${vm.name}`} onclick={() => run('start')}>
            {pending === 'start' ? busy.start : vm.state === 'suspended' ? 'Resume' : 'Start'}
        </button>
    {:else if vm.state === 'bootloader'}
        <!-- vm-bhyve will not stop a boot loader without asking on a terminal. -->
        <span class="why">Booting — try again once it is running</span>
    {:else if vm.state === 'locked'}
        <span class="why">Held by {vm.locked_by}</span>
    {/if}
</div>

<style>
.actions {
    display: flex;
    gap: var(--space-2);
    justify-content: flex-end;
    align-items: center;
}

.why {
    font-size: 0.8rem;
    color: var(--text-muted);
}
</style>
