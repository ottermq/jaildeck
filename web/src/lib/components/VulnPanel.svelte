<script lang="ts">
import { onMount } from 'svelte'
import { getVulnerabilities } from '../stores/vulns.svelte'
import { age, databaseNote, vulnTitle, type VulnDatabase, type VulnReport } from '../vulns'
import VulnBadge from './VulnBadge.svelte'

// One jail's or template's vulnerable packages, from the last audit (#230).
// A panel of its own, loaded on its own: a failure here costs this panel and
// nothing else on the page.
let { name, what }: { name: string, what: 'jail' | 'template' } = $props()

let report = $state<VulnReport | null>(null)
let database = $state<VulnDatabase | null>(null)
let failed = $state('')

async function load() {
    failed = ''
    try {
        const snap = await getVulnerabilities()
        const list = what === 'jail' ? snap.jails : snap.templates
        report = list.find((r) => r.name === name)
            ?? { status: 'unchecked', packages: 0, vulnerable: 0, findings: [] }
        database = snap.database
    } catch {
        failed = 'Could not read the last package audit.'
    }
}

onMount(load)

let note = $derived(database ? databaseNote(database) : null)
</script>

<section class="panel" data-panel="vulnerabilities">
    <div class="panel__head">
        <h2>Packages</h2>
    </div>
    {#if failed}
        <p class="notice notice--error">{failed}</p>
    {:else if report}
        <p class="summary">
            <VulnBadge summary={report} {what} />
            <span>{vulnTitle(report, what)}</span>
            {#if report.checked}<span class="muted">Audited {age(report.checked)}.</span>{/if}
        </p>
        {#if report.reason}
            <p class="notice">{report.reason}</p>
        {/if}
        {#if report.findings.length > 0}
            <div class="table-wrap">
            <table>
                <thead>
                    <tr>
                        <th>Package</th>
                        <th>Version</th>
                        <th>Vulnerability</th>
                        <th>Needed by</th>
                    </tr>
                </thead>
                <tbody>
                    {#each report.findings as f (f.name)}
                        <tr data-package={f.name}>
                            <td class="name"><code>{f.name}</code></td>
                            <td class="nowrap"><code>{f.version}</code></td>
                            <td>
                                {#each f.issues as issue, i (i)}
                                    <div class="issue">
                                        {#if issue.url.startsWith('https://')}
                                            <a href={issue.url} target="_blank" rel="noopener noreferrer">{issue.description}</a>
                                        {:else}
                                            {issue.description}
                                        {/if}
                                        {#if issue.affected.length > 0}
                                            <span class="muted">({issue.affected.join(', ')})</span>
                                        {/if}
                                        {#if issue.cves.length > 0}
                                            <details>
                                                <summary>{issue.cves.length === 1 ? issue.cves[0] : `${issue.cves.length} CVEs`}</summary>
                                                <p class="cves">{issue.cves.join(' ')}</p>
                                            </details>
                                        {/if}
                                    </div>
                                {/each}
                            </td>
                            <td>
                                {#if f.reverse_dependencies.length > 0}
                                    {f.reverse_dependencies.join(', ')}
                                {:else}—{/if}
                            </td>
                        </tr>
                    {/each}
                </tbody>
            </table>
            </div>
            <p class="hint">
                Jail Deck doesn't upgrade packages: <code>pkg upgrade</code> inside the {what}
                can restart services and change their configuration.
                {#if what === 'template'}Upgrading a template doesn't change the jails already cloned from it.{/if}
            </p>
        {/if}
        {#if note}
            <p class="db-note" class:db-note--warn={note.warn}>{note.text}</p>
        {/if}
    {/if}
</section>

<style>
/* Its own spacing, as TagsPanel has: a page's scoped .panel rules don't
   reach a component's section. */
.panel {
    margin-top: var(--space-6);
}

.panel__head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-4);
}

.panel__head h2 {
    margin-bottom: var(--space-3);
}

.summary {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
}
.issue + .issue {
    margin-top: 0.4rem;
}
.cves {
    margin: 0.25rem 0 0;
    font-family: var(--mono);
    font-size: 0.8rem;
    overflow-wrap: anywhere;
}
details summary {
    cursor: pointer;
    font-size: 0.85rem;
}
.hint {
    color: var(--text-muted);
    font-size: 0.85rem;
    margin: 0.75rem 0 0;
}
.muted,
.db-note {
    color: var(--text-muted);
    font-size: 0.85rem;
}
.db-note--warn {
    color: var(--warn);
}
</style>
