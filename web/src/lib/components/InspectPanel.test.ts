import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { createRawSnippet } from 'svelte'
import InspectPanel from './InspectPanel.svelte'
import type { Inspected, InspectFile } from './InspectPanel.svelte'

const absent = createRawSnippet((file: () => InspectFile) => ({
    render: () => `<span>no ${file().name} here</span>`,
}))

function panel(answers: (Inspected | Error)[]) {
    let reads = 0
    const view = render(InspectPanel, {
        noun: 'guest',
        absent,
        read: async () => {
            const answer = answers[Math.min(reads++, answers.length - 1)]
            if (answer instanceof Error) throw answer
            return answer
        },
    })
    return { ...view, reads: () => reads }
}

const guest: Inspected = {
    files: [
        { name: 'web.conf', path: '/vm/web/web.conf', present: true, content: 'cpu=1\nmemory=256M\n' },
        {
            name: 'user-data', path: '/vm/web/.cloud-init/user-data', present: true,
            content: '#cloud-config\npassword: (masked)\n', masked: ['password'],
        },
        { name: 'network-config', path: '/vm/web/.cloud-init/network-config', present: false },
    ],
}

describe('InspectPanel', () => {
    it('reads nothing until it is opened', async () => {
        const { reads } = panel([guest])
        expect(reads()).toBe(0)
        expect(screen.queryByText('/vm/web/web.conf')).toBeNull()

        await fireEvent.click(screen.getByRole('button', { name: 'Show the files' }))
        await screen.findByText('/vm/web/web.conf')
        expect(reads()).toBe(1)
        expect(screen.getByText(/memory=256M/)).toBeTruthy()
        expect(screen.getByText(/Masked: the value of/)).toBeTruthy()
        expect(screen.getByText('no network-config here')).toBeTruthy()
    })

    it('reads the host again on Reload, and not when opened again', async () => {
        const changed = { files: [{ ...guest.files[0], content: 'cpu=4\n' }] }
        const { reads } = panel([guest, changed])
        await fireEvent.click(screen.getByRole('button', { name: 'Show the files' }))
        await screen.findByText(/memory=256M/)
        await fireEvent.click(screen.getByRole('button', { name: 'Reload' }))
        await screen.findByText(/cpu=4/)
        await fireEvent.click(screen.getByRole('button', { name: 'Hide' }))
        await fireEvent.click(screen.getByRole('button', { name: 'Show the files' }))
        expect(reads()).toBe(2)
    })

    it('says what went wrong', async () => {
        panel([new Error('vm-bhyve is not configured')])
        await fireEvent.click(screen.getByRole('button', { name: 'Show the files' }))
        expect((await screen.findByRole('alert')).textContent).toContain('vm-bhyve is not configured')
    })

    it("shows a jail's rules, none, or why they could not be read", async () => {
        const stanza = { name: 'db.conf', path: '/etc/jail.conf.d/db.conf', present: true, content: 'db {\n}\n' }
        for (const [answer, text] of [
            [{ files: [stanza], rules: ['jail:db:pcpu:deny=200'] }, 'jail:db:pcpu:deny=200'],
            [{ files: [stanza], rules: [] }, "None keyed to this jail's name."],
            [{ files: [stanza], rules: null, rulesError: 'RACCT disabled' }, 'Could not read them: RACCT disabled'],
        ] as [Inspected, string][]) {
            const { unmount } = panel([answer])
            await fireEvent.click(screen.getByRole('button', { name: 'Show the files' }))
            await waitFor(() => expect(screen.getByText(text)).toBeTruthy())
            unmount()
        }
    })
})
