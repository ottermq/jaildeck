// Trailing slashes are normalised so matching stays a plain comparison.
function normalize(path: string): string {
    const trimmed = path.replace(/\/+$/, '')
    return trimmed === '' ? '/': trimmed
}

export const route = $state({path: normalize(window.location.pathname)})

// match tests a path against a pattern whose `:name` segments stand for
// anything, and returns what they matched -- or null when the pattern does not
// apply. Equal segment counts only: /jails and /jails/ottermq are different
// pages, not the same page with something missing.
//
// Deliberately this small. A dependency would bring history handling, lazy
// loading and nested layouts that this application has no use for; what it
// actually lacked was one segment of a path meaning a jail's name.
export function match(pattern: string, path: string = route.path): Record<string, string> | null {
    const want = pattern.split('/')
    const got = path.split('/')
    if (want.length !== got.length) return null

    const params: Record<string, string> = {}
    for (let i = 0; i < want.length; i++) {
        if (want[i].startsWith(':')) {
            // An empty segment is not a value: /jails/ would otherwise read as
            // a jail whose name is the empty string.
            if (got[i] === '') return null
            params[want[i].slice(1)] = decodeURIComponent(got[i])
            continue
        }
        if (want[i] !== got[i]) return null
    }
    return params
}

export function navigate(to:string, replace = false):void {
    const next = normalize(to)
    if (next===route.path) return
    if (replace) history.replaceState({}, '', next)
    else history.pushState({}, '', next)
    route.path = next
}

// Lives for the page's lifetime: there is nothing to unsubscribe from a
// single-page app  whose router is module, 
window.addEventListener('popstate', ()=>{
    route.path = normalize(window.location.pathname)
})

export function onNavClick(e: MouseEvent, to: string): void {
    // Let the browser handle modifier and non-primary clicks.
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
    e.preventDefault()
    navigate(to)
}
