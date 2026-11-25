import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import * as ticketsApi from '@/api/tickets'
import * as guestApi from '@/api/guest'
import { GuestTicketPage } from './GuestTicketPage'

// The guest form has a category selector and nothing below it, so it shows the
// category's description. That text is public to anyone who can open the form.

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue([
    { id: 'cat-hw', name: 'Hardware', description: 'Laptops, docks and monitors.', active: true, sort_order: 1 },
    { id: 'cat-sw', name: 'Software', description: '', active: true, sort_order: 2 },
    { id: 'cat-x', name: 'Markup', description: '<img src=x onerror=alert(1)>', active: true, sort_order: 3 },
  ])
  vi.spyOn(guestApi, 'listGuestFields').mockResolvedValue([])
})

function region(): HTMLElement {
  const id = screen.getByLabelText(/category/i).getAttribute('aria-describedby')
  expect(id).toBeTruthy()
  return document.getElementById(id!)!
}

describe('the guest form', () => {
  it("shows the chosen category's description and clears it with the choice", async () => {
    const user = userEvent.setup()
    renderWithQuery(<GuestTicketPage />)
    await screen.findByRole('option', { name: 'Hardware' })
    expect(region().textContent).toBe('')

    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    expect(region().textContent).toBe('Laptops, docks and monitors.')
    expect(region().getAttribute('aria-live')).toBe('polite')

    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-sw')
    expect(region().textContent).toBe('')

    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-hw')
    await user.selectOptions(screen.getByLabelText(/category/i), '')
    expect(region().textContent).toBe('')
  })

  it('shows markup as text', async () => {
    const user = userEvent.setup()
    renderWithQuery(<GuestTicketPage />)
    await screen.findByRole('option', { name: 'Markup' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-x')
    expect(region().textContent).toBe('<img src=x onerror=alert(1)>')
    expect(region().querySelector('img')).toBeNull()
  })
})
