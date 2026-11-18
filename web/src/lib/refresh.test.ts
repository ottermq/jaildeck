import { describe, expect, it } from 'vitest'
import { builtLabel, refreshRefusal, suggestedName } from './refresh'
import type { Template } from './stores/templates.svelte'

const t = (over: Partial<Template>): Template => ({
    name: 'pg18', dataset: 'tank/jails/templates/pg18', mountpoint: '/usr/local/jails/templates/pg18',
    ready: true, state: 'ready', used: 0, referenced: 0, ...over,
})

describe('refresh', () => {
    it('allows only a tree Jail Deck built', () => {
        for (const built of ['release', 'derived', 'refreshed'] as const) {
            expect(refreshRefusal(t({ built }))).toBe('')
        }
        expect(refreshRefusal(t({ built: 'jail' }))).toMatch(/made from a jail/)
        expect(refreshRefusal(t({}))).toMatch(/zfs set jaildeck:built=derived tank\/jails\/templates\/pg18/)
    })

    it('needs a ready template with nothing running', () => {
        expect(refreshRefusal(t({ built: 'derived', ready: false }))).toMatch(/no @base/)
        expect(refreshRefusal(t({ built: 'derived', running: { step: 'x', since: '' } }))).toMatch(/already working/)
    })

    it('suggests the name and the date, without stacking dates', () => {
        const day = new Date('2026-11-18T12:00:00Z')
        expect(suggestedName(t({}), day)).toBe('pg18-20261118')
        expect(suggestedName(t({ name: 'pg18-20261001' }), day)).toBe('pg18-20261118')
    })

    it('says how it was made', () => {
        expect(builtLabel(t({ built: 'jail' }))).toBe('made from a jail')
        expect(builtLabel(t({}))).toBe('')
    })
})
