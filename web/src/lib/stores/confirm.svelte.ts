// A confirmation asked before something destructive runs.
//
// One dialog, mounted once in the shell, driven from anywhere by `ask()`. The
// alternative -- a component instance per action -- would mean a dozen of them
// across two pages, each with its own open state to get wrong.
//
// It replaces the browser's confirm(), which could not carry the one thing
// several of these operations actually need: the API asks for the full
// `dataset@snapshot` in a confirm parameter before it will destroy or roll one
// back (JD-024), and a yes/no box has nowhere to type it.
export interface Confirmation {
    // What is about to happen, as a sentence.
    title: string
    // Why it is worth stopping over. Plain strings, one per paragraph: a
    // confirmation that needs markup is one that is explaining too much.
    lines?: string[]
    // Names the operation would affect -- snapshots, datasets, jails. Shown as
    // a list, because "3 snapshots" is not an answer to "which ones".
    items?: string[]
    // The button's own words: "Destroy", "Roll back", "Remove". Never "OK".
    verb: string
    // When set, the exact text that must be typed before the button works.
    // This is the API's own requirement shown rather than invented ceremony:
    // the same string goes in the request's confirm parameter.
    typed?: string
}

// Only one can be open, so this is one value rather than a queue.
const state = $state<{ current: Confirmation | null }>({ current: null })

let settle: ((confirmed: boolean) => void) | null = null

export function current(): Confirmation | null {
    return state.current
}

// ask shows the dialog and resolves true only if the user went through with
// it. Escape, the backdrop and Cancel all resolve false, so a caller that
// never hears yes does nothing -- the safe direction.
export function ask(confirmation: Confirmation): Promise<boolean> {
    // A second ask while one is open answers the first no rather than dropping
    // its promise on the floor, which would leave that caller waiting forever.
    settle?.(false)
    state.current = confirmation
    return new Promise<boolean>(resolve => {
        settle = resolve
    })
}

export function answer(confirmed: boolean): void {
    state.current = null
    const resolve = settle
    settle = null
    resolve?.(confirmed)
}
