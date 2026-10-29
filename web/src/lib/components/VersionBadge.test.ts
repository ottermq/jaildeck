import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/svelte'

import VersionBadge from './VersionBadge.svelte'
import AboutJaildeck from './AboutJaildeck.svelte'
import { about } from '../stores/about.svelte'

let answer: Record<string, unknown> | Error
const asked: string[] = []

vi.mock('../services/api', () => ({
    api: vi.fn(async (path: string) => {
        asked.push(path)
        if (answer instanceof Error) throw answer
        return answer
    }),
}))

const release = { version: 'v0.19.0', commit: 'e9960a1b2c3d4e5f', modified: false,
    started: '2026-10-29T03:00:00Z', state_dir: '/var/db/jaildeck' }

// The store reads /about once per page load, so each test starts unread.
beforeEach(() => {
    about.info = null
    asked.length = 0
    answer = release
})

describe('the version badge', () => {
    it('names a release and links to its notes, from /about', async () => {
        render(VersionBadge)
        const link = await screen.findByRole('link', { name: 'v0.19.0' })
        expect(link.getAttribute('href')).toBe('https://github.com/ottermq/jaildeck/releases/tag/v0.19.0')
        expect(asked).toEqual(['/about'])
    })

    it('names a build that is not a release, with no link', async () => {
        answer = { ...release, version: 'v0.6.1-0.20261029050605-4279f936b78e+dirty' }
        render(VersionBadge)
        expect(await screen.findByText('v0.6.1-0.20261029050605-4279f936b78e+dirty')).toBeTruthy()
        expect(screen.queryByRole('link')).toBeNull()
    })

    it('reads /about once for the badge and the Host block together', async () => {
        render(VersionBadge)
        render(AboutJaildeck)
        await screen.findByRole('heading', { name: 'Jail Deck' })
        expect(asked).toEqual(['/about'])
    })
})

describe('the Host page block', () => {
    it('says the version, the commit, when it started and the state directory', async () => {
        answer = { ...release, modified: true }
        render(AboutJaildeck)
        await screen.findByRole('heading', { name: 'Jail Deck' })
        expect(screen.getByRole('link', { name: 'v0.19.0' })).toBeTruthy()
        expect(screen.getByText('e9960a1b2c3d')).toBeTruthy()
        expect(screen.getByText('with uncommitted changes')).toBeTruthy()
        expect(screen.getByText('/var/db/jaildeck')).toBeTruthy()
        expect(screen.getByText(new Date(release.started).toLocaleString())).toBeTruthy()
    })

    it('says when the build stamped no commit', async () => {
        answer = { ...release, version: 'dev', commit: '' }
        render(AboutJaildeck)
        expect(await screen.findByText('not stamped in this build')).toBeTruthy()
        expect(screen.getByText('dev')).toBeTruthy()
    })

    it('shows nothing when /about could not be read', async () => {
        answer = new Error('down')
        const { container } = render(AboutJaildeck)
        await waitFor(() => expect(asked).toEqual(['/about']))
        expect(container.querySelector('section')).toBeNull()
    })
})
