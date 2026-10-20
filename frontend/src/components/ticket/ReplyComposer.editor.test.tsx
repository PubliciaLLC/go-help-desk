import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { ReplyComposer } from './ReplyComposer'
import * as ticketsApi from '@/api/tickets'

// ReplyComposer with the real editor (#443).

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(ticketsApi, 'listTicketCannedResponses').mockResolvedValue([
    { id: 'c1', name: 'Greeting', body: 'Hello **there**', sort_order: 1, created_at: '' },
  ])
})

describe('ReplyComposer with the editor', () => {
  // The textarea has always put a canned response at the caret and left the caret
  // after it; "No breaking changes" says that stays true in the plain editor.
  it('in the plain editor a canned response lands at the caret, which ends after it', async () => {
    const user = userEvent.setup()
    renderWithQuery(<ReplyComposer ticketId="t" isStaffOrAdmin />)
    await waitFor(() => expect(document.querySelector('[contenteditable="true"]')).not.toBeNull())
    await user.click(screen.getByRole('button', { name: /switch to plain editor/i }))
    const ta = screen.getByRole('textbox') as HTMLTextAreaElement
    await user.type(ta, 'hello world')
    ta.setSelectionRange(5, 5)
    await user.click(screen.getByRole('button', { name: /insert canned response/i }))
    await user.click(await screen.findByRole('button', { name: /greeting/i }))
    const after = screen.getByRole('textbox') as HTMLTextAreaElement
    expect(after.value).toBe('helloHello **there** world')
    await waitFor(() => {
      expect(after.selectionStart).toBe('helloHello **there**'.length)
      expect(after.selectionEnd).toBe('helloHello **there**'.length)
    })
  })

  it('in the plain editor a selection is replaced by the canned response', async () => {
    const user = userEvent.setup()
    renderWithQuery(<ReplyComposer ticketId="t" isStaffOrAdmin />)
    await waitFor(() => expect(document.querySelector('[contenteditable="true"]')).not.toBeNull())
    await user.click(screen.getByRole('button', { name: /switch to plain editor/i }))
    const ta = screen.getByRole('textbox') as HTMLTextAreaElement
    await user.type(ta, 'hello big world')
    ta.setSelectionRange(5, 9)
    await user.click(screen.getByRole('button', { name: /insert canned response/i }))
    await user.click(await screen.findByRole('button', { name: /greeting/i }))
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('helloHello **there** world')
  })

  it('in the rich editor a canned response is formatted and reaches the form value', async () => {
    const user = userEvent.setup()
    const add = vi.spyOn(ticketsApi, 'addReply').mockResolvedValue({} as never)
    renderWithQuery(<ReplyComposer ticketId="t" isStaffOrAdmin />)
    await waitFor(() => expect(document.querySelector('[contenteditable="true"]')).not.toBeNull())
    await user.click(screen.getByRole('button', { name: /insert canned response/i }))
    await user.click(await screen.findByRole('button', { name: /greeting/i }))
    await waitFor(() => expect(document.querySelector('.md-editor strong')?.textContent).toBe('there'))
    await user.click(screen.getByRole('button', { name: 'Save entry' }))
    expect(add).toHaveBeenCalledWith('t', 'Hello **there**', false, true)
  })

  it('sends the reply as Markdown', async () => {
    const user = userEvent.setup()
    const add = vi.spyOn(ticketsApi, 'addReply').mockResolvedValue({} as never)
    renderWithQuery(<ReplyComposer ticketId="t" isStaffOrAdmin />)
    await waitFor(() => expect(document.querySelector('[contenteditable="true"]')).not.toBeNull())
    await user.click(screen.getByRole('button', { name: /switch to plain editor/i }))
    await user.type(screen.getByRole('textbox'), 'a **b**')
    await user.click(screen.getByRole('button', { name: 'Save entry' }))
    expect(add).toHaveBeenCalledWith('t', 'a **b**', false, true)
  })
})
