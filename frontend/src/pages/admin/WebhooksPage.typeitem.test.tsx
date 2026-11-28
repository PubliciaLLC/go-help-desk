import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The webhook filter's Type and Item conditions (#631). They sit under the
// ticked categories and types, and the server refuses a type outside the
// ticked categories (D37), so unticking a parent must drop its children.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/webhooks' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { WebhooksPage } from './WebhooksPage'

const HOOK = {
  id: 'wh-1', url: 'https://example.com/ingest', events: ['*'], enabled: true,
  created_at: '2026-01-02T00:00:00Z', payload_format: 'raw',
}
const CATEGORIES = [
  { id: 'c-hw', name: 'Hardware', active: true },
  { id: 'c-fac', name: 'Facilities', active: true },
]
const TYPES: Record<string, unknown[]> = {
  'c-hw': [{ id: 't-lap', category_id: 'c-hw', name: 'Laptop', sort_order: 1, active: true }],
  'c-fac': [{ id: 't-door', category_id: 'c-fac', name: 'Door', sort_order: 1, active: true }],
}
const ITEMS: Record<string, unknown[]> = {
  't-lap': [{ id: 'i-bat', type_id: 't-lap', name: 'Battery', sort_order: 1, active: true }],
  't-door': [{ id: 'i-lock', type_id: 't-door', name: 'Lock', sort_order: 1, active: true }],
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin',
      mfa_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

async function renderPage(hooks: unknown[] = [HOOK]) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/webhooks') return Promise.resolve({ data: hooks })
    if (url === '/admin/categories') return Promise.resolve({ data: CATEGORIES })
    const types = /^\/admin\/categories\/([^/]+)\/types$/.exec(url)
    if (types) return Promise.resolve({ data: TYPES[types[1]] ?? [] })
    const items = /^\/admin\/categories\/[^/]+\/types\/([^/]+)\/items$/.exec(url)
    if (items) return Promise.resolve({ data: ITEMS[items[1]] ?? [] })
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

describe('webhook filter: type and item', () => {
  it('offers the types of a ticked category, then the items of a ticked type', async () => {
    await renderPage()
    const form = createForm()
    expect(form.queryByRole('checkbox', { name: 'Hardware › Laptop' })).toBeNull()

    await userEvent.click(await form.findByRole('checkbox', { name: 'Hardware' }))
    await userEvent.click(await form.findByRole('checkbox', { name: 'Hardware › Laptop' }))
    expect(await form.findByRole('checkbox', { name: 'Laptop › Battery' })).toBeTruthy()
    expect(form.queryByRole('checkbox', { name: 'Facilities › Door' })).toBeNull()
  })

  it('sends the ticked types and items when creating', async () => {
    const { post } = await renderPage()
    const form = createForm()
    await userEvent.type(form.getByLabelText('Webhook URL'), 'https://example.com/hook')
    await userEvent.click(form.getByRole('checkbox', { name: 'All events' }))
    await userEvent.click(await form.findByRole('checkbox', { name: 'Hardware' }))
    await userEvent.click(await form.findByRole('checkbox', { name: 'Hardware › Laptop' }))
    await userEvent.click(await form.findByRole('checkbox', { name: 'Laptop › Battery' }))
    await userEvent.click(form.getByRole('button', { name: /create/i }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0][1]).toMatchObject({
      filter: { category_ids: ['c-hw'], type_ids: ['t-lap'], item_ids: ['i-bat'], invert: false },
    })
  })

  it('unticking a category drops the types and items under it, so the server is not sent a contradiction', async () => {
    const { post } = await renderPage()
    const form = createForm()
    await userEvent.type(form.getByLabelText('Webhook URL'), 'https://example.com/hook')
    await userEvent.click(form.getByRole('checkbox', { name: 'All events' }))
    await userEvent.click(await form.findByRole('checkbox', { name: 'Hardware' }))
    await userEvent.click(await form.findByRole('checkbox', { name: 'Hardware › Laptop' }))
    await userEvent.click(await form.findByRole('checkbox', { name: 'Laptop › Battery' }))
    await userEvent.click(form.getByRole('checkbox', { name: 'Facilities' }))
    await userEvent.click(form.getByRole('checkbox', { name: 'Hardware' }))
    await userEvent.click(form.getByRole('checkbox', { name: 'critical' }))
    await userEvent.click(form.getByRole('button', { name: /create/i }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    const filter = (post.mock.calls[0][1] as { filter: Record<string, unknown> }).filter
    expect(filter.category_ids).toEqual(['c-fac'])
    expect(filter.type_ids).toBeUndefined()
    expect(filter.item_ids).toBeUndefined()
  })

  it('unticking a type drops the items under it', async () => {
    const { post } = await renderPage()
    const form = createForm()
    await userEvent.type(form.getByLabelText('Webhook URL'), 'https://example.com/hook')
    await userEvent.click(form.getByRole('checkbox', { name: 'All events' }))
    await userEvent.click(await form.findByRole('checkbox', { name: 'Hardware' }))
    await userEvent.click(await form.findByRole('checkbox', { name: 'Hardware › Laptop' }))
    await userEvent.click(await form.findByRole('checkbox', { name: 'Laptop › Battery' }))
    await userEvent.click(form.getByRole('checkbox', { name: 'Hardware › Laptop' }))
    await userEvent.click(form.getByRole('button', { name: /create/i }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    const filter = (post.mock.calls[0][1] as { filter: Record<string, unknown> }).filter
    expect(filter.type_ids).toBeUndefined()
    expect(filter.item_ids).toBeUndefined()
  })

  it('a filter with only a type counts as a filter on edit, and is sent back', async () => {
    const stored = { category_ids: [], priorities: [], group_ids: [], type_ids: ['t-lap'], invert: false }
    const { patch } = await renderPage([{ ...HOOK, filter: stored }])
    const r = (await screen.findByText(HOOK.url)).closest('tr') as HTMLElement
    expect(within(r).queryByText('All tickets')).toBeNull()
    expect(within(r).getByText('Type: 1 selected')).toBeTruthy()

    await userEvent.click(within(r).getByRole('button', { name: 'Edit' }))
    const form = within(screen.getByRole('form', { name: 'Edit webhook' }))
    expect((await form.findByRole('checkbox', { name: 'Hardware › Laptop' }) as HTMLInputElement).checked).toBe(true)
    await userEvent.click(form.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch.mock.calls[0][1]).toMatchObject({ filter: { type_ids: ['t-lap'] } })
  })

  it('summarises types and items in the list', async () => {
    await renderPage([{ ...HOOK, filter: { category_ids: [], priorities: [], group_ids: [], type_ids: ['t-lap', 't-door'], item_ids: ['i-bat'], invert: false } }])
    const r = (await screen.findByText(HOOK.url)).closest('tr') as HTMLElement
    expect(within(r).getByText('Type: 2 selected')).toBeTruthy()
    expect(within(r).getByText('Item: 1 selected')).toBeTruthy()
  })
})
