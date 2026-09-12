import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { ClassificationPanel } from './ClassificationPanel'
import * as ticketsApi from '@/api/tickets'

// The first component test in this codebase. ClassificationPanel is the
// deliberate starting point: it was just lifted out of TicketDetailPage's
// 676-line body, and the behaviour worth pinning is the part that was hardest
// to see in there — the dependent-dropdown cascade, and the null-versus-omitted
// distinction in the payload that decides whether a Type is cleared or kept.

const CATEGORIES = [
  { id: 'cat-net', name: 'Network', active: true, sort_order: 1 },
  { id: 'cat-hw', name: 'Hardware', active: true, sort_order: 2 },
  { id: 'cat-old', name: 'Retired', active: false, sort_order: 3 },
]
const TYPES = [
  { id: 'type-vpn', category_id: 'cat-net', name: 'VPN', active: true, sort_order: 1 },
  { id: 'type-dead', category_id: 'cat-net', name: 'Decommissioned', active: false, sort_order: 2 },
]
const ITEMS = [
  { id: 'item-cert', type_id: 'type-vpn', name: 'Certificate', active: true, sort_order: 1 },
]

beforeEach(() => {
  vi.restoreAllMocks()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue(CATEGORIES as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicTypes').mockResolvedValue(TYPES as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicItems').mockResolvedValue(ITEMS as any)
})

function renderPanel(overrides: Partial<React.ComponentProps<typeof ClassificationPanel>> = {}) {
  return renderWithQuery(
    <ClassificationPanel
      ticketId="tkt-1"
      categoryId="cat-net"
      typeId="type-vpn"
      itemId={null}
      canEdit
      {...overrides}
    />
  )
}

describe('display mode', () => {
  it('resolves ids to names', async () => {
    renderPanel()
    expect(await screen.findByText('Network')).toBeDefined()
    expect(await screen.findByText('VPN')).toBeDefined()
  })

  // A ticket classified only to a Category must not show empty Type and Item
  // rows — the tiers below Category are optional.
  it('omits the Type and Item rows when the ticket has neither', async () => {
    renderPanel({ typeId: null, itemId: null })
    await screen.findByText('Network')
    expect(screen.queryByText('Type')).toBeNull()
    expect(screen.queryByText('Item')).toBeNull()
  })

  // Edit is staff-only; a reporting user sees the classification but no control.
  it('hides the Edit control when the caller may not edit', async () => {
    renderPanel({ canEdit: false })
    await screen.findByText('Network')
    expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull()
  })
})

describe('edit mode', () => {
  it('offers only active options', async () => {
    const user = userEvent.setup()
    renderPanel()
    await user.click(await screen.findByRole('button', { name: 'Edit' }))

    expect(await screen.findByRole('option', { name: 'Network' })).toBeDefined()
    // Inactive entries are still referenced by old tickets but must not be
    // selectable for new classification.
    expect(screen.queryByRole('option', { name: 'Retired' })).toBeNull()
    expect(screen.queryByRole('option', { name: 'Decommissioned' })).toBeNull()
  })

  // The cascade is the reason this component owns all three values: choosing a
  // Category invalidates a Type that belonged to the previous one. Leaving the
  // stale Type selected would submit a Type from a different Category, which
  // the database rejects with a composite foreign-key error.
  it('clears the Type and Item when the Category changes', async () => {
    const update = vi.spyOn(ticketsApi, 'updateTicket').mockResolvedValue({} as never)
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    const categorySelect = await screen.findByRole('combobox', { name: /category/i })
    await user.selectOptions(categorySelect, 'cat-hw')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(update).toHaveBeenCalled())
    expect(update).toHaveBeenCalledWith('tkt-1', {
      category_id: 'cat-hw',
      type_id: null,
      item_id: null,
    })
  })

  // Opening the editor and saving unchanged must PRESERVE the Type — the draft
  // is seeded from the ticket. An earlier version of this test asserted the
  // opposite and was wrong: had the component behaved that way, merely opening
  // the panel and pressing Save would silently wipe a ticket's Type.
  it('preserves an unchanged Type', async () => {
    const update = vi.spyOn(ticketsApi, 'updateTicket').mockResolvedValue({} as never)
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(update).toHaveBeenCalled())
    expect(update).toHaveBeenCalledWith('tkt-1', {
      category_id: 'cat-net',
      type_id: 'type-vpn',
      item_id: null,
    })
  })

  // Clearing is null on the wire, not an omitted key: the API treats an absent
  // field as "leave it alone", so undefined here would keep the old Type.
  it('sends null when the Type is explicitly cleared', async () => {
    const update = vi.spyOn(ticketsApi, 'updateTicket').mockResolvedValue({} as never)
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    await user.selectOptions(await screen.findByRole('combobox', { name: /type/i }), '')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(update).toHaveBeenCalled())
    const [, payload] = update.mock.calls[0]
    expect(payload).toHaveProperty('type_id', null)
  })

  it('surfaces a server error instead of closing the editor', async () => {
    vi.spyOn(ticketsApi, 'updateTicket').mockRejectedValue(
      Object.assign(new Error('Request failed'), {
        isAxiosError: true,
        toJSON: () => ({}),
        response: { data: { error: { code: 'invalid_cti', message: 'type does not belong to category' } } },
      })
    )
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('type does not belong to category')).toBeDefined()
    // Still editing, so the user can correct it rather than losing the draft.
    expect(screen.getByRole('button', { name: 'Save' })).toBeDefined()
  })

  it('abandons the draft on Cancel', async () => {
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(await screen.findByRole('button', { name: 'Edit' })).toBeDefined()
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull()
  })
})
