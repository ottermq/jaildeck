import { describe, expect, it } from 'vitest'
import { asNumber, buildSpec, typed } from './jailSpec'
import type { SpecForm } from './jailSpec'

// The form as it stands when it opens: every box empty, every section unused.
function blank(over: Partial<SpecForm> = {}): SpecForm {
    return {
        name: 'web1',
        template: 'tank/jails/templates/15.1-RELEASE@base',
        hostname: '',
        mode: 'disable',
        ip4: '',
        iface: '',
        bridge: '',
        isPrivate: false,
        gateway: '',
        autostart: false,
        start: true,
        limits: {},
        paramValues: {},
        children: [],
        tags: [],
        ...over,
    }
}

describe('buildSpec', () => {
    // An empty box is "say nothing about this", not "set it empty".
    it('sends only what was filled in', () => {
        expect(buildSpec(blank())).toEqual({
            name: 'web1',
            template: 'tank/jails/templates/15.1-RELEASE@base',
            network: { mode: 'disable' },
        })
    })

    it('trims what was typed', () => {
        const spec = buildSpec(blank({ name: '  web1 ', hostname: ' web1.lan ' }))
        expect(spec.name).toBe('web1')
        expect(spec.hostname).toBe('web1.lan')
    })

    it('sends start: false and autostart: true only when they differ from the default', () => {
        expect(buildSpec(blank({ start: false, autostart: true }))).toMatchObject({
            start: false,
            autostart: true,
        })
        const spec = buildSpec(blank())
        expect(spec).not.toHaveProperty('start')
        expect(spec).not.toHaveProperty('autostart')
    })

    describe('network', () => {
        it('alias sends its address and interface', () => {
            const spec = buildSpec(blank({ mode: 'alias', ip4: '192.168.1.50', iface: 'em0' }))
            expect(spec.network).toEqual({ mode: 'alias', ip4: '192.168.1.50', interface: 'em0' })
        })

        // The API treats a field set for a mode that ignores it as a mistake.
        it('does not send a vnet field for alias, typed before the mode changed', () => {
            const spec = buildSpec(blank({ mode: 'alias', ip4: '10.0.0.2', iface: 'em0', bridge: 'bridge0', gateway: '10.0.0.1' }))
            expect(spec.network).not.toHaveProperty('bridge')
            expect(spec.network).not.toHaveProperty('gateway')
        })

        it('vnet sends its bridge, and the rest only when given', () => {
            expect(buildSpec(blank({ mode: 'vnet', bridge: 'bridge0' })).network)
                .toEqual({ mode: 'vnet', bridge: 'bridge0' })
            expect(buildSpec(blank({ mode: 'vnet', bridge: 'bridge0', isPrivate: true, ip4: '10.0.0.2/24', gateway: '10.0.0.1' })).network)
                .toEqual({ mode: 'vnet', bridge: 'bridge0', private: true, ip4: '10.0.0.2/24', gateway: '10.0.0.1' })
        })
    })

    describe('limits', () => {
        it('sends the numeric ones as numbers and the sizes as strings', () => {
            const spec = buildSpec(blank({ limits: { memoryuse: '640M', maxproc: '64', pcpu: '50', cpuset: '0-3' } }))
            expect(spec.limits).toEqual({ memoryuse: '640M', maxproc: 64, pcpu: 50, cpuset: '0-3' })
        })

        // v0.9.0: Number('abc') is NaN, JSON writes NaN as null, and the
        // server reads null as "no limit asked for" -- a typo that would have
        // left a jail silently unlimited. Kept as typed, the API refuses it
        // by name.
        it('keeps a numeric limit that is not a number as typed, never as NaN', () => {
            const spec = buildSpec(blank({ limits: { maxproc: '64x' } }))
            expect(spec.limits).toEqual({ maxproc: '64x' })
            expect(JSON.stringify(spec)).not.toContain('null')
        })

        it('sends no limits object when none was filled in', () => {
            expect(buildSpec(blank({ limits: { memoryuse: '  ' } }))).not.toHaveProperty('limits')
        })
    })

    describe('parameters', () => {
        // v0.9.0: paramValues was typed Record<string, string>, and a number
        // input binds a number -- so .trim() would have thrown on the first
        // parameter anybody touched. TypeScript cannot see through a binding.
        it('takes the number a number input binds', () => {
            const spec = buildSpec(blank({ paramValues: { securelevel: 3, enforce_statfs: 2 } }))
            expect(spec.params).toEqual({ securelevel: 3, enforce_statfs: 2 })
        })

        // An emptied number input binds null, not ''.
        it('treats the null an emptied number input binds as nothing asked', () => {
            const spec = buildSpec(blank({ paramValues: { securelevel: null } }))
            expect(spec).not.toHaveProperty('params')
        })

        it('keeps a zero, which is a value and not an empty box', () => {
            const spec = buildSpec(blank({ paramValues: { securelevel: 0 } }))
            expect(spec.params).toEqual({ securelevel: 0 })
        })

        it('sends a choice as its string and a flag as a boolean', () => {
            const spec = buildSpec(blank({ paramValues: { sysvshm: 'new', 'allow.chflags': 'true', 'allow.set_hostname': 'false' } }))
            expect(spec.params).toEqual({ sysvshm: 'new', 'allow.chflags': true, 'allow.set_hostname': false })
        })

        it('ignores a parameter that is not in the allowlist', () => {
            expect(buildSpec(blank({ paramValues: { 'allow.raw_sockets': 'true' } }))).not.toHaveProperty('params')
        })
    })

    describe('datasets and tags', () => {
        it('sends a child dataset with only the properties filled in', () => {
            const spec = buildSpec(blank({
                children: [{ id: 0, name: ' data ', properties: { refquota: '2G', compression: '', atime: ' off ' } }],
            }))
            expect(spec.datasets).toEqual([{ name: 'data', properties: { refquota: '2G', atime: 'off' } }])
        })

        it('skips a row whose name was left empty', () => {
            const spec = buildSpec(blank({
                children: [{ id: 0, name: '', properties: { refquota: '2G' } }, { id: 1, name: 'logs', properties: {} }],
            }))
            expect(spec.datasets).toEqual([{ name: 'logs' }])
        })

        it('sends tags as properties, the value exactly as typed', () => {
            const spec = buildSpec(blank({
                tags: [{ id: 0, name: ' otter:instance ', value: 'mq-01' }, { id: 1, name: '', value: 'orphan' }],
            }))
            expect(spec.properties).toEqual({ 'otter:instance': 'mq-01' })
        })
    })
})

describe('asNumber', () => {
    it('makes a number of what is one, and leaves the rest alone', () => {
        expect(asNumber('64')).toBe(64)
        expect(asNumber('1e3')).toBe(1000)
        expect(asNumber('abc')).toBe('abc')
        expect(asNumber('Infinity')).toBe('Infinity')
    })
})

describe('typed', () => {
    it('turns whatever a control bound into a trimmed string', () => {
        expect(typed(3)).toBe('3')
        expect(typed(0)).toBe('0')
        expect(typed(null)).toBe('')
        expect(typed(undefined)).toBe('')
        expect(typed(' new ')).toBe('new')
    })
})
