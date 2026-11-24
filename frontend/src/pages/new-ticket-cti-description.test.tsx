import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { useAuthStore } from '@/store/auth'
import * as ticketsApi from '@/api/tickets'

// The most specific description of the chosen Category / Type / Item shows
// beneath the chooser and follows every change (docs/DESIGN.md, Ticket
// Classification -> Descriptions).

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
}))
vi.mock('@/components/Layout', () => ({
  Layout: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { NewTicketPage } from './NewTicketPage'

const CATEGORIES = [
  { id: 'cat-hw', name: 'Hardware', description: 'Hardware text', active: true, sort_order: 1 },
  { id: 'cat-sw', name: 'Software', description: '', active: true, sort_order: 2 },
  { id: 'cat-html', name: 'Markup', description: '<b>bold</b>\nsecond line', active: true, sort_order: 3 },
]
const TYPES: Record<string, object[]> = {
  'cat-hw': [
    { id: 'ty-lap', category_id: 'cat-hw', name: 'Laptop', description: 'Laptop text', active: true, sort_order: 1 },
    { id: 'ty-mon', category_id: 'cat-hw', name: 'Monitor', description: '', active: true, sort_order: 2 },
  ],
  'cat-sw': [{ id: 'ty-os', category_id: 'cat-sw', name: 'OS', description: '', active: true, sort_order: 1 }],
}
const ITEMS: Record<string, object[]> = {
  'ty-lap': [
    { id: 'it-dock', type_id: 'ty-lap', name: 'Dock', description: 'Dock text', active: true, sort_order: 1 },
    { id: 'it-bat', type_id: 'ty-lap', name: 'Battery', description: '', active: true, sort_order: 2 },
  ],
}

function signIn(role: 'staff' | 'user') {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'x@example.com',
      display_name: 'X',
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
  vi.spyOn(ticketsApi, 'listPublicTypes').mockImplementation(async (id: string) => (TYPES[id] ?? []) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicItems').mockImplementation(async (_c: string, id: string) => (ITEMS[id] ?? []) as any)
  vi.spyOn(ticketsApi, 'resolveFieldsForCTI').mockResolvedValue([])
})

// The region the text sits in: the element the selectors point at.
function region(): HTMLElement {
  const id = screen.getByLabelText(/category/i).getAttribute('aria-describedby')
  expect(id).toBeTruthy()
  const el = document.getElementById(id!)
  expect(el).not.toBeNull()
  return el!
}

describe('the description under the chooser, as staff', () => {
  beforeEach(() => signIn('staff'))

  it('follows the selectors: category, type, item, and back up', async () => {
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })

    expect(region().textContent).toBe('') // nothing before a category is chosen

    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    expect(region().textContent).toBe('Hardware text')

    await screen.findByRole('option', { name: 'Laptop' })
    await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-lap')
    expect(region().textContent).toBe('Laptop text')

    await screen.findByRole('option', { name: 'Dock' })
    await user.selectOptions(screen.getByLabelText(/^item/i), 'it-dock')
    expect(region().textContent).toBe('Dock text')

    // An item with no description of its own: the type's, still.
    await user.selectOptions(screen.getByLabelText(/^item/i), 'it-bat')
    expect(region().textContent).toBe('Laptop text')

    // Back to no item: still the type's. Back to no type: the category's.
    await user.selectOptions(screen.getByLabelText(/^item/i), '')
    expect(region().textContent).toBe('Laptop text')
    await user.selectOptions(screen.getByLabelText(/^type/i), '')
    expect(region().textContent).toBe('Hardware text')
  })

  it('a type with no description falls back to its category', async () => {
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    await screen.findByRole('option', { name: 'Monitor' })
    await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-mon')
    expect(region().textContent).toBe('Hardware text')
  })

  it('clears when the category changes, and shows nothing if the new one has none', async () => {
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    await screen.findByRole('option', { name: 'Laptop' })
    await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-lap')
    expect(region().textContent).toBe('Laptop text')

    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-sw')
    expect(region().textContent).toBe('')
    expect(region().querySelector('p')).toBeNull() // no empty box

    await user.selectOptions(screen.getByLabelText(/category/i), '')
    expect(region().textContent).toBe('')
  })

  it('shows markup as text and keeps the line break for display', async () => {
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Markup' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-html')
    expect(region().textContent).toBe('<b>bold</b>\nsecond line')
    expect(region().querySelector('b')).toBeNull()
  })

  it('is announced politely, and every selector points at it', async () => {
    renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    expect(region().getAttribute('aria-live')).toBe('polite')
    for (const label of [/category/i, /^type/i, /^item/i]) {
      expect(screen.getByLabelText(label).getAttribute('aria-describedby')).toBe(region().id)
    }
  })

  it('has no hover-only tooltip', async () => {
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    expect(region().querySelector('[title]')).toBeNull()
    expect(screen.getByRole('option', { name: 'Hardware' }).getAttribute('title')).toBeNull()
  })
})

describe('the description under the chooser, as a requester', () => {
  beforeEach(() => signIn('user'))

  it('shows the category and type text; there is no item to choose', async () => {
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    expect(screen.queryByLabelText(/^item/i)).toBeNull()
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    expect(region().textContent).toBe('Hardware text')
    await screen.findByRole('option', { name: 'Laptop' })
    await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-lap')
    await waitFor(() => expect(region().textContent).toBe('Laptop text'))
  })
})
