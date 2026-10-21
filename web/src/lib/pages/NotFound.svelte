<script lang="ts">
import { route, onNavClick } from '../router.svelte'
</script>

<header class="page-head">
    <h1>Page not found</h1>
    <p class="page-sub">
        Nothing is served at <code>{route.path}</code>.
    </p>
</header>

<p class="notice">
    <a href="/jails" class="btn btn--small" onclick={(e) => onNavClick(e, '/jails')}>
        Go to Jails
    </a>
</p>

<style>
a.btn {
    text-decoration: none;
}
</style>
