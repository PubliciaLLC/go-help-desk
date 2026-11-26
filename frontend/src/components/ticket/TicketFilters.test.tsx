import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import * as ticketsApi from '@/api/tickets'
import * as adminApi from '@/api/admin'
import { api } from '@/api/client'
import { VIEW_FILTERS, type TicketSearch } from '@/lib/ticketSearch'
import { TicketFilters } from './TicketFilters'

beforeEach(() => {
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue([])
  vi.spyOn(adminApi, 'listAssignableStaff').mockResolvedValue([])
  vi.spyOn(api, 'get').mockResolvedValue({ data: [] })
})

function setup(value: TicketSearch, over: { staff?: boolean; offered?: readonly (typeof VIEW_FILTERS.me)[number][] } = {}) {
  const onChange = vi.fn()
  renderWithQuery(
    <TicketFilters
      value={value}
      onChange={onChange}
      layout="panel"
      staff={over.staff ?? true}
      slaOn={false}
      statuses={[]}
      offered={over.offered}
    />,
  )
  return { onChange }
}

describe('TicketFilters: awaiting my reply checkbox', () => {
  it('is a checkbox for staff on a view that offers it, and turning it on sends awaiting_reply', () => {
    const { onChange } = setup({ view: 'me' }, { offered: VIEW_FILTERS.me })
    const box = screen.getByRole('checkbox', { name: 'Awaiting my reply' }) as HTMLInputElement
    expect(box.checked).toBe(false)
    fireEvent.click(box)
    expect(onChange).toHaveBeenCalledWith({ awaiting_reply: true })
  })

  it('shows as checked when the address carries it, and turning it off clears it', () => {
    const { onChange } = setup({ view: 'groups', awaiting_reply: true }, { offered: VIEW_FILTERS.groups })
    const box = screen.getByRole('checkbox', { name: 'Awaiting my reply' }) as HTMLInputElement
    expect(box.checked).toBe(true)
    fireEvent.click(box)
    expect(onChange).toHaveBeenCalledWith({ awaiting_reply: undefined })
  })

  it('is not shown on a view that does not offer it', () => {
    setup({ view: 'available' }, { offered: VIEW_FILTERS.available })
    expect(screen.queryByRole('checkbox', { name: 'Awaiting my reply' })).toBeNull()
  })

  it('is not shown to a requester', () => {
    setup({}, { staff: false })
    expect(screen.queryByRole('checkbox', { name: 'Awaiting my reply' })).toBeNull()
  })
})

describe('TicketFilters: the Item filter (#711)', () => {
  const picked = { category: 'c', type: 't' }
  let listItems: ReturnType<typeof vi.spyOn>

  function items(rows: { id: string; name: string }[]) {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    vi.spyOn(ticketsApi, 'listPublicTypes').mockResolvedValue([{ id: 't', category_id: 'c', name: 'Laptop', description: '', active: true, sort_order: 1 }] as any)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    listItems = vi.spyOn(ticketsApi, 'listPublicItems').mockResolvedValue(rows as any)
  }

  it('is offered to a requester who is shown items', async () => {
    items([{ id: 'i1', name: 'Dock' }])
    setup(picked, { staff: false })
    expect(await screen.findByLabelText('Item')).toBeTruthy()
    expect(await screen.findByRole('option', { name: 'Dock' })).toBeTruthy()
  })

  it('is not offered to a requester when the list of items comes back empty', async () => {
    items([])
    setup(picked, { staff: false })
    await waitFor(() => expect(listItems).toHaveBeenCalled())
    await screen.findByLabelText('Type')
    expect(screen.queryByLabelText('Item')).toBeNull()
  })

  it('staff keep it whatever the list holds', async () => {
    items([])
    setup(picked, { staff: true })
    await waitFor(() => expect(listItems).toHaveBeenCalled())
    expect(await screen.findByLabelText('Item')).toBeTruthy()
  })
})
