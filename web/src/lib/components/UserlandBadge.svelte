<script lang="ts">
import { userlandLabel, userlandTitle, userlandTone, type Standing } from '../osversion'

// A jail's or a template's FreeBSD userland against the host's (#229).
let { standing, what }: { standing: Standing, what: 'jail' | 'template' } = $props()

const toneClass = { ok: 'badge--running', warn: 'badge--warn', muted: 'badge--stopped' }
</script>

<span class="badge userland {toneClass[userlandTone(standing)]}"
      title={userlandTitle(standing, what)}
      data-status={standing.status}>{userlandLabel(standing)}</span>

<style>
.userland {
    text-transform: none;
    letter-spacing: 0;
    font-variant-numeric: tabular-nums;
}
</style>
