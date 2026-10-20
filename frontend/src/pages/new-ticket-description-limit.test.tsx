import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { useAuthStore } from '@/store/auth'
import * as ticketsApi from '@/api/tickets'

// The description is held to 50,000 bytes, as the server holds it. The form must
// send one at exactly the limit, refuse one byte more with an alert, and never
// reach the API with it.

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }))
vi.mock('@/components/Layout', () => ({ Layout: ({ children }: { children: React.ReactNode }) => <>{children}</> }))
vi.mock('@/components/editor/MarkdownEditor', () => ({
  MarkdownEditor: (p: { value: string; onChange: (v: string) => void }) => (
    <textarea aria-label="Description text" value={p.value} onChange={(e) => p.onChange(e.target.value)} />
  ),
}))

import { NewTicketPage } from './NewTicketPage'

let createTicket: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: { id: 'u', email: 'u@example.com', display_name: 'U', role: 'user', mfa_enabled: false, created_at: '', updated_at: '' },
  })
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue([{ id: 'cat-1', name: 'Network', active: true, sort_order: 1 }] as any)
  vi.spyOn(ticketsApi, 'listPublicTypes').mockResolvedValue([])
  vi.spyOn(ticketsApi, 'listPublicItems').mockResolvedValue([])
  vi.spyOn(ticketsApi, 'resolveFieldsForCTI').mockResolvedValue([])
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  createTicket = vi.spyOn(ticketsApi, 'createTicket').mockResolvedValue({ id: 't-1' } as any)
})

async function fill(description: string) {
  const user = userEvent.setup()
  renderWithQuery(<NewTicketPage />)
  await user.type(screen.getByLabelText(/subject/i), 'Printer')
  await screen.findByRole('option', { name: 'Network' })
  await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')
  fireEvent.change(screen.getByLabelText('Description text'), { target: { value: description } })
  return user
}
const submit = () => screen.getByRole('button', { name: /submit|create|open/i })

describe('NewTicketPage description limit', () => {
  it('sends a description of exactly 50,000 bytes', async () => {
    const user = await fill('a'.repeat(50_000))
    await user.click(submit())
    await waitFor(() => expect(createTicket).toHaveBeenCalledTimes(1))
  })

  it('refuses one byte more: button disabled, alert shown, API never called', async () => {
    const user = await fill('a'.repeat(50_001))
    expect(submit()).toHaveProperty('disabled', true)
    expect(screen.getByRole('alert').textContent).toMatch(/over the size limit \(50,000 bytes\)/)
    await user.click(submit())
    fireEvent.submit(submit().closest('form')!)
    expect(createTicket).not.toHaveBeenCalled()
  })

  it('counts bytes: 16,667 characters of 3 bytes each is 50,001 bytes and is refused', async () => {
    const user = await fill('中'.repeat(16_667))
    expect(submit()).toHaveProperty('disabled', true)
    fireEvent.submit(submit().closest('form')!)
    expect(createTicket).not.toHaveBeenCalled()
    await user.clear(screen.getByLabelText('Description text'))
    fireEvent.change(screen.getByLabelText('Description text'), { target: { value: '中'.repeat(16_666) } })
    expect(submit()).toHaveProperty('disabled', false)
  })
})
