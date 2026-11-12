import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { useAuthStore } from '@/store/auth'
import * as ticketsApi from '@/api/tickets'

// The new-ticket form sets maxlength from what the server says applies to each
// field (its own limit, else its type's) and refuses an over-long value before
// calling the server.

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }))
vi.mock('@/components/Layout', () => ({
  Layout: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { NewTicketPage } from './NewTicketPage'

function assignment(id: string, name: string, type: string, effective?: number) {
  return {
    id: `a-${id}`, field_def_id: id, scope_type: 'category', scope_id: 'cat-1', sort_order: 1,
    visible_on_new: true, required_on_new: false,
    field_def: { id, name, field_type: type, options: [], active: true, sort_order: 1, effective_max_length: effective },
  }
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'staff@example.com', display_name: 'Sam', role: 'staff', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue([{ id: 'cat-1', name: 'Network', active: true, sort_order: 1 }] as any)
  vi.spyOn(ticketsApi, 'listPublicTypes').mockResolvedValue([])
  vi.spyOn(ticketsApi, 'listPublicItems').mockResolvedValue([])
  vi.spyOn(ticketsApi, 'resolveFieldsForCTI').mockResolvedValue([
    assignment('f-short', 'Asset tag', 'text', 100),
    assignment('f-log', 'Error log', 'textarea', 10000),
    assignment('f-cost', 'Cost', 'number', undefined),
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ] as any)
})

async function open() {
  const user = userEvent.setup()
  renderWithQuery(<NewTicketPage />)
  await screen.findByRole('option', { name: 'Network' })
  await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')
  await screen.findByLabelText('Asset tag')
  await user.type(screen.getByLabelText(/subject/i), 'Printer down')
  return user
}

describe('the new-ticket form and a custom field\'s maximum length', () => {
  it('sets maxlength from the limit that applies to each field', async () => {
    await open()
    expect((screen.getByLabelText('Asset tag') as HTMLInputElement).maxLength).toBe(100)
    expect((screen.getByLabelText('Error log') as HTMLTextAreaElement).maxLength).toBe(10000)
    expect((screen.getByLabelText('Cost') as HTMLInputElement).getAttribute('maxlength')).toBeNull()
  })

  it('refuses an over-long value, names the field and the limit, and does not call the server', async () => {
    const create = vi.spyOn(ticketsApi, 'createTicket').mockResolvedValue({ id: 't-1' } as never)
    const user = await open()
    fireEvent.change(screen.getByLabelText('Asset tag'), { target: { value: 'a'.repeat(101) } })
    await user.click(screen.getByRole('button', { name: 'Submit ticket' }))

    expect((await screen.findByRole('alert')).textContent).toBe('Asset tag is too long: at most 100 characters.')
    expect(create).not.toHaveBeenCalled()
  })

  it('sends a value of exactly the limit, counted in characters', async () => {
    const create = vi.spyOn(ticketsApi, 'createTicket').mockResolvedValue({ id: 't-1' } as never)
    const user = await open()
    fireEvent.change(screen.getByLabelText('Asset tag'), { target: { value: '😀'.repeat(100) } })
    await user.click(screen.getByRole('button', { name: 'Submit ticket' }))

    expect(create).toHaveBeenCalledTimes(1)
    expect(create.mock.calls[0][0].custom_fields).toEqual({ 'f-short': '😀'.repeat(100) })
  })
})
