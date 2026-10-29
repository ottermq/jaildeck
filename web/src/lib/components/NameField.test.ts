import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import NameField from './NameField.svelte'

let suggestions: (string | Error)[]
let resolveLate: ((name: string) => void) | null = null

vi.mock('../stores/names.svelte', () => ({
    suggestName: vi.fn(async () => {
        const next = suggestions.shift()
        if (next === 'late') return new Promise<string>(r => (resolveLate = r))
        if (next instanceof Error) throw next
        return next ?? 'brave-badger'
    }),
}))

beforeEach(() => {
    suggestions = []
    resolveLate = null
})

const field = () => screen.getByRole('textbox') as HTMLInputElement

describe('the name field', () => {
    it('opens with a suggestion, and draws another on request', async () => {
        suggestions = ['jolly-otter', 'calm-heron']
        render(NameField, { id: 'name' })
        await waitFor(() => expect(field().value).toBe('jolly-otter'))
        await fireEvent.click(screen.getByRole('button', { name: 'Suggest another name' }))
        await waitFor(() => expect(field().value).toBe('calm-heron'))
    })

    it('keeps a name given to it, and asks for nothing', async () => {
        suggestions = ['jolly-otter']
        render(NameField, { id: 'name', value: 'db1' })
        await new Promise(r => setTimeout(r, 20))
        expect(field().value).toBe('db1')
        expect(suggestions).toEqual(['jolly-otter'])
    })

    it('does not overwrite what was typed while the first suggestion was on its way', async () => {
        suggestions = ['late']
        render(NameField, { id: 'name' })
        await fireEvent.input(field(), { target: { value: 'my-own' } })
        await waitFor(() => expect(resolveLate).not.toBeNull())
        resolveLate!('jolly-otter')
        await new Promise(r => setTimeout(r, 20))
        expect(field().value).toBe('my-own')
    })

    it('stays empty, to be typed in, when no suggestion comes', async () => {
        suggestions = [new Error('offline')]
        render(NameField, { id: 'name' })
        await new Promise(r => setTimeout(r, 20))
        expect(field().value).toBe('')
        expect(screen.getByRole('button', { name: 'Suggest another name' })).toBeTruthy()
    })
})
