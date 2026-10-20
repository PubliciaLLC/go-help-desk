import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #441: the admin-wide audit page labels a follow removed by a reassignment
// the way the ticket's own feed does, with whose follow it was, not as its
// actor (the person who reassigned) stopping.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/audit' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { AdminAuditPage } from './AdminAuditPage'

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: 'staff-1', email: 's@example.com', display_name: 'Sam Staff', role: 'staff', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})
afterEach(() => vi.restoreAllMocks())

function serve(entries: unknown[]) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/audit') return Promise.resolve({ data: { entries, total: entries.length, total_capped: false, has_more: false } })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

const base = { entity_type: 'ticket', entity_id: 't-1', created_at: '2026-10-14T10:00:00Z' }

describe('follow entries on the admin audit page', () => {
  it('labels a reassignment removal with whose follow it was', async () => {
    serve([
      {
        ...base, id: 'e1', action: 'unfollowed', actor_id: 'u-admin', actor_name: 'Ada Admin',
        before: { assignee_user_id: 'u-sam' }, after: { assignee_user_id: 'u-other' },
        names: { 'u-sam': { name: 'Sam Staff' } },
      },
    ])
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('Follow removed on reassignment (Sam Staff)')).toBeTruthy())
    expect(screen.queryByText('Unfollowed')).toBeNull()
  })

  it('leaves an ordinary unfollow and every other action as they were', async () => {
    serve([
      { ...base, id: 'e1', action: 'unfollowed', actor_id: 'u1', actor_name: 'Sam' },
      { ...base, id: 'e2', action: 'resolved', actor_id: 'u1', actor_name: 'Sam' },
    ])
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('Resolved')).toBeTruthy())
    expect(screen.getByText('Unfollowed')).toBeTruthy()
  })
})

describe('reply and note entries on the admin audit page', () => {
  it('labels them and does not print their diff', async () => {
    serve([
      { ...base, id: 'e1', action: 'reply_added', actor_id: 'u1', actor_name: 'Sam', after: { reply_id: 'r-1', internal: false } },
      { ...base, id: 'e2', action: 'note_added', actor_id: 'u1', actor_name: 'Sam', after: { reply_id: 'r-2', internal: true } },
      { ...base, id: 'e3', action: 'reply_added', actor_id: null },
      { ...base, id: 'e4', action: 'note_added', actor_id: 'u-gone' },
    ])
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getAllByText('Reply added')).toHaveLength(2))
    expect(screen.getAllByText('Internal note added')).toHaveLength(2)
    expect(screen.getByText('Someone')).toBeTruthy()
    expect(screen.getByText('A guest')).toBeTruthy()
    expect(screen.queryByText(/r-1/)).toBeNull()
  })
})

describe('hiding replies and notes on the admin audit page', () => {
  it('asks the server to leave them out, and stops when unticked', async () => {
    const get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
      if (url === '/admin/audit') return Promise.resolve({ data: { entries: [], total: 0, total_capped: false, has_more: false } })
      return Promise.resolve({ data: [] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    const paramsOf = () => get.mock.calls.filter((c) => c[0] === '/admin/audit').map((c) => (c[1] as { params: Record<string, unknown> }).params)
    renderWithQuery(<AdminAuditPage />)
    await waitFor(() => expect(paramsOf().length).toBeGreaterThan(0))
    expect(paramsOf().at(-1)?.exclude_action).toBeUndefined()

    const box = screen.getByLabelText('Hide replies and notes')
    box.click()
    await waitFor(() => expect(paramsOf().at(-1)?.exclude_action).toEqual(['reply_added', 'note_added']))

    box.click()
    await waitFor(() => expect(paramsOf().at(-1)?.exclude_action).toBeUndefined())
  })
})
