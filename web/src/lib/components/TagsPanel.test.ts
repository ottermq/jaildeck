import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { createRawSnippet } from 'svelte'
import { ApiError } from '../services/api'
import TagsPanel from './TagsPanel.svelte'

let confirmed: boolean
let asked: { title: string, lines?: string[] }[]

vi.mock('../stores/confirm.svelte', () => ({
    ask: vi.fn(async (c: { title: string, lines?: string[] }) => {
        asked.push(c)
        return confirmed
    }),
}))

const empty = createRawSnippet(() => ({ render: () => '<span>nothing yet</span>' }))

let saved: Record<string, string | null>[]
let failure: Error | null
let reloads: number

function panel(properties: Record<string, string>) {
    return render(TagsPanel, {
        name: 'web',
        noun: 'guest',
        properties,
        save: async (tags: Record<string, string | null>) => {
            saved.push(tags)
            if (failure) throw failure
            return {}
        },
        changed: async () => { reloads++ },
        empty,
    })
}

beforeEach(() => {
    confirmed = true
    asked = []
    saved = []
    failure = null
    reloads = 0
})

describe('TagsPanel', () => {
    it('lists the labels sorted, or says there are none', () => {
        const { unmount } = panel({ 'otter:tier': 'frontend', 'otter:tenant': 'acme' })
        const names = screen.getAllByRole('row').slice(1).map(r => r.querySelector('code')?.textContent)
        expect(names).toEqual(['otter:tenant', 'otter:tier'])
        unmount()
        panel({})
        expect(screen.getByText('nothing yet')).toBeTruthy()
    })

    it('sets a new label with its name trimmed, and refetches', async () => {
        panel({})
        await fireEvent.click(screen.getByRole('button', { name: 'Add a tag' }))
        await fireEvent.input(screen.getByLabelText('Property'), { target: { value: ' otter:plan ' } })
        await fireEvent.input(screen.getByLabelText('Value'), { target: { value: 'gold' } })
        await fireEvent.click(screen.getByRole('button', { name: 'Set' }))
        await waitFor(() => expect(reloads).toBe(1))
        expect(saved).toEqual([{ 'otter:plan': 'gold' }])
        expect(screen.queryByLabelText('Property')).toBeNull()
    })

    it('keeps the form open with the API\'s words when the write is refused', async () => {
        failure = new ApiError(400, 'otter is not a user property: the name has to contain a colon')
        panel({})
        await fireEvent.click(screen.getByRole('button', { name: 'Add a tag' }))
        await fireEvent.input(screen.getByLabelText('Property'), { target: { value: 'otter' } })
        await fireEvent.click(screen.getByRole('button', { name: 'Set' }))
        expect((await screen.findByRole('alert')).textContent).toContain('has to contain a colon')
        expect(screen.getByLabelText('Property')).toBeTruthy()
        expect(reloads).toBe(1)
    })

    it('saves an edit to the empty string as a value, not a removal', async () => {
        panel({ 'otter:tier': 'frontend' })
        await fireEvent.click(screen.getByRole('button', { name: 'Edit' }))
        await fireEvent.input(screen.getByLabelText('Value for otter:tier'), { target: { value: '' } })
        await fireEvent.click(screen.getByRole('button', { name: 'Save' }))
        await waitFor(() => expect(saved).toEqual([{ 'otter:tier': '' }]))
    })

    it('removes with null, after a confirm that names the instance and its kind', async () => {
        panel({ 'otter:tier': 'frontend' })
        await fireEvent.click(screen.getByRole('button', { name: 'Remove' }))
        await waitFor(() => expect(saved).toEqual([{ 'otter:tier': null }]))
        expect(asked[0].title).toBe('Remove otter:tier from web?')
        expect(asked[0].lines?.join(' ')).toContain('finds its guests by this tag')
    })

    it('writes nothing when the remove is cancelled', async () => {
        confirmed = false
        panel({ 'otter:tier': 'frontend' })
        await fireEvent.click(screen.getByRole('button', { name: 'Remove' }))
        await waitFor(() => expect(asked).toHaveLength(1))
        expect(saved).toEqual([])
        expect(reloads).toBe(0)
    })
})
