import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, screen } from '@testing-library/react'
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
