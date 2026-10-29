import { api } from '../services/api'

// Which Jail Deck is running (#136), as GET /api/about says it.
export interface About {
    version: string
    commit: string
    modified: boolean
    started: string
    state_dir: string
}

// Read once per page load: none of it changes while the process runs, and a
// restart is a reload away. The badge and the Host page share it.
export const about = $state<{ info: About | null }>({ info: null })

let loading: Promise<void> | null = null

// Once it has answered, it is not asked again; two callers while it is being
// asked share the one request. A failure leaves nothing shown, and the next
// caller asks again.
export function loadAbout(): Promise<void> {
    if (about.info) return Promise.resolve()
    loading ??= api<About>('/about')
        .then(info => { about.info = info })
        .catch(() => {})
        .finally(() => { loading = null })
    return loading
}

// A release's notes, for a version that is one: v0.19.0. A dev build or a
// toolchain stamp (v0.6.1-0.2026...) has none to link to.
export function releaseNotes(version: string): string | null {
    return /^v\d+\.\d+\.\d+$/.test(version)
        ? `https://github.com/ottermq/jaildeck/releases/tag/${version}`
        : null
}
