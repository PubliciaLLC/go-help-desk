import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { ClassificationPanel } from './ClassificationPanel'
import * as ticketsApi from '@/api/tickets'

// Staff reclassifying a ticket choose a classification with the same three
// selectors, so they get the same description beneath them: while editing only.

const CATEGORIES = [
  { id: 'cat-net', name: 'Network', description: 'Network text', active: true, sort_order: 1 },
  { id: 'cat-hw', name: 'Hardware', description: '', active: true, sort_order: 2 },
]
const TYPES = [
  { id: 'type-vpn', category_id: 'cat-net', name: 'VPN', description: 'VPN text', active: true, sort_order: 1 },
  { id: 'type-wifi', category_id: 'cat-net', name: 'Wi-Fi', description: '', active: true, sort_order: 2 },
]
const ITEMS = [{ id: 'item-cert', type_id: 'type-vpn', name: 'Certificate', description: 'Cert text', active: true, sort_order: 1 }]

beforeEach(() => {
  vi.restoreAllMocks()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue(CATEGORIES as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicTypes').mockImplementation(async (id: string) => (id === 'cat-net' ? TYPES : []) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicItems').mockImplementation(async (_c: string, t: string) => (t === 'type-vpn' ? ITEMS : []) as any)
})

function region(): HTMLElement {
  const id = screen.getByLabelText('Category').getAttribute('aria-describedby')
  expect(id).toBeTruthy()
  return document.getElementById(id!)!
}

describe('the description in the Classification card', () => {
  it('is not shown in the read-only display, where nobody is choosing', async () => {
    renderWithQuery(<ClassificationPanel ticketId="t1" categoryId="cat-net" typeId="type-vpn" itemId={null} canEdit />)
    await screen.findByText('Network')
    expect(screen.queryByText('Network text')).toBeNull()
    expect(screen.queryByText('VPN text')).toBeNull()
  })

  it('follows the draft while editing, starting from the ticket own classification', async () => {
    const user = userEvent.setup()
    renderWithQuery(<ClassificationPanel ticketId="t1" categoryId="cat-net" typeId="type-vpn" itemId={null} canEdit />)
    await screen.findByText('Network')
    await user.click(screen.getByRole('button', { name: 'Edit' }))
    await screen.findByRole('option', { name: 'Certificate' })
    expect(region().textContent).toBe('VPN text')

    await user.selectOptions(screen.getByLabelText('Item'), 'item-cert')
    expect(region().textContent).toBe('Cert text')

    await user.selectOptions(screen.getByLabelText('Type'), 'type-wifi')
    expect(region().textContent).toBe('Network text') // a type with none: its category's

    await user.selectOptions(screen.getByLabelText('Category'), 'cat-hw')
    expect(region().textContent).toBe('') // changing the category clears the type and item with their text
    expect(region().getAttribute('aria-live')).toBe('polite')
  })
})
