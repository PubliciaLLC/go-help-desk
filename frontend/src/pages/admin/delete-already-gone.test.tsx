import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, AxiosHeaders } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// Deleting an id that does not exist answers 404 (#418). For a delete that
// someone else already did, that is the outcome the person wanted: the dialog
// must close and the list refresh. Any other failure must say so, not leave
// the dialog open in silence.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/api-keys' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { APIKeysPage } from './APIKeysPage'

const KEY = { id: 'k-1', name: 'ci-key', scopes: ['tickets:read'], created_at: '2026-01-01T00:00:00Z' }

function refusal(status: number, code: string, message: string) {
  const headers = new AxiosHeaders()
  return new AxiosError('Request failed', 'ERR_BAD_REQUEST', { headers } as never, null, {
    status,
    statusText: 'x',
    headers,
    config: { headers } as never,
    data: { error: { code, message } },
  } as never)
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'a@example.com', display_name: 'Ada', role: 'admin', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

async function openRevokeDialog(deleteResult: Promise<unknown> | (() => Promise<unknown>)) {
  const get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/api-keys') return Promise.resolve({ data: [KEY] })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const del = vi.spyOn(api, 'delete').mockImplementation((() => (typeof deleteResult === 'function' ? deleteResult() : deleteResult)) as any)
  renderWithQuery(<APIKeysPage />)
  await userEvent.click(await screen.findByRole('button', { name: 'Revoke' }))
  const dialog = await screen.findByRole('dialog')
  return { get, del, dialog }
}

describe('revoking an API key that is already gone', () => {
  it('closes the dialog and refreshes the list on 404', async () => {
    const { get, del, dialog } = await openRevokeDialog(() => Promise.reject(refusal(404, 'not_found', 'API key k-1: not found')))
    const listCallsBefore = get.mock.calls.filter((c) => c[0] === '/admin/api-keys').length

    await userEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(del).toHaveBeenCalledWith('/admin/api-keys/k-1')
    await waitFor(() =>
      expect(get.mock.calls.filter((c) => c[0] === '/admin/api-keys').length).toBeGreaterThan(listCallsBefore),
    )
  })

  it('keeps the dialog open and shows the server message on any other failure', async () => {
    const { dialog } = await openRevokeDialog(() => Promise.reject(refusal(500, 'internal_error', 'database unavailable')))

    await userEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }))

    expect(await within(dialog).findByText('database unavailable')).toBeTruthy()
    expect(screen.queryByRole('dialog')).not.toBeNull()
  })
})
