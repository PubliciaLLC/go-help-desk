import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #450: a webhook made by an API key that has since been revoked or has expired
// is paused. The page says so, with the reason, and a signed-in administrator
// can re-approve it.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/webhooks' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { WebhooksPage } from './WebhooksPage'

const LIVE = {
  id: 'wh-1', url: 'https://example.com/live', events: ['*'], enabled: true,
  created_at: '2026-01-01T00:00:00Z', payload_format: 'raw', paused: false,
}
const PAUSED = {
  id: 'wh-2', url: 'https://example.com/orphan', events: ['*'], enabled: true,
  created_at: '2026-01-02T00:00:00Z', payload_format: 'raw', paused: true,
  paused_reason: 'Created by a revoked or expired API key',
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

async function renderPage() {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/webhooks') return Promise.resolve({ data: [LIVE, PAUSED] })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const post = vi.spyOn(api, 'post').mockResolvedValue({ data: undefined } as any)
  renderWithQuery(<WebhooksPage />)
  await screen.findByRole('heading', { level: 1, name: 'Webhooks' })
  return post
}

const rowOf = async (url: string) => (await screen.findByText(url)).closest('tr') as HTMLElement

describe('a paused webhook', () => {
  it('shows a Paused badge with the reason, and only on the paused one', async () => {
    await renderPage()
    const paused = await rowOf(PAUSED.url)
    expect(within(paused).getByText('Paused')).toBeTruthy()
    expect(within(paused).getByText(/created by a revoked or expired api key/i)).toBeTruthy()
    expect(within(await rowOf(LIVE.url)).queryByText('Paused')).toBeNull()
  })

  it('offers Re-approve only on the paused one, and posts to its route', async () => {
    const post = await renderPage()
    expect(within(await rowOf(LIVE.url)).queryByRole('button', { name: 'Re-approve' })).toBeNull()

    await userEvent.click(within(await rowOf(PAUSED.url)).getByRole('button', { name: 'Re-approve' }))
    await waitFor(() => expect(post).toHaveBeenCalledWith('/admin/webhooks/wh-2/reapprove'))
  })
})
