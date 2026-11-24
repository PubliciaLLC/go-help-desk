import { describe, expect, it, vi, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { AvailableQueueSection } from './AvailableQueueSection'

// #676: who may open the Available view. The control reads the settings dump
// itself and writes only available_queue_roles, so it is tested on its own.

afterEach(() => vi.restoreAllMocks())

function settingsAre(settings: Record<string, unknown>) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: settings })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

function refusedWith(status: number, message: string) {
  return new AxiosError('refused', String(status), undefined, undefined, {
    status, statusText: '', headers: {}, config: {} as never,
    data: { error: { code: 'bad_request', message } },
  })
}

describe('AvailableQueueSection', () => {
  it('loads with the heading and the explanation', async () => {
    settingsAre({})
    renderWithQuery(<AvailableQueueSection />)

    expect(await screen.findByRole('heading', { name: 'Available queue' })).toBeTruthy()
    expect(
      screen.getByText('Who can open the Available view: tickets with no group and no person assigned.'),
    ).toBeTruthy()
  })

  it('shows Administrators ticked and disabled', async () => {
    settingsAre({ available_queue_roles: [] })
    renderWithQuery(<AvailableQueueSection />)

    const admins = await screen.findByRole('checkbox', { name: /administrators/i })
    expect((admins as HTMLInputElement).checked).toBe(true)
    expect((admins as HTMLInputElement).disabled).toBe(true)
    expect(screen.getByText('always')).toBeTruthy()
  })

  it('reads a missing key as admins only, so Staff is unticked', async () => {
    settingsAre({})
    renderWithQuery(<AvailableQueueSection />)

    const staff = await screen.findByRole('checkbox', { name: 'Staff' })
    expect((staff as HTMLInputElement).checked).toBe(false)
  })

  it('ticking Staff saves both roles', async () => {
    settingsAre({})
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)
    renderWithQuery(<AvailableQueueSection />)

    await userEvent.click(await screen.findByRole('checkbox', { name: 'Staff' }))
    await userEvent.click(screen.getByRole('button', { name: /^save$/i }))
    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith('/admin/settings', { available_queue_roles: ['admin', 'staff'] }),
    )
  })

  it('unticking Staff, from a stored staff value, saves admin only', async () => {
    settingsAre({ available_queue_roles: ['admin', 'staff'] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)
    renderWithQuery(<AvailableQueueSection />)

    const staff = screen.getByRole('checkbox', { name: 'Staff' }) as HTMLInputElement
    await waitFor(() => expect(staff.checked).toBe(true))
    await userEvent.click(staff)
    await userEvent.click(screen.getByRole('button', { name: /^save$/i }))
    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith('/admin/settings', { available_queue_roles: ['admin'] }),
    )
  })

  it('shows the server refusal inline', async () => {
    settingsAre({ available_queue_roles: ['admin'] })
    vi.spyOn(api, 'patch').mockRejectedValue(
      refusedWith(400, 'available_queue_roles may list only admin and staff'),
    )
    renderWithQuery(<AvailableQueueSection />)

    await userEvent.click(await screen.findByRole('checkbox', { name: 'Staff' }))
    await userEvent.click(screen.getByRole('button', { name: /^save$/i }))

    expect(await screen.findByText('available_queue_roles may list only admin and staff')).toBeTruthy()
  })

  // Not the page's ['admin','settings'] query: refetching that one would make
  // the Settings page drop its unsaved edits.
  it('invalidates the me query and its own settings query after a successful save, not the page-wide one', async () => {
    settingsAre({})
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)
    const { queryClient } = renderWithQuery(<AvailableQueueSection />)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')

    await userEvent.click(await screen.findByRole('checkbox', { name: 'Staff' }))
    await userEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ['me'] }))
    await waitFor(() =>
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['admin', 'settings', 'available_queue_roles'], exact: true }),
    )
    expect(invalidate).not.toHaveBeenCalledWith({ queryKey: ['admin', 'settings'] })
  })

  it('shows the enforcement sentence only while ticket scope enforcement is on', async () => {
    settingsAre({ ticket_scope_enforced: false })
    const { unmount } = renderWithQuery(<AvailableQueueSection />)
    await screen.findByRole('checkbox', { name: 'Staff' })
    expect(screen.queryByText(/With Limit staff to their group scope on/)).toBeNull()
    unmount()

    settingsAre({ ticket_scope_enforced: true })
    renderWithQuery(<AvailableQueueSection />)
    expect(
      await screen.findByText(
        "With Limit staff to their group scope on, staff see only the available tickets in their groups' categories. Tick Staff if your staff pick up new tickets themselves: otherwise they find them only through Search.",
      ),
    ).toBeTruthy()
  })
})
