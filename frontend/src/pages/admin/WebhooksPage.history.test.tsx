import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// Delivery history and Redeliver on the webhook admin page (#448).

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/webhooks' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { WebhooksPage } from './WebhooksPage'

const HOOK = {
  id: 'wh-1',
  url: 'https://example.com/ingest',
  events: ['*'],
  enabled: true,
  created_at: '2026-01-01T00:00:00Z',
  payload_format: 'raw',
}

const DELIVERIES = [
  {
    id: 'd-2', event: 'ticket.note_added', started_at: '2026-10-13T12:00:00Z',
    last_attempt_at: '2026-10-13T12:01:30Z', status: 503, result: 'http_status', attempts: 2, retrying: true,
  },
  {
    id: 'd-1', event: 'ticket.replied', started_at: '2026-10-13T11:00:00Z',
    last_attempt_at: '2026-10-13T11:00:00Z', status: 200, result: 'ok', attempts: 1, retrying: false,
  },
]

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin',
      mfa_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

async function renderPage(deliveries: unknown[] = DELIVERIES) {
  const get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/webhooks') return Promise.resolve({ data: [HOOK] })
    if (url === '/admin/webhooks/wh-1/deliveries') return Promise.resolve({ data: deliveries })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const post = vi.spyOn(api, 'post').mockResolvedValue({ data: undefined } as any)
  renderWithQuery(<WebhooksPage />)
  await screen.findByRole('heading', { level: 1, name: 'Webhooks' })
  return { get, post }
}

async function openHistory() {
  const cell = await screen.findByText(HOOK.url)
  const tr = cell.closest('tr') as HTMLElement
  await userEvent.click(within(tr).getByRole('button', { name: 'History' }))
  return screen.findByRole('region', { name: 'Delivery history' })
}

describe('delivery history', () => {
  it('is not fetched until it is asked for', async () => {
    const { get } = await renderPage()
    await screen.findByText(HOOK.url)
    expect(get).not.toHaveBeenCalledWith('/admin/webhooks/wh-1/deliveries')
  })

  it('shows each delivery as an outcome, newest first, with no content', async () => {
    await renderPage()
    const panel = await openHistory()

    const rows = within(panel).getAllByRole('row').slice(1) // minus the header
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByText('ticket.note_added')).toBeTruthy()
    expect(within(rows[0]).getByText(/503/)).toBeTruthy()
    expect(within(rows[0]).getByText(/retrying/i)).toBeTruthy()
    expect(within(rows[0]).getByText('2')).toBeTruthy() // attempts
    expect(within(rows[1]).getByText('ticket.replied')).toBeTruthy()
    expect(within(rows[1]).getByText(/delivered/i)).toBeTruthy()
  })

  // A retry that was dropped (#499) must not keep saying "retrying".
  it.each([
    ['disabled', /hook switched off/i],
    ['unsubscribed', /no longer subscribed/i],
    ['url_changed', /url changed/i],
    ['filter', /filter no longer admits/i],
    ['paused', /its api key, or that key's owner, is no longer active/i],
    ['unbuildable', /body could not be built/i],
  ])('shows a dropped retry with its reason (%s)', async (reason, text) => {
    await renderPage([{
      id: 'd-3', event: 'ticket.replied', started_at: '2026-10-13T12:00:00Z',
      last_attempt_at: '2026-10-13T12:01:30Z', status: 503, result: 'dropped', attempts: 1,
      retrying: false, drop_reason: reason,
    }])
    const panel = await openHistory()
    const row = within(panel).getAllByRole('row')[1]
    expect(within(row).getByText(/dropped/i)).toBeTruthy()
    expect(within(row).getByText(text)).toBeTruthy()
    expect(within(row).queryByText(/retrying/i)).toBeNull()
    expect(within(row).queryByText(/failed/i)).toBeNull()
  })

  it('says so when a hook has no history', async () => {
    await renderPage([])
    const panel = await openHistory()
    expect(within(panel).getByText(/no deliveries yet/i)).toBeTruthy()
  })

  it('redelivers one delivery and says it was queued', async () => {
    const { post } = await renderPage()
    const panel = await openHistory()
    const rows = within(panel).getAllByRole('row').slice(1)

    await userEvent.click(within(rows[1]).getByRole('button', { name: 'Redeliver' }))

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/admin/webhooks/wh-1/deliveries/d-1/redeliver'),
    )
    expect(await within(panel).findByText(/queued for redelivery/i)).toBeTruthy()
  })

  it('says why a redelivery was refused', async () => {
    const { post } = await renderPage()
    post.mockRejectedValueOnce(new Error('boom'))
    const panel = await openHistory()
    const rows = within(panel).getAllByRole('row').slice(1)

    await userEvent.click(within(rows[0]).getByRole('button', { name: 'Redeliver' }))

    expect(await within(panel).findByRole('alert')).toBeTruthy()
  })

  it('offers the new note event for subscription', async () => {
    await renderPage()
    expect(await screen.findAllByRole('checkbox', { name: 'ticket.note_added' })).not.toHaveLength(0)
  })
})
