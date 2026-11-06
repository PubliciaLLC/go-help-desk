import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { useAuthStore } from '@/store/auth'
import * as ticketsApi from '@/api/tickets'

// #421: nobody may newly pick an archived category, type or item. Staff and
// administrators are still sent the archived rows (they recognise them from old
// tickets), so the form lists them greyed out and not selectable.

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
}))
vi.mock('@/components/Layout', () => ({
  Layout: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { NewTicketPage } from './NewTicketPage'

const CATEGORIES = [
  { id: 'cat-open', name: 'Network', active: true, sort_order: 1 },
  { id: 'cat-old', name: 'Retired kit', active: false, sort_order: 2 },
]
const TYPES = [
  { id: 'type-open', category_id: 'cat-open', name: 'VPN', active: true, sort_order: 1 },
  { id: 'type-old', category_id: 'cat-open', name: 'Dial-up', active: false, sort_order: 2 },
]
const ITEMS = [
  { id: 'item-open', type_id: 'type-open', name: 'Certificate', active: true, sort_order: 1 },
  { id: 'item-old', type_id: 'type-open', name: 'Token fob', active: false, sort_order: 2 },
]

function signInAs(role: 'staff' | 'admin') {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: `${role}@example.com`,
      display_name: 'Sam',
      role,
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

beforeEach(() => {
  vi.restoreAllMocks()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue(CATEGORIES as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicTypes').mockResolvedValue(TYPES as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicItems').mockResolvedValue(ITEMS as any)
  vi.spyOn(ticketsApi, 'resolveFieldsForCTI').mockResolvedValue([])
})

describe.each(['staff', 'admin'] as const)('the new-ticket form for %s', (role) => {
  it('lists archived rows at every level but will not let them be chosen', async () => {
    signInAs(role)
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)

    const retiredCategory = (await screen.findByRole('option', { name: 'Retired kit (archived)' })) as HTMLOptionElement
    expect(retiredCategory.disabled).toBe(true)
    expect((screen.getByRole('option', { name: 'Network' }) as HTMLOptionElement).disabled).toBe(false)

    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-open')
    const retiredType = (await screen.findByRole('option', { name: 'Dial-up (archived)' })) as HTMLOptionElement
    expect(retiredType.disabled).toBe(true)
    expect((screen.getByRole('option', { name: 'VPN' }) as HTMLOptionElement).disabled).toBe(false)

    await user.selectOptions(screen.getByLabelText('Type'), 'type-open')
    const retiredItem = (await screen.findByRole('option', { name: 'Token fob (archived)' })) as HTMLOptionElement
    expect(retiredItem.disabled).toBe(true)
    expect((screen.getByRole('option', { name: 'Certificate' }) as HTMLOptionElement).disabled).toBe(false)
  })

  it('does not select a disabled archived category when the user tries', async () => {
    signInAs(role)
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Retired kit (archived)' })

    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-old').catch(() => {})
    expect((screen.getByLabelText(/category/i) as HTMLSelectElement).value).toBe('')
  })
})
