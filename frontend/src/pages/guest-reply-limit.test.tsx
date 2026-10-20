import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, screen } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'

// The guest's reply form will not send what the server would refuse (50,000).

const { getGuestTicket, addGuestReply } = vi.hoisted(() => ({ getGuestTicket: vi.fn(), addGuestReply: vi.fn() }))
vi.mock('@/api/guest', async (orig) => ({ ...(await orig<typeof import('@/api/guest')>()), getGuestTicket, addGuestReply }))
vi.mock('@/components/editor/MarkdownEditor', () => ({
  MarkdownEditor: (p: { value: string; onChange: (v: string) => void }) => (
    <textarea aria-label="Add a reply" value={p.value} onChange={(e) => p.onChange(e.target.value)} />
  ),
}))

import { GuestTicketViewPage } from './GuestTicketViewPage'

beforeEach(() => {
  getGuestTicket.mockReset()
  addGuestReply.mockReset()
  window.location.hash = '#some-token'
  getGuestTicket.mockResolvedValue({
    tracking_number: 'GHD-1', subject: 's', description: 'd', description_format: 'plain', status: 'New',
    created_at: '2026-09-20T10:00:00Z', replies: [],
  })
})

describe('guest reply length', () => {
  it('sends at the limit', async () => {
    addGuestReply.mockResolvedValue({})
    renderWithQuery(<GuestTicketViewPage />)
    fireEvent.change(await screen.findByRole('textbox', { name: 'Add a reply' }), { target: { value: 'a'.repeat(50_000) } })
    expect(screen.getByRole('button', { name: 'Send reply' })).toHaveProperty('disabled', false)
  })

  it('will not send one over, and says so', async () => {
    renderWithQuery(<GuestTicketViewPage />)
    fireEvent.change(await screen.findByRole('textbox', { name: 'Add a reply' }), { target: { value: 'a'.repeat(50_001) } })
    expect(screen.getByRole('button', { name: 'Send reply' })).toHaveProperty('disabled', true)
    expect(screen.getByRole('alert').textContent).toMatch(/1% over the size limit/)
    fireEvent.submit(screen.getByRole('button', { name: 'Send reply' }).closest('form')!)
    expect(addGuestReply).not.toHaveBeenCalled()
  })
})
