import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { ReplyComposer } from './ReplyComposer'
import * as ticketsApi from '@/api/tickets'

// What is pinned here is the behaviour that was hard to see while this lived
// inline in TicketDetailPage's 676-line body: the coupling between the two
// checkboxes, which decides whether a customer gets emailed, and the ordering
// between the reply and its attachments.

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(ticketsApi, 'listTicketCannedResponses').mockResolvedValue([])
})

function renderComposer(isStaffOrAdmin = true) {
  return renderWithQuery(<ReplyComposer ticketId="tkt-1" isStaffOrAdmin={isStaffOrAdmin} />)
}

describe('posting a reply', () => {
  // The button guards against posting an empty body, and against a body that
  // is only whitespace — which would otherwise create a blank entry in the
  // customer-visible timeline.
  it('stays disabled until the body has real content', async () => {
    const user = userEvent.setup()
    renderComposer()

    const save = screen.getByRole('button', { name: 'Save entry' })
    expect(save).toHaveProperty('disabled', true)

    const textarea = screen.getByRole('textbox')
    await user.type(textarea, '   ')
    expect(screen.getByRole('button', { name: 'Save entry' })).toHaveProperty('disabled', true)

    await user.type(textarea, 'real content')
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Save entry' })).toHaveProperty('disabled', false)
    )
  })

  it('sends the body with the current flags', async () => {
    const add = vi.spyOn(ticketsApi, 'addReply').mockResolvedValue({} as never)
    const user = userEvent.setup()
    renderComposer()

    await user.type(screen.getByRole('textbox'), 'Swapped the switch')
    await user.click(screen.getByRole('button', { name: 'Save entry' }))

    await waitFor(() => expect(add).toHaveBeenCalled())
    expect(add).toHaveBeenCalledWith('tkt-1', 'Swapped the switch', false, true)
  })

  it('surfaces a server error and keeps the draft', async () => {
    vi.spyOn(ticketsApi, 'addReply').mockRejectedValue(
      Object.assign(new Error('Request failed'), {
        isAxiosError: true,
        toJSON: () => ({}),
        response: { data: { error: { code: 'too_long', message: 'reply is too long' } } },
      })
    )
    const user = userEvent.setup()
    renderComposer()

    await user.type(screen.getByRole('textbox'), 'a reply')
    await user.click(screen.getByRole('button', { name: 'Save entry' }))

    expect(await screen.findByText('reply is too long')).toBeDefined()
    // The draft must survive a failure; retyping a long entry is the worst
    // possible response to "too long".
    expect(screen.getByRole('textbox')).toHaveProperty('value', 'a reply')
  })
})

describe('internal notes', () => {
  // These two checkboxes are coupled, and getting it wrong emails a customer
  // the contents of a staff-only note. Ticking Internal must clear Notify.
  it('turns off the customer email when the note is internal', async () => {
    const add = vi.spyOn(ticketsApi, 'addReply').mockResolvedValue({} as never)
    const user = userEvent.setup()
    renderComposer()

    await user.click(screen.getByRole('checkbox', { name: /internal note/i }))
    // The notify checkbox is hidden entirely while Internal is set, so there is
    // no way to re-enable mail on a note that is not meant to leave the building.
    expect(screen.queryByRole('checkbox', { name: /send ticket update email/i })).toBeNull()

    await user.type(screen.getByRole('textbox'), 'internal only')
    await user.click(screen.getByRole('button', { name: 'Save entry' }))

    await waitFor(() => expect(add).toHaveBeenCalled())
    expect(add).toHaveBeenCalledWith('tkt-1', 'internal only', true, false)
  })

  it('restores the customer email when Internal is unticked', async () => {
    const user = userEvent.setup()
    renderComposer()

    const internal = screen.getByRole('checkbox', { name: /internal note/i })
    await user.click(internal)
    await user.click(internal)

    const notify = await screen.findByRole('checkbox', { name: /send ticket update email/i })
    expect(notify).toHaveProperty('checked', true)
  })
})

describe('reporting users', () => {
  // A reporting user must not be offered internal notes, canned responses or
  // attachments on a reply — those are staff tools.
  it('sees only the reply box', () => {
    renderComposer(false)

    expect(screen.getByRole('button', { name: 'Send reply' })).toBeDefined()
    expect(screen.queryByRole('checkbox', { name: /internal note/i })).toBeNull()
    expect(screen.queryByRole('button', { name: /insert canned response/i })).toBeNull()
  })
})

describe('attachments', () => {
  // Attachments belong to the ticket, not the reply, so they upload only AFTER
  // the reply is created — uploading first orphans files whenever the reply
  // fails to save.
  //
  // An earlier version of these tests never attached a file, which made both
  // assertions vacuous: with no files, uploadAttachment could not have been
  // called by ANY implementation, including one with the ordering inverted.
  // attachFile is what makes them mean something.
  async function attachFile(user: ReturnType<typeof userEvent.setup>, name = 'trace.txt') {
    // The input is visually hidden and driven by a button, so it is addressed
    // directly rather than through the label.
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    expect(input).not.toBeNull()
    await user.upload(input, new File(['log contents'], name, { type: 'text/plain' }))
    // Confirm the component actually took the file; if this regresses, the
    // ordering assertions below would quietly go vacuous again.
    expect(await screen.findByText(name)).toBeDefined()
  }

  it('uploads only after the reply is created', async () => {
    const order: string[] = []
    vi.spyOn(ticketsApi, 'addReply').mockImplementation(async () => {
      order.push('reply')
      return {} as never
    })
    const upload = vi.spyOn(ticketsApi, 'uploadAttachment').mockImplementation(async () => {
      order.push('upload')
      return {} as never
    })

    const user = userEvent.setup()
    renderComposer()
    await user.type(screen.getByRole('textbox'), 'with a file')
    await attachFile(user)
    await user.click(screen.getByRole('button', { name: 'Save entry' }))

    await waitFor(() => expect(upload).toHaveBeenCalled())
    expect(order).toEqual(['reply', 'upload'])
    expect(upload).toHaveBeenCalledWith('tkt-1', expect.any(File))
  })

  it('does not upload when the reply fails', async () => {
    const upload = vi.spyOn(ticketsApi, 'uploadAttachment').mockResolvedValue({} as never)
    vi.spyOn(ticketsApi, 'addReply').mockRejectedValue(new Error('nope'))

    const user = userEvent.setup()
    renderComposer()
    await user.type(screen.getByRole('textbox'), 'with a file')
    await attachFile(user)
    await user.click(screen.getByRole('button', { name: 'Save entry' }))

    await screen.findByText(/nope/i)
    expect(upload).not.toHaveBeenCalled()
  })
})
