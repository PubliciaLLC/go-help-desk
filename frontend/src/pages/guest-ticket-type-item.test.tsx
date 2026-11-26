import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import * as ticketsApi from '@/api/tickets'
import * as guestApi from '@/api/guest'
import * as adminApi from '@/api/admin'
import { GuestTicketPage } from './GuestTicketPage'

// #711: the guest form shows Category > Type > Item only while the instance
// lets guests choose them, which GET /site reports as
// guests_can_choose_type_item. Off, it is the category-only form it always was
// and asks for no types.

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

let listTypes: ReturnType<typeof vi.spyOn>
let listItems: ReturnType<typeof vi.spyOn>
let createGuest: ReturnType<typeof vi.spyOn>

function site(on: boolean | undefined) {
  vi.spyOn(adminApi, 'getSiteConfig').mockResolvedValue({
    name: 'Help', logo_url: '', version: '1', guest_submission_enabled: true, guests_can_choose_type_item: on,
  })
}

beforeEach(() => {
  vi.restoreAllMocks()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue(CATEGORIES as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  listTypes = vi.spyOn(ticketsApi, 'listPublicTypes').mockImplementation(async (id: string) => (TYPES[id] ?? []) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  listItems = vi.spyOn(ticketsApi, 'listPublicItems').mockImplementation(async (_c: string, id: string) => (ITEMS[id] ?? []) as any)
  vi.spyOn(guestApi, 'listGuestFields').mockResolvedValue([])
  createGuest = vi.spyOn(guestApi, 'createGuestTicket').mockResolvedValue('GHD-2026-000001')
})

function region(): HTMLElement {
  const id = screen.getByLabelText(/category/i).getAttribute('aria-describedby')
  return document.getElementById(id!)!
}

async function fillAndSubmit(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText(/name \*/i), 'Visitor')
  await user.type(screen.getByLabelText(/email \*/i), 'v@example.com')
  await user.type(screen.getByLabelText(/subject/i), 'Dock dead')
  await user.click(screen.getByRole('button', { name: /submit request/i }))
}

describe('the guest form while guests may not choose the Type and Item', () => {
  for (const [name, flag] of [['false', false], ['absent (an older server)', undefined]] as const) {
    it(`is category only and asks for no types: guests_can_choose_type_item ${name}`, async () => {
      site(flag)
      const user = userEvent.setup()
      renderWithQuery(<GuestTicketPage />)
      await screen.findByRole('option', { name: 'Hardware' })
      await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
      expect(screen.queryByLabelText(/^type/i)).toBeNull()
      expect(screen.queryByLabelText(/^item/i)).toBeNull()
      expect(region().textContent).toBe('Hardware text')

      await fillAndSubmit(user)
      await waitFor(() => expect(createGuest).toHaveBeenCalled())
      const sent = createGuest.mock.calls[0][0] as Record<string, unknown>
      expect(sent.category_id).toBe('cat-hw')
      expect(sent.type_id).toBeUndefined()
      expect(sent.item_id).toBeUndefined()
      expect(listTypes).not.toHaveBeenCalled()
      expect(listItems).not.toHaveBeenCalled()
    })
  }

  it('shows no selectors before the site answers', async () => {
    vi.spyOn(adminApi, 'getSiteConfig').mockReturnValue(new Promise(() => {}))
    renderWithQuery(<GuestTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    expect(screen.queryByLabelText(/^type/i)).toBeNull()
  })
})

describe('the guest form while guests may choose the Type and Item', () => {
  beforeEach(() => site(true))

  it('shows Category, Type and Item in that order, each disabled until its parent is chosen', async () => {
    const user = userEvent.setup()
    renderWithQuery(<GuestTicketPage />)
    const type = (await screen.findByLabelText(/^type/i)) as HTMLSelectElement
    const item = screen.getByLabelText(/^item/i) as HTMLSelectElement
    const category = screen.getByLabelText(/category/i)
    expect(category.compareDocumentPosition(type) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(type.compareDocumentPosition(item) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(type.disabled).toBe(true)
    expect(item.disabled).toBe(true)

    await screen.findByRole('option', { name: 'Hardware' })
    await user.selectOptions(category, 'cat-hw')
    await screen.findByRole('option', { name: 'Laptop' })
    expect(type.disabled).toBe(false)
    expect(item.disabled).toBe(true)
    await user.selectOptions(type, 'ty-lap')
    await screen.findByRole('option', { name: 'Dock' })
    expect(item.disabled).toBe(false)
  })

  it('shows the most specific description and resets down the cascade', async () => {
    const user = userEvent.setup()
    renderWithQuery(<GuestTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    expect(region().textContent).toBe('Hardware text')
    await screen.findByRole('option', { name: 'Laptop' })
    await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-lap')
    expect(region().textContent).toBe('Laptop text')
    await screen.findByRole('option', { name: 'Dock' })
    await user.selectOptions(screen.getByLabelText(/^item/i), 'it-dock')
    expect(region().textContent).toBe('Dock text')
    await user.selectOptions(screen.getByLabelText(/^item/i), 'it-bat')
    expect(region().textContent).toBe('Laptop text')

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

  it('sends the type and the item that were chosen', async () => {
    const user = userEvent.setup()
    renderWithQuery(<GuestTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    await screen.findByRole('option', { name: 'Laptop' })
    await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-lap')
    await screen.findByRole('option', { name: 'Dock' })
    await user.selectOptions(screen.getByLabelText(/^item/i), 'it-dock')
    await fillAndSubmit(user)
    await waitFor(() => expect(createGuest).toHaveBeenCalled())
    expect(createGuest.mock.calls[0][0]).toMatchObject({ category_id: 'cat-hw', type_id: 'ty-lap', item_id: 'it-dock' })
  })

  it('sends only what was chosen: the category alone still files', async () => {
    const user = userEvent.setup()
    renderWithQuery(<GuestTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    await fillAndSubmit(user)
    await waitFor(() => expect(createGuest).toHaveBeenCalled())
    const sent = createGuest.mock.calls[0][0] as Record<string, unknown>
    expect(sent.type_id).toBeUndefined()
    expect(sent.item_id).toBeUndefined()
  })

  it('says so while the types load, and when they cannot be loaded, instead of looking empty', async () => {
    const user = userEvent.setup()
    let release: (v: object[]) => void = () => {}
    listTypes.mockImplementation(() => new Promise((resolve) => { release = resolve as typeof release }))
    renderWithQuery(<GuestTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    expect((await screen.findByRole('status', { name: /types/i })).textContent).toMatch(/loading/i)
    release(TYPES['cat-hw'])
    await screen.findByRole('option', { name: 'Laptop' })
    expect(screen.queryByRole('status', { name: /types/i })).toBeNull()

    listTypes.mockRejectedValue(new Error('boom'))
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-sw')
    const alert = await screen.findByRole('alert', { name: /types/i })
    expect(alert.textContent).toMatch(/could not be loaded/i)
    // The form still files under the category alone.
    expect(screen.getByRole('button', { name: /submit request/i })).toBeTruthy()
  })

  it('does the same for the items of a type', async () => {
    const user = userEvent.setup()
    listItems.mockRejectedValue(new Error('boom'))
    renderWithQuery(<GuestTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    await screen.findByRole('option', { name: 'Laptop' })
    await user.selectOptions(screen.getByLabelText(/^type/i), 'ty-lap')
    const alert = await screen.findByRole('alert', { name: /items/i })
    expect(alert.textContent).toMatch(/could not be loaded/i)
  })

  it('every selector points at the description region', async () => {
    renderWithQuery(<GuestTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    await screen.findByLabelText(/^type/i)
    for (const label of [/category/i, /^type/i, /^item/i]) {
      expect(screen.getByLabelText(label).getAttribute('aria-describedby')).toBe(region().id)
    }
    expect(region().getAttribute('aria-live')).toBe('polite')
  })
})
