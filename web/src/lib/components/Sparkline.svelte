<script lang="ts">
import { INTERVAL } from '../usageWindow'
// A small trailing line beside a value in a table cell (#126). One series,
// named by its column, so no legend; the value itself stays in text beside
// it. Drawn in muted ink: the status colours mean something else.
let { values, floor = 0, label, format }: {
    values: number[]
    // The top of the scale is at least this, so a jail idling at 0.3% of a
    // core is not drawn as a mountain. CPU passes 100, one core.
    floor?: number
    // What the line is, for the summary: "CPU", "Memory".
    label: string
    format: (v: number) => string
} = $props()

const W = 64
const H = 18
const PAD = 2

let top = $derived(Math.max(floor, ...values))
function y(v: number): number {
    return top > 0 ? H - PAD - (v / top) * (H - 2 * PAD) : H - PAD
}
let points = $derived(values.map((v, i) =>
    `${((i / Math.max(1, values.length - 1)) * W).toFixed(1)},${y(v).toFixed(1)}`).join(' '))
let summary = $derived(values.length < 2 ? '' :
    `${label} over the last ${Math.round((values.length - 1) * INTERVAL / 1000)} s: ` +
    `low ${format(Math.min(...values))}, high ${format(Math.max(...values))}, now ${format(values[values.length - 1])}`)
</script>

{#if values.length >= 2}
    <svg class="spark" viewBox="0 0 {W} {H}" width={W} height={H} role="img" aria-label={summary}>
        <title>{summary}</title>
        <polyline points={points} />
        <circle cx={W} cy={y(values[values.length - 1])} r="2" />
    </svg>
{/if}

<style>
.spark {
    display: inline-block;
    vertical-align: middle;
    margin-right: var(--space-2);
    overflow: visible;
}

polyline {
    fill: none;
    stroke: var(--text-muted);
    stroke-width: 1.5;
    stroke-linejoin: round;
    stroke-linecap: round;
    vector-effect: non-scaling-stroke;
}

circle {
    fill: var(--text-muted);
}
</style>
