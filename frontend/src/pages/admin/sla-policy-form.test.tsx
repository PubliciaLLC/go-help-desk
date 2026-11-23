import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The SLA policy add and edit form (#671). It used to be one six-column table
// row inside the narrow Features card, with placeholder-only inputs. Each field
// now has a visible label, so it can be found and named by label.

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

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin',
      mfa_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: { sla_enabled: true } })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/admin/sla/policies') return Promise.resolve({ data: [POLICY] })
    if (url === '/admin/categories') {
      return Promise.resolve({ data: [{ id: 'c1', name: 'Network', active: true }] })
    }
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})

async function renderSLA() {
  renderWithQuery(<SettingsPage />)
  await screen.findByRole('heading', { name: 'Settings' })
  await userEvent.click(screen.getByRole('button', { name: 'Features' }))
  await screen.findByText('Critical outage')
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

  it('cancel on edit restores the row without saving', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(screen.queryByLabelText('Policy name')).toBeNull()
    const row = screen.getByText('Critical outage').closest('tr') as HTMLElement
    expect(within(row).getByRole('button', { name: 'Edit' })).toBeTruthy()
    expect(patch).not.toHaveBeenCalled()
  })
})
