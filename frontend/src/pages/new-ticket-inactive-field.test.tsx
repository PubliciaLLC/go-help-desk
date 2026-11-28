import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { useAuthStore } from '@/store/auth'
import * as ticketsApi from '@/api/tickets'

// The resolver keeps returning inactive fields (the ticket page needs them), so
// the new-ticket form has to leave them out itself. A required one included:
// the server refuses every write to an inactive field, so asking for it would
// make the ticket impossible to submit.

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
}))
vi.mock('@/components/Layout', () => ({
  Layout: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { NewTicketPage } from './NewTicketPage'

const CATEGORIES = [{ id: 'cat-1', name: 'Network', active: true, sort_order: 1 }]

function assignment(id: string, name: string, active: boolean, required: boolean) {
  return {
    id: `a-${id}`,
    field_def_id: id,
    scope_type: 'category',
    scope_id: 'cat-1',
    sort_order: 1,
    visible_on_new: true,
    required_on_new: required,
    field_def: { id, name, field_type: 'text', options: [], active, sort_order: 1 },
  }
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'staff@example.com',
      display_name: 'Sam',
      role: 'staff',
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue(CATEGORIES as any)
  vi.spyOn(ticketsApi, 'listPublicTypes').mockResolvedValue([])
  vi.spyOn(ticketsApi, 'listPublicItems').mockResolvedValue([])
  vi.spyOn(ticketsApi, 'resolveFieldsForCTI').mockResolvedValue([
    assignment('f-live', 'Asset tag', true, false),
    assignment('f-off', 'Old cost centre', false, false),
    assignment('f-off-req', 'Retired approval code', false, true),
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ] as any)
})

describe('the new-ticket form and inactive custom fields', () => {
  it('leaves out an inactive field, a required one included', async () => {
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Network' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')

    expect(await screen.findByLabelText('Asset tag')).toBeTruthy()
    expect(screen.queryByLabelText(/Old cost centre/)).toBeNull()
    expect(screen.queryByLabelText(/Retired approval code/)).toBeNull()
  })

  it('does not block submit on an inactive required field', async () => {
    const create = vi.spyOn(ticketsApi, 'createTicket').mockResolvedValue({ id: 't-1' } as never)
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Network' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')
    await screen.findByLabelText('Asset tag')

    await user.type(screen.getByLabelText(/subject/i), 'Printer down')
    await user.click(screen.getByRole('button', { name: 'Submit ticket' }))

    expect(screen.queryByText(/Retired approval code is required/)).toBeNull()
    expect(create).toHaveBeenCalledTimes(1)
  })
})
