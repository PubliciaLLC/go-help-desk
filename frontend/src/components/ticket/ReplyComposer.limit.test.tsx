import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, screen } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { ReplyComposer } from './ReplyComposer'
import * as ticketsApi from '@/api/tickets'

// The composer refuses to send a reply the server would refuse (50,000 characters).
vi.mock('@/components/editor/MarkdownEditor', () => ({
  MarkdownEditor: (p: { value: string; onChange: (v: string) => void }) => (
    <textarea value={p.value} onChange={(e) => p.onChange(e.target.value)} />
  ),
}))

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(ticketsApi, 'listTicketCannedResponses').mockResolvedValue([])
})

describe('ReplyComposer length limit', () => {
  it('sends a reply at the limit', async () => {
    const add = vi.spyOn(ticketsApi, 'addReply').mockResolvedValue({} as never)
    renderWithQuery(<ReplyComposer ticketId="t" isStaffOrAdmin />)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'a'.repeat(50_000) } })
    expect(screen.getByRole('button', { name: 'Save entry' })).toHaveProperty('disabled', false)
    fireEvent.click(screen.getByRole('button', { name: 'Save entry' }))
    await vi.waitFor(() => expect(add).toHaveBeenCalled())
  })

  it('will not send one character over, and says how far over', () => {
    const add = vi.spyOn(ticketsApi, 'addReply').mockResolvedValue({} as never)
    renderWithQuery(<ReplyComposer ticketId="t" isStaffOrAdmin />)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'a'.repeat(50_001) } })
    expect(screen.getByRole('button', { name: 'Save entry' })).toHaveProperty('disabled', true)
    expect(screen.getByRole('alert').textContent).toMatch(/1 too many/)
    fireEvent.click(screen.getByRole('button', { name: 'Save entry' }))
    expect(add).not.toHaveBeenCalled()
  })

  it('counts bytes, like the server', () => {
    renderWithQuery(<ReplyComposer ticketId="t" isStaffOrAdmin />)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'é'.repeat(25_001) } })
    expect(screen.getByRole('button', { name: 'Save entry' })).toHaveProperty('disabled', true)
  })

  it('shows the counter near the limit', () => {
    renderWithQuery(<ReplyComposer ticketId="t" isStaffOrAdmin />)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'a'.repeat(46_000) } })
    expect(screen.getByText(/46,000 \/ 50,000/)).toBeTruthy()
  })
})
