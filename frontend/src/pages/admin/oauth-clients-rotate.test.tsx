import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// Rotating a client's secret (#622): a confirmation first, because the old
// secret stops working at once, then the new secret shown once in the banner.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/oauth-clients' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { OAuthClientsPage } from './OAuthClientsPage'

const client = {
  id: 'c-1', client_id: 'cid-1', name: 'JIRA sync', scopes: ['tickets:read'],
  created_at: '2026-01-01T00:00:00Z', user_id: 'u-9', groups: [],
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-0', email: 'admin@example.com', display_name: 'An Admin', role: 'admin',
      mfa_enabled: true, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
  vi.spyOn(api, 'get').mockImplementation(((url: string) =>
    Promise.resolve({ data: url === '/admin/oauth-clients' ? [client] : [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ) as any)
})

describe('Rotate secret', () => {
  it('asks first, and sends nothing until confirmed', async () => {
    const post = vi.spyOn(api, 'post')
    renderWithQuery(<OAuthClientsPage />)
    const tr = (await screen.findByText('cid-1')).closest('tr')!
    await userEvent.click(within(tr).getByRole('button', { name: /rotate secret/i }))

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/stops working immediately/i)).toBeTruthy()
    expect(post).not.toHaveBeenCalled()

    await userEvent.click(within(dialog).getByRole('button', { name: /cancel/i }))
    expect(post).not.toHaveBeenCalled()
  })

  it('shows the new secret once the rotation is confirmed', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({
      data: { id: 'c-1', client_id: 'cid-1', client_secret: 'brand-new-secret' },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any)
    renderWithQuery(<OAuthClientsPage />)
    const tr = (await screen.findByText('cid-1')).closest('tr')!
    await userEvent.click(within(tr).getByRole('button', { name: /rotate secret/i }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: /^rotate$/i }))

    await waitFor(() => expect(post).toHaveBeenCalledWith('/admin/oauth-clients/c-1/rotate-secret'))
    expect(await screen.findByText('brand-new-secret')).toBeTruthy()
    expect(screen.getByText(/secret rotated/i)).toBeTruthy()
  })

  it('says so inside the dialog when the server refuses', async () => {
    vi.spyOn(api, 'post').mockRejectedValue({
      isAxiosError: true, response: { status: 404, data: { error: { code: 'not_found', message: 'gone' } } },
    })
    renderWithQuery(<OAuthClientsPage />)
    const tr = (await screen.findByText('cid-1')).closest('tr')!
    await userEvent.click(within(tr).getByRole('button', { name: /rotate secret/i }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: /^rotate$/i }))
    expect(await within(await screen.findByRole('dialog')).findByRole('alert')).toBeTruthy()
  })
})
