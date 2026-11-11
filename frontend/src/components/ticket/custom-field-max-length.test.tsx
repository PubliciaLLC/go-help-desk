import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { CustomFieldsPanel } from './CustomFieldsPanel'
import * as ticketsApi from '@/api/tickets'
import type { Assignment, FieldDef, TicketFieldValue } from '@/api/types'
import { charCount, overLimit } from '@/lib/fieldLimits'

// A field's longest value (its own limit, else its type's) is told to the forms
// as effective_max_length: they set maxlength and refuse an over-long value
// before calling the server. The ticket page's edit box is one of them.

function def(id: string, name: string, over: Partial<FieldDef> = {}): FieldDef {
  return { id, name, field_type: 'text', sort_order: 1, active: true, created_at: '', effective_max_length: 255, ...over }
}
function assignment(d: FieldDef): Assignment {
  return { id: `a-${d.id}`, field_def_id: d.id, field_def: d, scope_type: 'category', scope_id: 's', sort_order: 1, visible_on_new: true, required_on_new: false }
}
function held(d: FieldDef, value: string): TicketFieldValue {
  return { ticket_id: 't1', field_def_id: d.id, field_name: d.name, field_type: d.field_type, value, updated_at: '', effective_max_length: d.effective_max_length }
}

beforeEach(() => vi.restoreAllMocks())

function setup(d: FieldDef, value = '') {
  vi.spyOn(ticketsApi, 'resolveFieldsForCTI').mockResolvedValue([assignment(d)])
  vi.spyOn(ticketsApi, 'listTicketCustomFields').mockResolvedValue(value ? [held(d, value)] : [])
  return renderWithQuery(<CustomFieldsPanel ticketId="t1" categoryId="c1" isStaffOrAdmin />)
}

describe('CustomFieldsPanel: maximum length', () => {
  it('sets maxlength on a text box and a text area from what the server says applies', async () => {
    setup(def('f1', 'Short', { max_length: 100, effective_max_length: 100 }))
    await userEvent.click(await screen.findByRole('button', { name: 'Edit' }))
    expect((screen.getByLabelText('Short') as HTMLInputElement).maxLength).toBe(100)
  })

  it('uses the text area limit for a text area', async () => {
    setup(def('f1', 'Log', { field_type: 'textarea', effective_max_length: 10000 }))
    await userEvent.click(await screen.findByRole('button', { name: 'Edit' }))
    expect((screen.getByLabelText('Log') as HTMLTextAreaElement).maxLength).toBe(10000)
  })

  it('sets none on a field that reports none (a number, a select)', async () => {
    setup(def('f1', 'Cost', { field_type: 'number', effective_max_length: undefined }))
    await userEvent.click(await screen.findByRole('button', { name: 'Edit' }))
    expect((screen.getByLabelText('Cost') as HTMLInputElement).getAttribute('maxlength')).toBeNull()
  })

  it('refuses an over-long value, says so, and does not call the server', async () => {
    const put = vi.spyOn(ticketsApi, 'putTicketCustomFields').mockResolvedValue()
    setup(def('f1', 'Short', { max_length: 10, effective_max_length: 10 }))
    await userEvent.click(await screen.findByRole('button', { name: 'Edit' }))
    // maxlength stops typing; a paste or script is what gets past it.
    fireEvent.change(screen.getByLabelText('Short'), { target: { value: 'a'.repeat(11) } })
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect((await screen.findByRole('alert')).textContent).toBe('Short is too long: at most 10 characters.')
    expect(put).not.toHaveBeenCalled()
  })

  it('counts characters, not UTF-16 units: ten emoji fit in ten', async () => {
    const put = vi.spyOn(ticketsApi, 'putTicketCustomFields').mockResolvedValue()
    setup(def('f1', 'Short', { max_length: 10, effective_max_length: 10 }))
    await userEvent.click(await screen.findByRole('button', { name: 'Edit' }))
    fireEvent.change(screen.getByLabelText('Short'), { target: { value: '😀'.repeat(10) } })
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(put).toHaveBeenCalledWith('t1', { f1: '😀'.repeat(10) }))
  })

  it('does not hold a value that is already there, and unchanged, to a limit lowered since', async () => {
    const put = vi.spyOn(ticketsApi, 'putTicketCustomFields').mockResolvedValue()
    const d = def('f1', 'Short', { max_length: 10, effective_max_length: 10 })
    setup(d, 'a'.repeat(40))
    await userEvent.click(await screen.findByRole('button', { name: 'Edit' }))
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(screen.queryByRole('alert')).toBeNull()
    expect(put).not.toHaveBeenCalled() // nothing changed, so nothing is sent
  })
})

describe('field limit helpers', () => {
  it('counts code points', () => {
    expect(charCount('a😀中')).toBe(3)
    expect('😀'.length).toBe(2)
  })
  it('says nothing for a value that fits or a field with no limit', () => {
    expect(overLimit('F', 'abc', 3)).toBe('')
    expect(overLimit('F', 'abc', undefined)).toBe('')
  })
  it('names the field and the limit, with a thousands separator', () => {
    expect(overLimit('Log', 'a'.repeat(10001), 10000)).toBe('Log is too long: at most 10,000 characters.')
  })
})
