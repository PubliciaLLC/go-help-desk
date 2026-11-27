import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { useAuthStore } from '@/store/auth'
import * as ticketsApi from '@/api/tickets'
import * as authApi from '@/api/auth'
import type { User } from '@/api/types'

// #711: a requester gets an Item selector under Type only while the setting is
// on, which /me reports as can_choose_item. Staff always have one. Everything
// else (cascade, description, custom fields, payload) is the staff form's.

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
      id: 'u-1', email: 'x@example.com', display_name: 'X', role, mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

function me(canChooseItem: boolean | undefined) {
  vi.spyOn(authApi, 'getMe').mockResolvedValue({
    id: 'u-1', email: 'x@example.com', display_name: 'X', role: 'user', mfa_enabled: false,
    created_at: '', updated_at: '', can_choose_item: canChooseItem,
  } as User)
}

let createTicket: ReturnType<typeof vi.spyOn>
let resolveFields: ReturnType<typeof vi.spyOn>
let listItems: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  vi.restoreAllMocks()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue(CATEGORIES as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicTypes').mockImplementation(async (id: string) => (TYPES[id] ?? []) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  listItems = vi.spyOn(ticketsApi, 'listPublicItems').mockImplementation(async (_c: string, id: string) => (ITEMS[id] ?? []) as any)
  resolveFields = vi.spyOn(ticketsApi, 'resolveFieldsForCTI').mockResolvedValue([])
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  createTicket = vi.spyOn(ticketsApi, 'createTicket').mockResolvedValue({ id: 't-1' } as any)
})

function region(): HTMLElement {
  const id = screen.getByLabelText(/category/i).getAttribute('aria-describedby')
  return document.getElementById(id!)!
}

async function fillAndSubmit(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText(/subject/i), 'Dock dead')
  await user.click(screen.getByRole('button', { name: /submit|create/i }))
}

describe('a requester while Items is not shown to registered users', () => {
  beforeEach(() => signIn('user'))

  for (const [name, flag] of [['false', false], ['absent (an older server)', undefined]] as const) {
    it(`has no Item selector and asks for no items: can_choose_item ${name}`, async () => {
      me(flag)
      const user = userEvent.setup()
      renderWithQuery(<NewTicketPage />)
      await screen.findByRole('option', { name: 'Hardware' })
      await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
      await screen.findByRole('option', { name: 'Laptop' })
      await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-lap')
      expect(screen.queryByLabelText(/^item/i)).toBeNull()
      expect(listItems).not.toHaveBeenCalled()
      await waitFor(() => expect(region().textContent).toBe('Laptop text'))

      await fillAndSubmit(user)
      await waitFor(() => expect(createTicket).toHaveBeenCalled())
      const sent = createTicket.mock.calls[0][0] as Record<string, unknown>
      expect(sent.type_id).toBe('ty-lap')
      expect(sent.item_id).toBeUndefined()
    })
  }
})

describe('a requester while Items is shown to registered users', () => {
  beforeEach(() => {
    signIn('user')
    me(true)
  })

  it('offers the Item under Type, disabled until a type with items is chosen', async () => {
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    const item = await screen.findByLabelText(/^item/i)
    expect((item as HTMLSelectElement).disabled).toBe(true)
    await screen.findByRole('option', { name: 'Hardware' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    await screen.findByRole('option', { name: 'Laptop' })
    await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-mon') // a type with no items
    expect((screen.getByLabelText(/^item/i) as HTMLSelectElement).disabled).toBe(true)
    await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-lap')
    await screen.findByRole('option', { name: 'Dock' })
    expect((screen.getByLabelText(/^item/i) as HTMLSelectElement).disabled).toBe(false)
  })

  it('shows the item description, falls back, and resets with the cascade', async () => {
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    await screen.findByRole('option', { name: 'Laptop' })
    await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-lap')
    await screen.findByRole('option', { name: 'Dock' })

    await user.selectOptions(screen.getByLabelText(/^item/i), 'it-dock')
    expect(region().textContent).toBe('Dock text')
    await user.selectOptions(screen.getByLabelText(/^item/i), 'it-bat') // no text of its own
    expect(region().textContent).toBe('Laptop text')

    // A new type clears the item; a new category clears type and item.
    await user.selectOptions(screen.getByLabelText(/^item/i), 'it-dock')
    await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-mon')
    expect((screen.getByLabelText(/^item/i) as HTMLSelectElement).value).toBe('')
    expect(region().textContent).toBe('Hardware text')
    await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-lap')
    await screen.findByRole('option', { name: 'Dock' })
    await user.selectOptions(screen.getByLabelText(/^item/i), 'it-dock')
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-sw')
    expect((screen.getByLabelText(/^type/i) as HTMLSelectElement).value).toBe('')
    expect((screen.getByLabelText(/^item/i) as HTMLSelectElement).value).toBe('')
    expect(region().textContent).toBe('')
  })

  it('resolves the custom fields for the chosen item', async () => {
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    await screen.findByRole('option', { name: 'Laptop' })
    await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-lap')
    await screen.findByRole('option', { name: 'Dock' })
    await user.selectOptions(screen.getByLabelText(/^item/i), 'it-dock')
    await waitFor(() =>
      expect(resolveFields).toHaveBeenCalledWith({ category_id: 'cat-hw', type_id: 'ty-lap', item_id: 'it-dock' }))
  })

  it('sends the item with the ticket', async () => {
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    await screen.findByRole('option', { name: 'Laptop' })
    await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-lap')
    await screen.findByRole('option', { name: 'Dock' })
    await user.selectOptions(screen.getByLabelText(/^item/i), 'it-dock')
    await fillAndSubmit(user)
    await waitFor(() => expect(createTicket).toHaveBeenCalled())
    expect(createTicket.mock.calls[0][0]).toMatchObject({ category_id: 'cat-hw', type_id: 'ty-lap', item_id: 'it-dock' })
  })

  it('sends no item when none was chosen, and still offers no priority', async () => {
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    expect(screen.queryByLabelText(/priority/i)).toBeNull()
    await fillAndSubmit(user)
    await waitFor(() => expect(createTicket).toHaveBeenCalled())
    const sent = createTicket.mock.calls[0][0] as Record<string, unknown>
    expect(sent.item_id).toBeUndefined()
    expect(sent.priority).toBeUndefined()
  })
})

describe('staff', () => {
  it('always have the Item selector, whatever /me says', async () => {
    signIn('staff')
    me(false)
    renderWithQuery(<NewTicketPage />)
    expect(await screen.findByLabelText(/^item/i)).toBeTruthy()
  })
})
