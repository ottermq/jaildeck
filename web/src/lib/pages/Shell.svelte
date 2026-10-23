<script lang="ts">
import { onMount } from 'svelte'
import { route, navigate, onNavClick, match } from '../router.svelte'
import { auth, logout } from '../stores/auth.svelte'
import Jails from './Jails.svelte'
import Jail from './Jail.svelte'
import Templates from './Templates.svelte'
import Template from './Template.svelte'
import Operations from './Operations.svelte'
import Host from './Host.svelte'
import NotFound from './NotFound.svelte'

let sessionExpiry = $derived(
    auth.principal?.expires_at === undefined
        ? '—'
        : new Date(auth.principal.expires_at).toLocaleString()
)

const menu = [
    { label: 'Jails', href: '/jails' },
    { label: 'Templates', href: '/templates' },
    { label: 'Operations', href: '/operations' },
    { label: 'Host', href: '/host' },
]

// A detail page keeps its section highlighted: /jails/ottermq is still Jails.
function isActive(href: string): boolean {
    return route.path === href || route.path.startsWith(href + '/')
}

// Matched once per render rather than inside the markup, so the name is
// available to key the component on — without the key, navigating from one
// jail to another would reuse the mounted component and never re-fetch.
let jail = $derived(match('/jails/:name'))
let template = $derived(match('/templates/:name'))

// The landing path is replaced rather than pushed: a push would make Back
// return to '/', which redirects forward again and traps the user.
onMount(() => {
    if (route.path === '/') navigate('/jails', true)
})
</script>

<div class="shell">
    <aside class="rail">
        <div class="rail__brand">
            <span class="brand">
                <span class="brand__accent">J</span>ail<span class="brand__accent">D</span>eck
            </span>
        </div>

        <nav class="rail__nav" aria-label="Sections">
            {#each menu as { label, href } (href)}
                <a
                    {href}
                    class="rail__link"
                    class:is-active={isActive(href)}
                    aria-current={isActive(href) ? 'page' : undefined}
                    onclick={(e) => onNavClick(e, href)}
                >{label}</a>
            {/each}
        </nav>

        <div class="rail__foot">
            <dl class="session">
                <dt>Signed in</dt>
                <dd>{auth.principal?.kind ?? '—'}{auth.principal?.name ? ` · ${auth.principal.name}` : ''}</dd>
                <dt>Session ends</dt>
                <dd>{sessionExpiry}</dd>
            </dl>
            <button type="button" class="rail__logout" onclick={logout}>Log out</button>
        </div>
    </aside>

    <main class="main">
        {#if route.path === '/jails'}
            <Jails />
        {:else if jail}
            {#key jail.name}<Jail name={jail.name} />{/key}
        {:else if route.path === '/templates'}
            <Templates />
        {:else if template}
            {#key template.name}<Template name={template.name} />{/key}
        {:else if route.path === '/operations'}
            <Operations />
        {:else if route.path === '/host'}
            <Host />
        {:else}
            <NotFound />
        {/if}
    </main>
</div>

<style>
.shell {
    display: flex;
    min-height: 100svh;
}

.rail {
    flex: 0 0 216px;
    display: flex;
    flex-direction: column;
    background: var(--rail-bg);
    color: var(--rail-text);
    border-right: 1px solid var(--rail-border);

    /*
     * Pinned to the viewport rather than stretched to the container. As a plain
     * flex item the rail grows with the main column, which pushes Log out below
     * the fold on any page with a long table.
     */
    position: sticky;
    top: 0;
    align-self: flex-start;
    height: 100svh;
    overflow-y: auto;
}

.rail__brand {
    padding: var(--space-5) var(--space-4) var(--space-4);
    border-bottom: 1px solid var(--rail-border);
}

.brand {
    font-size: 1.6rem;
    font-weight: 700;
    letter-spacing: -0.02em;
    color: var(--rail-text-strong);
}

.brand__accent {
    color: var(--brand);
}

.rail__nav {
    display: flex;
    flex-direction: column;
    padding: var(--space-3) var(--space-2);
    gap: 2px;
    flex: 1;
}

.rail__link {
    padding: var(--space-2) var(--space-3);
    border-radius: var(--radius-sm);
    color: var(--rail-text);
    text-decoration: none;
    font-weight: 500;
}

.rail__link:hover {
    background: var(--rail-active-bg);
    color: var(--rail-text-strong);
}

.rail__link.is-active {
    background: var(--rail-active-bg);
    color: var(--rail-text-strong);
    box-shadow: inset 2px 0 0 var(--brand);
}

.rail__foot {
    padding: var(--space-4);
    border-top: 1px solid var(--rail-border);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
}

.session {
    margin: 0;
    display: grid;
    gap: 2px;
    font-size: 0.75rem;
}

.session dt {
    color: var(--rail-text);
    opacity: 0.65;
    text-transform: uppercase;
    letter-spacing: 0.04em;
}

.session dd {
    margin: 0 0 var(--space-2);
    color: var(--rail-text-strong);
}

.session dd:last-of-type {
    margin-bottom: 0;
}

.rail__logout {
    font: inherit;
    font-weight: 500;
    padding: var(--space-2) var(--space-3);
    border-radius: var(--radius);
    border: 1px solid var(--rail-border);
    background: transparent;
    color: var(--rail-text);
    cursor: pointer;
}

.rail__logout:hover {
    border-color: var(--brand);
    color: var(--rail-text-strong);
}

.main {
    flex: 1;
    min-width: 0;
    padding: var(--space-6);
}

/* Narrow viewports: the rail becomes a top bar so the table keeps the width. */
@media (max-width: 760px) {
    .shell {
        flex-direction: column;
    }

    .rail {
        flex: none;
        border-right: 0;
        border-bottom: 1px solid var(--rail-border);
        /* A top bar, so the full-height pin from the wide layout must go. */
        height: auto;
        overflow: visible;
        z-index: 1;
    }

    .rail__brand {
        padding: var(--space-3) var(--space-4);
    }

    .rail__nav {
        flex-direction: row;
        flex: none;
    }

    .rail__link.is-active {
        box-shadow: inset 0 -2px 0 var(--brand);
    }

    .rail__foot {
        flex-direction: row;
        align-items: center;
        justify-content: space-between;
    }

    .main {
        padding: var(--space-4) var(--space-4) var(--space-6);
    }
}
</style>
