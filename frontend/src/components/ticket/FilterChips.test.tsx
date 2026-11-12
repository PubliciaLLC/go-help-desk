import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import * as ticketsApi from '@/api/tickets'
import { FilterChips, type ChipNames } from './FilterChips'
import type { TicketSearch } from '@/lib/ticketSearch'

const NAMES: ChipNames = {
  status: id => ({ 'st-new': 'New', 'st-prog': 'In Progress' })[id],
  category: id => ({ 'cat-1': 'Hardware' })[id],
  type: id => ({ 'ty-1': 'Printers' })[id],
  item: id => ({ 'it-1': 'Toner' })[id],
  user: id => ({ 'u-sam': 'Sam Staff' })[id],
  group: id => ({ 'g-1': 'Support' })[id],
}

function setup(search: TicketSearch, over: { staff?: boolean } = {}) {
  const onClear = vi.fn()
  const onClearAll = vi.fn()
  const utils = renderWithQuery(
    <FilterChips search={search} names={NAMES} staff={over.staff ?? true} onClear={onClear} onClearAll={onClearAll} />,
  )
  return { onClear, onClearAll, ...utils }
}

beforeEach(() => {
  vi.spyOn(ticketsApi, 'lookupRequesterById').mockResolvedValue({
    id: 'u-dana',
    display_name: 'Dana Whitfield',
    email: 'dana@example.com',
  })
})

describe('FilterChips', () => {
  it('renders nothing when no filter is set (text, queue and sort are not filters)', () => {
    const { container } = setup({ q: 'x', scope: 'all', sort: 'subject' })
    expect(container.textContent).toBe('')
  })

  it('shows one chip per filter, named by what the filter means', () => {
    setup({
      status: ['st-new', 'st-prog'],
      priority: ['high'],
      category: 'cat-1',
      type: 'ty-1',
      channel: ['web', 'phone'],
      sla: 'at_risk',
    })
    const group = screen.getByRole('group', { name: 'Active filters' })
    const chips = within(group).getAllByRole('listitem').map(li => li.textContent?.replace(/\s+/g, ' ').trim())
    expect(chips).toEqual([
      'Status: New, In Progress',
      'Priority: High',
      'Category: Hardware › Printers',
      'Came in by: Web, Phone',
      'SLA: At risk',
    ])
  })

  it('a category, type and item read as one trail', () => {
    setup({ category: 'cat-1', type: 'ty-1', item: 'it-1' })
    expect(screen.getByText('Hardware › Printers › Toner')).toBeTruthy()
  })

  it('a date range reads as a range, the year once', () => {
    setup({ created_from: '2026-10-01', created_to: '2026-10-31' })
    const chip = screen.getByText(/^Created:/).closest('li')!
    expect(chip.textContent).toMatch(/1.*Oct.*–.*31.*Oct.*2026|Oct.*1.*–.*Oct.*31.*2026/)
  })

  it('an open-ended range says which end it has', () => {
    setup({ updated_from: '2026-11-03' })
    expect(screen.getByText(/^Updated:/).closest('li')!.textContent).toMatch(/from .*(3.*Nov|Nov.*3)/)
  })

  it('each chip has its own remove button, and it removes that filter', () => {
    const { onClear } = setup({ priority: ['high'], sla: 'breached' })
    fireEvent.click(screen.getByRole('button', { name: 'Remove Priority filter' }))
    expect(onClear).toHaveBeenCalledWith('priority')
    fireEvent.click(screen.getByRole('button', { name: 'Remove SLA filter' }))
    expect(onClear).toHaveBeenCalledWith('sla')
  })

  it('Clear all follows the chips', () => {
    const { onClearAll } = setup({ priority: ['high'] })
    fireEvent.click(screen.getByRole('button', { name: 'Clear all' }))
    expect(onClearAll).toHaveBeenCalledTimes(1)
  })

  describe('the requester chip', () => {
    it('resolves a picked account by id, as it must after a reload', async () => {
      setup({ reporter: 'u-dana' })
      await screen.findByText('Dana Whitfield')
      expect(ticketsApi.lookupRequesterById).toHaveBeenCalledWith('u-dana')
      expect(screen.getByText(/^Requester:/)).toBeTruthy()
    })

    it('shows something readable while the name is on its way', () => {
      vi.spyOn(ticketsApi, 'lookupRequesterById').mockReturnValue(new Promise(() => {}))
      setup({ reporter: 'u-dana' })
      expect(screen.getByText(/^Requester:/).closest('li')!.textContent).toContain('…')
    })

    it('says so when the account is gone (a 404 is also a disabled or deleted one)', async () => {
      vi.spyOn(ticketsApi, 'lookupRequesterById').mockResolvedValue(null)
      setup({ reporter: 'u-gone' })
      await screen.findByText('Unknown account')
    })

    it('says so, and does not leave the chip empty, when the lookup fails', async () => {
      vi.spyOn(ticketsApi, 'lookupRequesterById').mockRejectedValue(new Error('network'))
      setup({ reporter: 'u-dana' })
      await screen.findByText('Unknown account')
    })

    it('free text is "contains", and repeats only what the viewer typed', () => {
      setup({ requester: 'whitfield' })
      expect(screen.getByText(/^Requester contains:/).closest('li')!.textContent).toContain('“whitfield”')
      expect(ticketsApi.lookupRequesterById).not.toHaveBeenCalled()
    })

    it('an account wins over free text if an address carries both', () => {
      setup({ reporter: 'u-dana', requester: 'whitfield' })
      expect(screen.getAllByText(/^Requester/)).toHaveLength(1)
    })

    it('is removed with both its fields', () => {
      const { onClear } = setup({ requester: 'whitfield' })
      fireEvent.click(screen.getByRole('button', { name: 'Remove Requester filter' }))
      expect(onClear).toHaveBeenCalledWith('requester')
    })
  })

  describe('for a requester (not staff)', () => {
    it('shows only the filters the page actually sends them', () => {
      setup(
        { priority: ['high'], sla: 'breached', assignee: 'u-sam', group: 'g-1', channel: ['web'], requester: 'x', reporter: 'u-dana', updated_from: '2026-11-03' },
        { staff: false },
      )
      expect(screen.getAllByRole('listitem').map(li => li.textContent)).toEqual(['Priority: High'])
      expect(ticketsApi.lookupRequesterById).not.toHaveBeenCalled()
    })

    it('shows no chip row at all when every filter is one they cannot use', () => {
      const { container } = setup({ sla: 'breached' }, { staff: false })
      expect(container.textContent).toBe('')
    })
  })

  it('names an assignee and a group', () => {
    setup({ assignee: 'u-sam', group: 'g-1' })
    expect(screen.getByText('Sam Staff')).toBeTruthy()
    expect(screen.getByText('Support')).toBeTruthy()
  })

  it('does not wait on the lookup to remove a chip', async () => {
    const { onClear } = setup({ reporter: 'u-dana' })
    fireEvent.click(screen.getByRole('button', { name: 'Remove Requester filter' }))
    expect(onClear).toHaveBeenCalledWith('requester')
    await waitFor(() => expect(ticketsApi.lookupRequesterById).toHaveBeenCalled())
  })
})
