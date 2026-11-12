import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// Turning ticket scope enforcement on can leave an OAuth client seeing nothing
// (#420): its service user is in no group. The server does not refuse; it
// answers the PATCH 200 {"warning": "..."} naming the clients. The page used
// to throw the body away, so the warning never reached the administrator.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { SettingsPage } from './SettingsPage'

const WARNING =
  'Client JIRA sync, CI pipeline will see no tickets until their user is added to a group.'

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin',
      mfa_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: { ticket_scope_enforced: false } })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})

async function saveWith(patchBody: unknown, change: () => Promise<void>) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: patchBody } as any)
  renderWithQuery(<SettingsPage />)
  await screen.findByRole('heading', { name: 'Settings' })
  await change()
  await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
  await waitFor(() => expect(patch).toHaveBeenCalled())
  return patch
}

async function turnScopeOnAndSave(patchBody: unknown) {
  return saveWith(patchBody, async () => {
    const row = screen.getByText('Limit staff to their group scope').parentElement!.parentElement!
    await userEvent.click(within(row).getByRole('switch'))
  })
}

describe('the scope enforcement save warning', () => {
  it('shows the warning the server returns, and links to the OAuth clients page', async () => {
    const patch = await turnScopeOnAndSave({ warning: WARNING })
    expect(patch).toHaveBeenCalledWith('/admin/settings', { ticket_scope_enforced: true })

    const note = await screen.findByText(WARNING)
    const box = note.closest('[role="status"]') as HTMLElement
    expect(box).toBeTruthy()
    expect(within(box).getByRole('link', { name: /OAuth clients/i }).getAttribute('href')).toBe(
      '/admin/oauth-clients',
    )
  })

  // An SSO warning shares the field. It must not send the administrator to the
  // OAuth clients page.
  it('links to OAuth clients only on the save that turned scope on', async () => {
    await saveWith({ warning: 'SSO would lock everyone out.' }, async () => {
      await userEvent.click(screen.getByRole('switch', { name: 'Staff can view ticket change history' }))
    })
    const note = await screen.findByText('SSO would lock everyone out.')
    const box = note.closest('[role="status"]') as HTMLElement
    expect(within(box).queryByRole('link')).toBeNull()
  })

  it('says nothing extra when the server returns no warning', async () => {
    await turnScopeOnAndSave(undefined)
    await screen.findByText('Saved.')
    expect(screen.queryByRole('status')).toBeNull()
  })
})
