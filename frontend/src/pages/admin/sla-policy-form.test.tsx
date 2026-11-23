import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The SLA policy add and edit form (#671). It used to be one six-column table
// row inside the narrow Features card, with placeholder-only inputs. This file
// pins what replaced it: every field has a visible label and is found by it;
// add, edit and cancel send the right payloads, including the clear flags; one
// shared form sits outside the scrolling table and is re-pointed from one
// policy to another; focus moves to the Name field when the form opens; and the
// empty-state text never shows beside an open form.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { SettingsPage } from './SettingsPage'

const POLICY = {
  id: 'p1',
  name: 'Critical outage',
  priority: 'critical',
  category_id: 'c1',
  response_target_min: 60,
  resolution_target_min: 240,
}

const POLICY_B = {
  id: 'p2',
  name: 'Printer fleet',
  priority: 'low',
  category_id: 'c2',
  response_target_min: 15,
  resolution_target_min: 90,
}

// What the list endpoint returns. Tests reassign it before or after render.
let policies: (typeof POLICY)[]

beforeEach(() => {
  vi.restoreAllMocks()
  policies = [POLICY]
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin',
      mfa_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: { sla_enabled: true } })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/admin/sla/policies') return Promise.resolve({ data: policies })
    if (url === '/admin/categories') {
      return Promise.resolve({
        data: [
          { id: 'c1', name: 'Network', active: true },
          { id: 'c2', name: 'Hardware', active: true },
        ],
      })
    }
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})

async function renderSLA() {
  const { queryClient } = renderWithQuery(<SettingsPage />)
  await screen.findByRole('heading', { name: 'Settings' })
  await userEvent.click(screen.getByRole('button', { name: 'Features' }))
  await screen.findByText('Critical outage')
  return queryClient
}

describe('the SLA policy form', () => {
  it('labels every field so it is reachable by label', async () => {
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: '+ Add policy' }))

    expect(screen.getByLabelText('Policy name')).toBeTruthy()
    expect(screen.getByLabelText('Priority')).toBeTruthy()
    expect(screen.getByLabelText('Category')).toBeTruthy()
    expect(screen.getByLabelText('Response target')).toBeTruthy()
    expect(screen.getByLabelText('Resolution target')).toBeTruthy()
  })

  it('adds a policy with the typed fields', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {} } as any)
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: '+ Add policy' }))

    await userEvent.type(screen.getByLabelText('Policy name'), 'Printer fleet')
    await userEvent.selectOptions(screen.getByLabelText('Priority'), 'high')
    await userEvent.selectOptions(screen.getByLabelText('Category'), 'c1')
    fireEvent.change(screen.getByLabelText('Response target'), { target: { value: '30' } })
    fireEvent.change(screen.getByLabelText('Resolution target'), { target: { value: '120' } })
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith('/admin/sla/policies', {
      name: 'Printer fleet',
      priority: 'high',
      category_id: 'c1',
      response_target_min: 30,
      resolution_target_min: 120,
    })
  })

  it('edit pre-fills the fields and sends the changed policy', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))

    const name = screen.getByLabelText('Policy name') as HTMLInputElement
    expect(name.value).toBe('Critical outage')
    expect((screen.getByLabelText('Priority') as HTMLSelectElement).value).toBe('critical')
    expect((screen.getByLabelText('Category') as HTMLSelectElement).value).toBe('c1')
    expect((screen.getByLabelText('Response target') as HTMLInputElement).value).toBe('60')
    expect((screen.getByLabelText('Resolution target') as HTMLInputElement).value).toBe('240')

    await userEvent.clear(name)
    await userEvent.type(name, 'Major outage')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/sla/policies/p1', {
      name: 'Major outage',
      priority: 'critical',
      clear_priority: false,
      category_id: 'c1',
      clear_category: false,
      response_target_min: 60,
      resolution_target_min: 240,
    })
  })

  it('cancel on add closes the form without saving', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {} } as any)
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: '+ Add policy' }))
    await userEvent.type(screen.getByLabelText('Policy name'), 'Unsaved')
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(screen.queryByLabelText('Policy name')).toBeNull()
    expect(post).not.toHaveBeenCalled()
  })

  it('cancel on edit closes the form and restores the row without saving', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    expect(screen.getByLabelText('Policy name')).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(screen.queryByLabelText('Policy name')).toBeNull()
    const row = screen.getByText('Critical outage').closest('tr') as HTMLElement
    expect(within(row).getByRole('button', { name: 'Edit' })).toBeTruthy()
    expect(patch).not.toHaveBeenCalled()
  })

  it('renders the edit form outside the scrolling table, like the add form', async () => {
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    expect(screen.getByLabelText('Policy name').closest('table')).toBeNull()
    expect(screen.getByLabelText('Policy name').closest('.overflow-x-auto')).toBeNull()

    // The policy being edited stays in the list, marked, with no row actions.
    const row = screen.getByText('Critical outage').closest('tr') as HTMLElement
    expect(row.className).toContain('bg-blue-50')
    expect(within(row).queryByRole('button', { name: 'Edit' })).toBeNull()
  })

  it('edit sends clear_priority and clear_category when set to Any priority and All categories', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    await userEvent.selectOptions(screen.getByLabelText('Priority'), '')
    await userEvent.selectOptions(screen.getByLabelText('Category'), '')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    const body = patch.mock.calls[0][1] as Record<string, unknown>
    expect(body.clear_priority).toBe(true)
    expect(body.clear_category).toBe(true)
    expect(body.priority).toBeUndefined()
    expect(body.category_id).toBeUndefined()
  })

  it('disables Save while the request is pending', async () => {
    vi.spyOn(api, 'patch').mockReturnValue(new Promise(() => {}) as never)
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect((screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true))
  })

  it('hides the Add button while a form is open', async () => {
    await renderSLA()
    expect(screen.getByRole('button', { name: '+ Add policy' })).toBeTruthy()

    await userEvent.click(screen.getByRole('button', { name: '+ Add policy' }))
    expect(screen.queryByRole('button', { name: '+ Add policy' })).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    expect(screen.queryByRole('button', { name: '+ Add policy' })).toBeNull()
  })

  it('focuses the Name field when Edit opens the form, and again when Edit is clicked on another policy', async () => {
    policies = [POLICY, POLICY_B]
    await renderSLA()
    await screen.findByText('Printer fleet')
    const rowA = screen.getByText('Critical outage').closest('tr') as HTMLElement
    await userEvent.click(within(rowA).getByRole('button', { name: 'Edit' }))

    const name = screen.getByLabelText('Policy name') as HTMLInputElement
    expect(document.activeElement).toBe(name)
    expect(name.value).toBe('Critical outage')

    // Focus moved away from the form, so the second Edit has to take it back.
    await userEvent.click(document.body)
    expect(document.activeElement).not.toBe(name)
    const rowB = screen.getByText('Printer fleet').closest('tr') as HTMLElement
    await userEvent.click(within(rowB).getByRole('button', { name: 'Edit' }))

    const nameB = screen.getByLabelText('Policy name') as HTMLInputElement
    expect(document.activeElement).toBe(nameB)
    expect(nameB.value).toBe('Printer fleet')
  })

  it('focuses the Name field when + Add policy opens the form', async () => {
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: '+ Add policy' }))
    expect(document.activeElement).toBe(screen.getByLabelText('Policy name'))
  })

  it('does not take focus back while the user types in the open form', async () => {
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    const response = screen.getByLabelText('Response target')
    await userEvent.click(response)
    await userEvent.type(response, '5')
    expect(document.activeElement).toBe(response)
  })

  it('hides the empty-state text while a form is open and shows it after Cancel', async () => {
    const queryClient = await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))

    // The policy being edited disappears from the list on a refetch.
    policies = []
    await queryClient.invalidateQueries({ queryKey: ['sla-policies'] })
    await waitFor(() => expect(screen.queryByText('Critical outage')).toBeNull())

    expect(screen.getByLabelText('Policy name')).toBeTruthy()
    expect(screen.queryByText('No SLA policies defined.')).toBeNull()

    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.getByText('No SLA policies defined.')).toBeTruthy()
  })

  it('switching Edit from one policy to another re-points the shared form at the second', async () => {
    policies = [POLICY, POLICY_B]
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
    await renderSLA()
    await screen.findByText('Printer fleet')
    const rowA = () => screen.getByText('Critical outage').closest('tr') as HTMLElement
    const rowB = () => screen.getByText('Printer fleet').closest('tr') as HTMLElement

    await userEvent.click(within(rowA()).getByRole('button', { name: 'Edit' }))
    expect((screen.getByLabelText('Policy name') as HTMLInputElement).value).toBe('Critical outage')
    await userEvent.click(within(rowB()).getByRole('button', { name: 'Edit' }))

    // One form, showing B and nothing of A.
    expect(screen.getAllByLabelText('Policy name')).toHaveLength(1)
    expect((screen.getByLabelText('Policy name') as HTMLInputElement).value).toBe('Printer fleet')
    expect((screen.getByLabelText('Priority') as HTMLSelectElement).value).toBe('low')
    expect((screen.getByLabelText('Category') as HTMLSelectElement).value).toBe('c2')
    expect((screen.getByLabelText('Response target') as HTMLInputElement).value).toBe('15')
    expect((screen.getByLabelText('Resolution target') as HTMLInputElement).value).toBe('90')

    // A is back to a plain row, B is the marked one without actions.
    expect(within(rowA()).getByRole('button', { name: 'Edit' })).toBeTruthy()
    expect(within(rowA()).getByRole('button', { name: 'Delete' })).toBeTruthy()
    expect(rowA().className).not.toContain('bg-blue-50')
    expect(within(rowB()).queryByRole('button', { name: 'Edit' })).toBeNull()
    expect(within(rowB()).queryByRole('button', { name: 'Delete' })).toBeNull()

    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/sla/policies/p2', {
      name: 'Printer fleet',
      priority: 'low',
      clear_priority: false,
      category_id: 'c2',
      clear_category: false,
      response_target_min: 15,
      resolution_target_min: 90,
    })
  })
})
