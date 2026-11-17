<script lang="ts">
import { vulnLabel, vulnTitle, vulnTone, type VulnSummary } from '../vulns'

// A tree's last package audit in numbers (#230).
let { summary, what }: { summary: VulnSummary, what: 'host' | 'jail' | 'template' } = $props()

const toneClass = { ok: 'badge--running', warn: 'badge--warn', muted: 'badge--stopped' }
</script>

<span class="badge vulns {toneClass[vulnTone(summary)]}"
      title={vulnTitle(summary, what)}
      data-status={summary.status}>{vulnLabel(summary)}</span>

<style>
.vulns {
    text-transform: none;
    letter-spacing: 0;
    font-variant-numeric: tabular-nums;
}
</style>
