import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import type { JailSpec } from '../stores/jails.svelte'
import NewJail from './NewJail.svelte'

// The create form, rendered and used the way a person uses it: these are the
// tests #77 asks for. v0.9.0 shipped two bugs that only show through a
// binding -- a number input's value, and rows keyed by index -- and neither
// TypeScript nor svelte-check can see through a binding.

const sent: JailSpec[] = []

vi.mock('../stores/templates.svelte', async (original) => ({
    ...(await original<typeof import('../stores/templates.svelte')>()),
    listTemplates: vi.fn(async () => [{
        name: '15.1-RELEASE', dataset: 'tank/jails/templates/15.1-RELEASE',
        mountpoint: '/usr/local/jails/templates/15.1-RELEASE', ready: true,
        snapshot: 'tank/jails/templates/15.1-RELEASE@base', state: 'ready',
        used: 0, referenced: 0,
    }]),
}))

// Records the spec and refuses, so the form stays on screen and the test is
// about what it sent rather than about the report a success renders.
vi.mock('../stores/jails.svelte', async (original) => ({
    ...(await original<typeof import('../stores/jails.svelte')>()),
    createJail: vi.fn(async (spec: JailSpec) => {
        // A copy, as the request body would be.
        sent.push(JSON.parse(JSON.stringify(spec)) as JailSpec)
        throw new Error('test: not sent anywhere')
    }),
}))

// Jail Deck's networks the form offers a VNET jail (JD-047); none unless a
// test sets them.
let networks: { name: string, bridge: string, switch: string, subnet: string, gateway: string }[] = []
vi.mock('../stores/networks.svelte', () => ({ ownNetworks: vi.fn(async () => networks) }))

beforeEach(() => {
    sent.length = 0
    networks = []
})

// A Svelte binding listens for `input`; setting .value alone does nothing.
async function type(element: HTMLElement, value: string) {
    ;(element as HTMLInputElement).value = value
    await fireEvent.input(element)
}

async function choose(element: HTMLElement, value: string) {
    ;(element as HTMLSelectElement).value = value
    await fireEvent.change(element)
}

async function openForm() {
    render(NewJail)
    // The one ready template is chosen for the operator once it has loaded.
    await waitFor(() =>
        expect((screen.getByRole('button', { name: 'Create jail' }) as HTMLButtonElement).disabled).toBe(false))
    await type(screen.getByLabelText('Name'), 'web1')
}

async function submit(): Promise<JailSpec> {
    await fireEvent.click(screen.getByRole('button', { name: 'Create jail' }))
    await waitFor(() => expect(sent).toHaveLength(1))
    return sent[0]
}

describe('the create form', () => {
    it('puts a vnet jail on a Jail Deck network by name, its address left to it', async () => {
        networks = [{ name: 'platform', bridge: 'jdn1', switch: 'platform', subnet: '10.92.1.0/24', gateway: '10.92.1.1' }]
        await openForm()
        await choose(screen.getByLabelText('Mode'), 'vnet')
        const on = await screen.findByLabelText('On') as HTMLSelectElement
        expect(on.value).toBe('platform')
        expect(screen.queryByLabelText('Bridge')).toBeNull()
        expect((screen.getByLabelText('Address') as HTMLInputElement).placeholder).toBe('picked by Jail Deck')
        expect((screen.getByLabelText('Gateway') as HTMLInputElement).placeholder).toBe('10.92.1.1')
        expect((await submit()).network).toEqual({ mode: 'vnet', name: 'platform' })
    })

    it('puts a vnet jail on another bridge, typed in', async () => {
        networks = [{ name: 'platform', bridge: 'jdn1', switch: 'platform', subnet: '10.92.1.0/24', gateway: '10.92.1.1' }]
        await openForm()
        await choose(screen.getByLabelText('Mode'), 'vnet')
        await choose(await screen.findByLabelText('On'), '')
        await type(screen.getByLabelText('Bridge'), 'bridge0')
        await type(screen.getByLabelText('Address'), '10.0.0.5/24')
        expect((await submit()).network).toEqual({ mode: 'vnet', bridge: 'bridge0', ip4: '10.0.0.5/24' })
    })

    it('sends the minimal spec for a form left as it opened', async () => {
        await openForm()
        expect(await submit()).toEqual({
            name: 'web1',
            template: 'tank/jails/templates/15.1-RELEASE@base',
            network: { mode: 'disable' },
        })
    })

    // v0.9.0, bug 1: paramValues was typed Record<string, string> while bound
    // to <input type="number">. The input binds a number, so .trim() threw on
    // the first parameter anybody touched -- and an emptied one binds null.
    it('sends a number parameter as the number the input bound', async () => {
        await openForm()
        await fireEvent.click(screen.getByRole('button', { name: 'Set parameters' }))
        await type(screen.getByLabelText('securelevel'), '3')
        await type(screen.getByLabelText('enforce_statfs'), '2')
        expect((await submit()).params).toEqual({ securelevel: 3, enforce_statfs: 2 })
    })

    it('sends nothing for a number parameter typed and then emptied', async () => {
        await openForm()
        await fireEvent.click(screen.getByRole('button', { name: 'Set parameters' }))
        const securelevel = screen.getByLabelText('securelevel')
        await type(securelevel, '3')
        await type(securelevel, '')
        expect(await submit()).not.toHaveProperty('params')
    })

    it('sends a flag as a boolean and a choice as its string', async () => {
        await openForm()
        await fireEvent.click(screen.getByRole('button', { name: 'Set parameters' }))
        await choose(screen.getByLabelText('allow.chflags'), 'true')
        await choose(screen.getByLabelText('sysvshm'), 'new')
        expect((await submit()).params).toEqual({ 'allow.chflags': true, sysvshm: 'new' })
    })

    // v0.9.0, bug 2: rows keyed by index. Remove the first of three and every
    // row's identity shifts under its bindings.
    it('removing the first of three tags sends exactly the other two', async () => {
        await openForm()
        await fireEvent.click(screen.getByRole('button', { name: 'Add storage' }))
        const add = screen.getByRole('button', { name: 'Add a tag' })
        await fireEvent.click(add)
        await fireEvent.click(add)
        await fireEvent.click(add)

        const names = screen.getAllByLabelText('Tag')
        const values = screen.getAllByLabelText('Value')
        for (const [i, which] of ['a', 'b', 'c'].entries()) {
            await type(names[i], `otter:${which}`)
            await type(values[i], `value-${which}`)
        }
        await fireEvent.click(screen.getAllByRole('button', { name: 'Remove' })[0])

        // What is on screen follows the rows that are left...
        expect(screen.getAllByLabelText('Tag').map((e) => (e as HTMLInputElement).value))
            .toEqual(['otter:b', 'otter:c'])
        expect(screen.getAllByLabelText('Value').map((e) => (e as HTMLInputElement).value))
            .toEqual(['value-b', 'value-c'])
        // ...and so does what is sent.
        expect((await submit()).properties).toEqual({ 'otter:b': 'value-b', 'otter:c': 'value-c' })
    })

    // What keying by index actually breaks, measured: not the values. Svelte 5
    // re-points each reused element's binding at the row now in its place, so
    // the screen and the payload are right either way. What moves is element
    // identity -- the input that held row b holds row c afterwards -- and a
    // person sees that as their focus, caret or half-typed word jumping to a
    // different row.
    it('the input that held a row still holds that row after the one above is removed', async () => {
        await openForm()
        await fireEvent.click(screen.getByRole('button', { name: 'Add storage' }))
        const add = screen.getByRole('button', { name: 'Add a tag' })
        await fireEvent.click(add)
        await fireEvent.click(add)
        await fireEvent.click(add)
        const names = screen.getAllByLabelText('Tag')
        for (const [i, which] of ['a', 'b', 'c'].entries()) await type(names[i], `otter:${which}`)

        const rowB = names[1] as HTMLInputElement
        rowB.focus()
        await fireEvent.click(screen.getAllByRole('button', { name: 'Remove' })[0])

        expect(rowB.isConnected).toBe(true)
        expect(rowB.value).toBe('otter:b')
    })

    it('removing the first of three datasets sends exactly the other two', async () => {
        await openForm()
        await fireEvent.click(screen.getByRole('button', { name: 'Add storage' }))
        const add = screen.getByRole('button', { name: 'Add a dataset' })
        await fireEvent.click(add)
        await fireEvent.click(add)
        await fireEvent.click(add)

        const datasets = screen.getAllByLabelText('Dataset')
        const quotas = screen.getAllByLabelText('refquota')
        for (const [i, which] of ['a', 'b', 'c'].entries()) {
            await type(datasets[i], which)
            await type(quotas[i], `${i + 1}G`)
        }
        await fireEvent.click(screen.getAllByRole('button', { name: 'Remove' })[0])

        expect((await submit()).datasets).toEqual([
            { name: 'b', properties: { refquota: '2G' } },
            { name: 'c', properties: { refquota: '3G' } },
        ])
    })

    // v0.9.0's other one: Number('abc') is NaN, JSON writes it as null, and
    // the server reads null as no limit -- a typo became an unlimited jail.
    it('sends a mistyped numeric limit as typed, so the API refuses it by name', async () => {
        await openForm()
        await fireEvent.click(screen.getByRole('button', { name: 'Set limits' }))
        await type(screen.getByLabelText('maxproc'), '64x')
        const spec = await submit()
        expect(spec.limits).toEqual({ maxproc: '64x' })
        expect(JSON.stringify(spec)).not.toContain('null')
    })
})
