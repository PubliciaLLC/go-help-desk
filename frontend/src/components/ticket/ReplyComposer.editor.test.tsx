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
  it('a canned response is added after what is written, as its own paragraph', async () => {
    const user = userEvent.setup()
    renderWithQuery(<ReplyComposer ticketId="t" isStaffOrAdmin />)
    await waitFor(() => expect(document.querySelector('[contenteditable="true"]')).not.toBeNull())
    await user.click(screen.getByRole('button', { name: /switch to plain editor/i }))
    await user.type(screen.getByRole('textbox'), 'first')
    await user.click(screen.getByRole('button', { name: /insert canned response/i }))
    await user.click(await screen.findByRole('button', { name: /greeting/i }))
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('first\n\nHello **there**')
  })

  it('a canned response reaches the rich editor through its value', async () => {
    const user = userEvent.setup()
    renderWithQuery(<ReplyComposer ticketId="t" isStaffOrAdmin />)
    await waitFor(() => expect(document.querySelector('[contenteditable="true"]')).not.toBeNull())
    await user.click(screen.getByRole('button', { name: /insert canned response/i }))
    await user.click(await screen.findByRole('button', { name: /greeting/i }))
    await waitFor(() => expect(document.querySelector('.md-editor strong')?.textContent).toBe('there'))
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
