import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import * as ticketsApi from '@/api/tickets'

// The public form offers the custom fields an administrator chose for the
// category (#627), sends only those, and holds a visitor to the required ones.
// The server is the authority (a bad value is a 400); the form saves them the
// round trip.

const { createGuestTicket, listGuestFields } = vi.hoisted(() => ({
  createGuestTicket: vi.fn(),
  listGuestFields: vi.fn(),
}))
vi.mock('@/api/guest', async (orig) => ({
  ...(await orig<typeof import('@/api/guest')>()),
  createGuestTicket,
  listGuestFields,
}))
vi.mock('@/components/editor/MarkdownEditor', () => ({
  MarkdownEditor: (p: { value: string; onChange: (v: string) => void }) => (
    <textarea aria-label="Description text" value={p.value} onChange={(e) => p.onChange(e.target.value)} />
  ),
}))

import { GuestTicketPage } from './GuestTicketPage'

const SERIAL = { id: 'f-serial', name: 'Serial number', field_type: 'text', required: true }
const COLOUR = { id: 'f-colour', name: 'Colour', field_type: 'select', options: ['red', 'blue'], required: false }

beforeEach(() => {
  vi.restoreAllMocks()
  createGuestTicket.mockReset().mockResolvedValue('GHD-1')
  listGuestFields.mockReset().mockImplementation(async (categoryId: string) =>
    categoryId === 'cat-1' ? [SERIAL, COLOUR] : [])
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue([
    { id: 'cat-1', name: 'Network', active: true, sort_order: 1 },
    { id: 'cat-2', name: 'Printers', active: true, sort_order: 2 },
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ] as any)
})

async function fillBasics() {
  const user = userEvent.setup()
  renderWithQuery(<GuestTicketPage />)
  await user.type(screen.getByLabelText(/^name/i), 'Ada')
  await user.type(screen.getByLabelText(/^email/i), 'ada@example.com')
  await user.type(screen.getByLabelText(/subject/i), 'Printer')
  await screen.findByRole('option', { name: 'Network' })
  return user
}
const submit = () => screen.getByRole('button', { name: /submit request/i })

describe('GuestTicketPage custom fields', () => {
  it('asks for no fields until a category is chosen', async () => {
    await fillBasics()
    expect(listGuestFields).not.toHaveBeenCalled()
  })

  it('shows the fields offered for the category, required ones marked', async () => {
    const user = await fillBasics()
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')

    expect(await screen.findByLabelText(/serial number/i)).toBeTruthy()
    expect(screen.getByText(/serial number/i).textContent).toContain('*')
    const colour = screen.getByLabelText(/colour/i)
    expect(Array.from(colour.querySelectorAll('option')).map((o) => o.textContent)).toEqual(['Select…', 'red', 'blue'])
    expect(screen.getByText(/colour/i).textContent).not.toContain('*')
    expect(listGuestFields).toHaveBeenCalledWith('cat-1')
  })

  it('sends what was filled in, keyed by field id', async () => {
    const user = await fillBasics()
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')
    await user.type(await screen.findByLabelText(/serial number/i), 'SN-42')
    await user.selectOptions(screen.getByLabelText(/colour/i), 'blue')
    await user.click(submit())

    await waitFor(() => expect(createGuestTicket).toHaveBeenCalledTimes(1))
    expect(createGuestTicket.mock.calls[0][0]).toMatchObject({
      category_id: 'cat-1',
      custom_fields: { 'f-serial': 'SN-42', 'f-colour': 'blue' },
    })
  })

  it('leaves out a field that was not filled in', async () => {
    const user = await fillBasics()
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')
    await user.type(await screen.findByLabelText(/serial number/i), 'SN-42')
    await user.click(submit())

    await waitFor(() => expect(createGuestTicket).toHaveBeenCalledTimes(1))
    expect(createGuestTicket.mock.calls[0][0].custom_fields).toEqual({ 'f-serial': 'SN-42' })
  })

  it('holds the visitor to a required field before calling the server', async () => {
    const user = await fillBasics()
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')
    await screen.findByLabelText(/serial number/i)
    await user.click(submit())

    expect((await screen.findByRole('alert')).textContent).toMatch(/serial number is required/i)
    expect(createGuestTicket).not.toHaveBeenCalled()
  })

  it('counts spaces as no value', async () => {
    const user = await fillBasics()
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')
    await user.type(await screen.findByLabelText(/serial number/i), '   ')
    await user.click(submit())

    expect((await screen.findByRole('alert')).textContent).toMatch(/serial number is required/i)
    expect(createGuestTicket).not.toHaveBeenCalled()
  })

  it('does not carry values to another category, which may not offer the field', async () => {
    const user = await fillBasics()
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')
    await user.type(await screen.findByLabelText(/serial number/i), 'SN-42')
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-2')
    await waitFor(() => expect(screen.queryByLabelText(/serial number/i)).toBeNull())
    await user.click(submit())

    await waitFor(() => expect(createGuestTicket).toHaveBeenCalledTimes(1))
    expect(createGuestTicket.mock.calls[0][0].custom_fields).toBeUndefined()
  })

  it('files a ticket as before when the category offers nothing', async () => {
    const user = await fillBasics()
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-2')
    await user.click(submit())

    await waitFor(() => expect(createGuestTicket).toHaveBeenCalledTimes(1))
    expect(createGuestTicket.mock.calls[0][0].custom_fields).toBeUndefined()
  })

  // The lookup is throttled (429) and can fail like any request. A form that went
  // on without the list would ask for nothing and then be refused for a required
  // field it never showed, with nothing to fill in. So it says so, offers a retry,
  // and does not submit until the fields are known.
  describe('when the field list cannot be loaded', () => {
    function rateLimited() {
      const err = Object.assign(new Error('Request failed with status code 429'), { isAxiosError: true, response: { status: 429 } })
      return err
    }

    it('says to try again in a moment, and blocks submit', async () => {
      listGuestFields.mockReset().mockRejectedValue(rateLimited())
      const user = await fillBasics()
      await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')

      expect((await screen.findByRole('alert')).textContent).toMatch(/try again in a moment/i)
      expect(submit()).toHaveProperty('disabled', true)
      expect(screen.getByRole('button', { name: /try again/i })).toBeTruthy()
      expect(createGuestTicket).not.toHaveBeenCalled()
    })

    it('does the same for any other failure', async () => {
      listGuestFields.mockReset().mockRejectedValue(new Error('offline'))
      const user = await fillBasics()
      await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')

      expect((await screen.findByRole('alert')).textContent).toMatch(/try again/i)
      expect(submit()).toHaveProperty('disabled', true)
    })

    it('Try again asks again, and the fields appear and submit works', async () => {
      listGuestFields.mockReset()
        .mockRejectedValueOnce(rateLimited())
        .mockResolvedValue([SERIAL])
      const user = await fillBasics()
      await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')
      await user.click(await screen.findByRole('button', { name: /try again/i }))

      await user.type(await screen.findByLabelText(/serial number/i), 'SN-42')
      await waitFor(() => expect(submit()).toHaveProperty('disabled', false))
      await user.click(submit())
      await waitFor(() => expect(createGuestTicket).toHaveBeenCalledTimes(1))
      expect(createGuestTicket.mock.calls[0][0].custom_fields).toEqual({ 'f-serial': 'SN-42' })
      expect(listGuestFields).toHaveBeenCalledTimes(2)
    })

    it('blocks submit while the fields are still loading', async () => {
      listGuestFields.mockReset().mockReturnValue(new Promise(() => {}))
      const user = await fillBasics()
      await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')

      await waitFor(() => expect(listGuestFields).toHaveBeenCalled())
      expect(submit()).toHaveProperty('disabled', true)
    })
  })
})
