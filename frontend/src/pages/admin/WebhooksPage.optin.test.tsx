import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// New webhook events (#630). The follow events are sent only to a hook that
// lists them by name, so "All events" must neither tick them nor hide them.

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
  created_at: '2026-01-02T00:00:00Z',
  payload_format: 'raw',
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'admin@example.com',
      display_name: 'Ada Admin',
      role: 'admin',
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

async function renderPage(hooks: unknown[] = []) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/webhooks') return Promise.resolve({ data: hooks })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {} } as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
  renderWithQuery(<WebhooksPage />)
  await screen.findByRole('heading', { level: 1, name: 'Webhooks' })
  return { post, patch }
}

const createForm = () => within(screen.getByRole('form', { name: 'New webhook' }))

async function openEditor(url: string) {
  const cell = await screen.findByText(url)
  const r = cell.closest('tr') as HTMLElement
  await userEvent.click(within(r).getByRole('button', { name: 'Edit' }))
  return within(screen.getByRole('form', { name: 'Edit webhook' }))
}

describe('the new ordinary events', () => {
  it('offers ticket.unlinked and ticket.attachment_added, which All events covers', async () => {
    await renderPage()
    const form = createForm()
    await userEvent.click(form.getByRole('checkbox', { name: 'All events' }))
    for (const name of ['ticket.unlinked', 'ticket.attachment_added']) {
      const box = form.getByRole('checkbox', { name }) as HTMLInputElement
      expect(box.checked).toBe(true)
      expect(box.disabled).toBe(true)
    }
  })

  it('sends them by name when chosen, in the server order', async () => {
    const { post } = await renderPage()
    const form = createForm()
    await userEvent.type(form.getByLabelText('Webhook URL'), 'https://example.com/hook')
    await userEvent.click(form.getByRole('checkbox', { name: 'ticket.attachment_added' }))
    await userEvent.click(form.getByRole('checkbox', { name: 'ticket.unlinked' }))
    await userEvent.click(form.getByRole('button', { name: /create/i }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0][1]).toMatchObject({ events: ['ticket.unlinked', 'ticket.attachment_added'] })
  })
})

describe('the follow events, which "All events" does not include', () => {
  it('leaves them unticked and tickable under All events', async () => {
    await renderPage()
    const form = createForm()
    await userEvent.click(form.getByRole('checkbox', { name: 'All events' }))
    for (const name of ['ticket.followed', 'ticket.unfollowed']) {
      const box = form.getByRole('checkbox', { name }) as HTMLInputElement
      expect(box.checked).toBe(false)
      expect(box.disabled).toBe(false)
    }
  })

  it('sends "*" with the follow events by name when both are chosen', async () => {
    const { post } = await renderPage()
    const form = createForm()
    await userEvent.type(form.getByLabelText('Webhook URL'), 'https://example.com/hook')
    await userEvent.click(form.getByRole('checkbox', { name: 'All events' }))
    await userEvent.click(form.getByRole('checkbox', { name: 'ticket.unfollowed' }))
    await userEvent.click(form.getByRole('checkbox', { name: 'ticket.followed' }))
    await userEvent.click(form.getByRole('button', { name: /create/i }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0][1]).toMatchObject({ events: ['*', 'ticket.followed', 'ticket.unfollowed'] })
  })

  it('sends only "*" when no follow event is ticked, as before', async () => {
    const { post } = await renderPage()
    const form = createForm()
    await userEvent.type(form.getByLabelText('Webhook URL'), 'https://example.com/hook')
    await userEvent.click(form.getByRole('checkbox', { name: 'All events' }))
    await userEvent.click(form.getByRole('button', { name: /create/i }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0][1]).toMatchObject({ events: ['*'] })
  })

  it('lets a follow event be the only subscription', async () => {
    const { post } = await renderPage()
    const form = createForm()
    await userEvent.type(form.getByLabelText('Webhook URL'), 'https://example.com/hook')
    await userEvent.click(form.getByRole('checkbox', { name: 'ticket.followed' }))
    await userEvent.click(form.getByRole('button', { name: /create/i }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0][1]).toMatchObject({ events: ['ticket.followed'] })
  })

  it('shows a hook on "*" plus a follow event with both, and saves it unchanged', async () => {
    const hook = { ...HOOK, events: ['*', 'ticket.followed'] }
    const { patch } = await renderPage([hook])

    const r = (await screen.findByText(hook.url)).closest('tr') as HTMLElement
    expect(within(r).getByText('All events')).toBeTruthy()
    expect(within(r).getByText('ticket.followed')).toBeTruthy()

    const form = await openEditor(hook.url)
    expect((form.getByRole('checkbox', { name: 'All events' }) as HTMLInputElement).checked).toBe(true)
    expect((form.getByRole('checkbox', { name: 'ticket.followed' }) as HTMLInputElement).checked).toBe(true)
    expect((form.getByRole('checkbox', { name: 'ticket.unfollowed' }) as HTMLInputElement).checked).toBe(false)
    await userEvent.click(form.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch.mock.calls[0][1]).toMatchObject({ events: ['*', 'ticket.followed'] })
  })

  it('keeps a stored follow event next to the other names, and saves them unchanged', async () => {
    const hook = { ...HOOK, events: ['ticket.created', 'ticket.unfollowed'] }
    const { patch } = await renderPage([hook])
    const form = await openEditor(hook.url)

    expect((form.getByRole('checkbox', { name: 'ticket.unfollowed' }) as HTMLInputElement).checked).toBe(true)
    await userEvent.click(form.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch.mock.calls[0][1]).toMatchObject({ events: ['ticket.created', 'ticket.unfollowed'] })
  })
})
