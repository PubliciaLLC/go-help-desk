import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, AxiosHeaders } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #447: the two auto-assignment fallbacks could only be set with
// PATCH /admin/settings, and the admin guide sent people to a page that did
// not exist.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/routing' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { RoutingPage } from './RoutingPage'

const GROUPS = [
  { id: 'g-1', name: 'Service Desk', description: '' },
  { id: 'g-2', name: 'Networking', description: '' },
]
const STAFF = [
  { id: 's-1', display_name: 'Ada Admin', assignable: true },
  { id: 's-2', display_name: 'Sam Staff', assignable: true },
  { id: 's-3', display_name: 'Sue Suspended', assignable: false },
]

function refusal(message: string) {
  const headers = new AxiosHeaders()
  return new AxiosError('Request failed', 'ERR_BAD_REQUEST', { headers } as never, null, {
    status: 400,
    statusText: 'Bad Request',
    headers,
    config: { headers } as never,
    data: { error: { code: 'invalid_auto_assign', message } },
  } as never)
}

function mockApi(settings: Record<string, unknown> = {}) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: settings })
    if (url === '/admin/groups') return Promise.resolve({ data: GROUPS })
    if (url === '/staff') return Promise.resolve({ data: STAFF })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-admin',
      email: 'admin@example.com',
      display_name: 'An Admin',
      role: 'admin',
      mfa_enabled: true,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

describe('the routing page', () => {
  it('lists the rules in the order they run', async () => {
    mockApi()
    renderWithQuery(<RoutingPage />)
    await screen.findByRole('heading', { level: 1, name: 'Routing' })

    const rules = screen.getAllByRole('listitem').map((li) => li.textContent ?? '')
    expect(rules[0]).toMatch(/group whose scope covers/i)
    expect(rules[1]).toMatch(/catch-all group/i)
    expect(rules[2]).toMatch(/round-robin/i)
    expect(rules[3]).toMatch(/unassigned/i)
  })

  it('shows what is saved now', async () => {
    mockApi({ auto_assign_group_id: 'g-2', auto_assign_user_ids: ['s-2'] })
    renderWithQuery(<RoutingPage />)

    const select = (await screen.findByRole('combobox', { name: 'Catch-all group' })) as HTMLSelectElement
    await waitFor(() => expect(select.value).toBe('g-2'))
    expect((screen.getByRole('checkbox', { name: 'Sam Staff' }) as HTMLInputElement).checked).toBe(true)
    expect((screen.getByRole('checkbox', { name: 'Ada Admin' }) as HTMLInputElement).checked).toBe(false)
  })

  it('offers only people who can be given work, unless they are already in the list', async () => {
    mockApi()
    renderWithQuery(<RoutingPage />)
    await screen.findByRole('checkbox', { name: 'Ada Admin' })
    expect(screen.queryByRole('checkbox', { name: /Sue Suspended/ })).toBeNull()
  })

  it('shows a saved person who cannot take work as ticked and labelled, and lets the admin untick them', async () => {
    // /staff cannot say whether they are suspended or were demoted, so the box
    // must stay usable: a demoted person who could not be removed would make
    // every save fail.
    const patch = mockApi({ auto_assign_user_ids: ['s-1', 's-3'] })
    const user = userEvent.setup()
    renderWithQuery(<RoutingPage />)

    const box = (await screen.findByRole('checkbox', { name: "Sue Suspended (can't take work right now)" })) as HTMLInputElement
    expect(box.checked).toBe(true)
    expect(box.disabled).toBe(false)

    await user.click(box)
    expect(box.checked).toBe(false)
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith('/admin/settings', {
        auto_assign_group_id: '',
        auto_assign_user_ids: ['s-1'],
      }),
    )
  })

  it('saves the group and the people, in the order they were ticked', async () => {
    const patch = mockApi()
    const user = userEvent.setup()
    renderWithQuery(<RoutingPage />)

    await user.selectOptions(await screen.findByRole('combobox', { name: 'Catch-all group' }), 'g-1')
    await user.click(await screen.findByRole('checkbox', { name: 'Sam Staff' }))
    await user.click(screen.getByRole('checkbox', { name: 'Ada Admin' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith('/admin/settings', {
        auto_assign_group_id: 'g-1',
        auto_assign_user_ids: ['s-2', 's-1'],
      }),
    )
    expect(await screen.findByText('Saved.')).toBeTruthy()
  })

  it('turns the catch-all off by saving an empty group', async () => {
    const patch = mockApi({ auto_assign_group_id: 'g-1' })
    const user = userEvent.setup()
    renderWithQuery(<RoutingPage />)

    const select = (await screen.findByRole('combobox', { name: 'Catch-all group' })) as HTMLSelectElement
    await waitFor(() => expect(select.value).toBe('g-1'))
    await user.selectOptions(select, '')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith('/admin/settings', {
        auto_assign_group_id: '',
        auto_assign_user_ids: [],
      }),
    )
  })

  it('sends a suspended person back unchanged, and drops one who no longer exists, saying so', async () => {
    // s-3 is suspended: the server keeps them in the rotation until they are
    // re-enabled. gone-1 no longer exists and the server would refuse it.
    const patch = mockApi({ auto_assign_user_ids: ['s-1', 's-3', 'gone-1'] })
    const user = userEvent.setup()
    renderWithQuery(<RoutingPage />)

    expect(await screen.findByText(/1 saved entry no longer exists/i)).toBeTruthy()
    await user.click(screen.getByRole('checkbox', { name: 'Sam Staff' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith('/admin/settings', {
        auto_assign_group_id: '',
        auto_assign_user_ids: ['s-1', 's-3', 's-2'],
      }),
    )
  })

  it("shows the server's refusal", async () => {
    mockApi()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    vi.spyOn(api, 'patch').mockRejectedValue(refusal('auto_assign_group_id: no group with id g-9') as any)
    const user = userEvent.setup()
    renderWithQuery(<RoutingPage />)

    await user.click(await screen.findByRole('button', { name: 'Save' }))
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('no group with id g-9')
  })
})
