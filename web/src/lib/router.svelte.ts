// Trailing slashes are normalised so matching stays a plain comparison.
function normalize(path: string): string {
    const trimmed = path.replace(/\/+$/, '')
    return trimmed === '' ? '/': trimmed
}

export const route = $state({path: normalize(window.location.pathname)})

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
