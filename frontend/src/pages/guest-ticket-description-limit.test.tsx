import { describe, expect, it, vi, beforeEach } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import * as ticketsApi from '@/api/tickets'

// The guest form holds the description to 50,000 bytes too.

const { createGuestTicket } = vi.hoisted(() => ({ createGuestTicket: vi.fn() }))
vi.mock('@/api/guest', async (orig) => ({ ...(await orig<typeof import('@/api/guest')>()), createGuestTicket }))
vi.mock('@/components/editor/MarkdownEditor', () => ({
  MarkdownEditor: (p: { value: string; onChange: (v: string) => void }) => (
    <textarea aria-label="Description text" value={p.value} onChange={(e) => p.onChange(e.target.value)} />
  ),
}))

import { GuestTicketPage } from './GuestTicketPage'

beforeEach(() => {
  vi.restoreAllMocks()
  createGuestTicket.mockReset().mockResolvedValue('GHD-1')
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue([{ id: 'cat-1', name: 'Network', active: true, sort_order: 1 }] as any)
})

async function fill(description: string) {
  const user = userEvent.setup()
  renderWithQuery(<GuestTicketPage />)
  await user.type(screen.getByLabelText(/name/i), 'Ada')
  await user.type(screen.getByLabelText(/email/i), 'ada@example.com')
  await user.type(screen.getByLabelText(/subject/i), 'Printer')
  await screen.findByRole('option', { name: 'Network' })
  await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')
  fireEvent.change(screen.getByLabelText('Description text'), { target: { value: description } })
  return user
}
const submit = () => screen.getByRole('button', { name: /submit|send|create/i })

describe('GuestTicketPage description limit', () => {
  it('sends a description of exactly 50,000 bytes', async () => {
    const user = await fill('a'.repeat(50_000))
    await user.click(submit())
    await waitFor(() => expect(createGuestTicket).toHaveBeenCalledTimes(1))
  })

  it('refuses one byte more: button disabled, alert shown, API never called', async () => {
    const user = await fill('a'.repeat(50_001))
    expect(submit()).toHaveProperty('disabled', true)
    expect(screen.getByRole('alert').textContent).toMatch(/over the size limit \(50,000 bytes\)/)
    await user.click(submit())
    fireEvent.submit(submit().closest('form')!)
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50))
    })
    expect(createGuestTicket).not.toHaveBeenCalled()
  })

  it('counts bytes: 16,667 three-byte characters are refused', async () => {
    await fill('中'.repeat(16_667))
    expect(submit()).toHaveProperty('disabled', true)
    fireEvent.submit(submit().closest('form')!)
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50))
    })
    expect(createGuestTicket).not.toHaveBeenCalled()
  })
})
