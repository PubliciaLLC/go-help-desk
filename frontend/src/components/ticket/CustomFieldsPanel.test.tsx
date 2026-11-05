import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { CustomFieldsPanel } from './CustomFieldsPanel'
import * as ticketsApi from '@/api/tickets'
import type { Assignment, FieldDef, TicketFieldValue } from '@/api/types'

// Bug 2 (#603): the card used to show only the fields that already held a value,
// so a field that was empty at creation, or was never on the new-ticket form,
// could not be filled in afterwards. Staff now see every field the ticket has.

function def(id: string, name: string, over: Partial<FieldDef> = {}): FieldDef {
  return { id, name, field_type: 'text', sort_order: 1, active: true, created_at: '', ...over }
}

function assignment(d: FieldDef, scope: Assignment['scope_type']): Assignment {
  return {
    id: `a-${d.id}`,
    field_def_id: d.id,
    field_def: d,
    scope_type: scope,
    scope_id: 's',
    sort_order: 1,
    visible_on_new: true,
    required_on_new: false,
  }
}

function held(d: FieldDef, value: string): TicketFieldValue {
  return {
    ticket_id: 't1',
    field_def_id: d.id,
    field_name: d.name,
    field_type: d.field_type,
    options: d.options,
    value,
    updated_at: '',
  }
}

function setup(opts: {
  fields: Assignment[]
  values: TicketFieldValue[]
  staff: boolean
}) {
  vi.spyOn(ticketsApi, 'resolveFieldsForCTI').mockResolvedValue(opts.fields)
  vi.spyOn(ticketsApi, 'listTicketCustomFields').mockResolvedValue(opts.values)
  return renderWithQuery(
    <CustomFieldsPanel ticketId="t1" categoryId="c1" typeId="ty1" itemId="i1" isStaffOrAdmin={opts.staff} />
  )
}

// The labels in the card, top to bottom. "Inactive" markers are not labels.
function labels() {
  return screen.getAllByText(/^(Region|Order|Tier|Owner|Legacy|Stray)$/).map((e) => e.textContent)
}

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('CustomFieldsPanel, staff', () => {
  it('shows an empty active field and saves a value typed into it', async () => {
    const region = def('f1', 'Region')
    const put = vi.spyOn(ticketsApi, 'putTicketCustomFields').mockResolvedValue()
    setup({ fields: [assignment(region, 'category')], values: [], staff: true })

    expect(await screen.findByText('Region')).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    await userEvent.type(screen.getByLabelText('Region'), 'EMEA')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(put).toHaveBeenCalledWith('t1', { f1: 'EMEA' }))
  })

  it('shows an inactive field that holds a value as read-only, marked Inactive', async () => {
    const legacy = def('f1', 'Legacy', { active: false })
    const put = vi.spyOn(ticketsApi, 'putTicketCustomFields').mockResolvedValue()
    setup({ fields: [assignment(legacy, 'category')], values: [held(legacy, 'old data')], staff: true })

    expect(await screen.findByText('old data')).toBeTruthy()
    expect(screen.getByText('Inactive')).toBeTruthy()

    // No active field, so nothing to edit, and the inactive one is never sent.
    expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull()
    expect(put).not.toHaveBeenCalled()
  })

  it('keeps an inactive field read-only while the active ones are being edited, and does not send it', async () => {
    const region = def('f1', 'Region')
    const legacy = def('f2', 'Legacy', { active: false })
    const put = vi.spyOn(ticketsApi, 'putTicketCustomFields').mockResolvedValue()
    setup({
      fields: [assignment(region, 'category'), assignment(legacy, 'category')],
      values: [held(legacy, 'old data')],
      staff: true,
    })

    await userEvent.click(await screen.findByRole('button', { name: 'Edit' }))
    expect(screen.getByLabelText('Region')).toBeTruthy()
    expect(screen.queryByLabelText('Legacy')).toBeNull()
    expect(screen.getByText('old data')).toBeTruthy()

    await userEvent.type(screen.getByLabelText('Region'), 'EMEA')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(put).toHaveBeenCalledWith('t1', { f1: 'EMEA' }))
  })

  it('does not show an inactive field that holds no value', async () => {
    const region = def('f1', 'Region')
    const legacy = def('f2', 'Legacy', { active: false })
    setup({ fields: [assignment(region, 'category'), assignment(legacy, 'category')], values: [], staff: true })

    expect(await screen.findByText('Region')).toBeTruthy()
    expect(screen.queryByText('Legacy')).toBeNull()
    expect(screen.queryByText('Inactive')).toBeNull()
  })

  it('keeps the order the server resolved, grouped by level, and puts a detached field last', async () => {
    const tier = def('f1', 'Tier')
    const order = def('f2', 'Order')
    const region = def('f3', 'Region')
    const stray = def('f4', 'Stray')
    setup({
      // The resolver already groups by level: category, then type, then item.
      fields: [assignment(tier, 'category'), assignment(order, 'type'), assignment(region, 'item')],
      // The value list comes back in a different order, and holds a field the
      // ticket's classification no longer attaches.
      values: [held(stray, 'x'), held(region, 'r'), held(tier, 't')],
      staff: true,
    })

    await screen.findByText('Tier')
    expect(labels()).toEqual(['Tier', 'Order', 'Region', 'Stray'])
  })

  it('shows a detached field that holds a value as read-only', async () => {
    const region = def('f1', 'Region')
    const stray = def('f2', 'Stray')
    setup({ fields: [assignment(region, 'category')], values: [held(stray, 'left behind')], staff: true })

    await userEvent.click(await screen.findByRole('button', { name: 'Edit' }))
    expect(screen.getByText('left behind')).toBeTruthy()
    expect(screen.queryByLabelText('Stray')).toBeNull()
  })

  it('shows nothing when the ticket has no fields at all', async () => {
    const { container } = setup({ fields: [], values: [], staff: true })
    await waitFor(() => expect(ticketsApi.listTicketCustomFields).toHaveBeenCalled())
    expect(container.innerHTML).toBe('')
  })
})

describe('CustomFieldsPanel, requester', () => {
  it('shows only fields that hold a value, in the resolved order, with no Edit', async () => {
    const tier = def('f1', 'Tier')
    const order = def('f2', 'Order')
    const region = def('f3', 'Region')
    const legacy = def('f4', 'Legacy', { active: false })
    setup({
      fields: [assignment(tier, 'category'), assignment(order, 'type'), assignment(region, 'item'), assignment(legacy, 'item')],
      values: [held(region, 'r'), held(tier, 't'), held(legacy, 'old')],
      staff: false,
    })

    await screen.findByText('Tier')
    // Order has no value, so it is absent. Legacy holds one, so it shows.
    expect(labels()).toEqual(['Tier', 'Region', 'Legacy'])
    expect(screen.queryByText('Inactive')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull()
  })

  it('shows nothing when no field holds a value', async () => {
    const region = def('f1', 'Region')
    const { container } = setup({ fields: [assignment(region, 'category')], values: [], staff: false })
    await waitFor(() => expect(ticketsApi.listTicketCustomFields).toHaveBeenCalled())
    expect(container.innerHTML).toBe('')
  })
})
