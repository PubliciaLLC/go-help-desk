import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError } from 'axios'
import type { QueryClient } from '@tanstack/react-query'
import type { ReactElement } from 'react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The SLA policy add and edit form (#671). It used to be one six-column table
// row inside the narrow Features card, with placeholder-only inputs. This file
// pins what replaced it: every field has a visible label and is found by it;
// add, edit and cancel send the right payloads, including the clear flags; one
// shared form sits outside the scrolling table and is re-pointed from one
// policy to another; focus moves to the Name field when the form opens; and the
// empty-state text never shows beside an open form.
//
// #672 made saves asynchronous: Save closes the form at once and the list shows
// the change marked "Saving…". The list is the server's rows plus an overlay of
// the saves in flight, read from react-query's mutation cache; nothing is written
// to the query cache and there is no rollback. A save that fails drops out of the
// overlay (the row shows what the server has) and is listed above the table with
// a Reopen button, and a save finishing never touches whatever form is open by
// then. The tests for that use deferred promises so each request can be resolved
// or rejected on demand.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  useBlocker: () => ({ status: 'idle' }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { FeaturesSettingsPage } from './settings/FeaturesSettingsPage'
import { GeneralSettingsPage } from './settings/GeneralSettingsPage'

const POLICY = {
  id: 'p1',
  name: 'Critical outage',
  priority: 'critical',
  category_id: 'c1',
  response_target_min: 60,
  resolution_target_min: 240,
}

const POLICY_B = {
  id: 'p2',
  name: 'Printer fleet',
  priority: 'low',
  category_id: 'c2',
  response_target_min: 15,
  resolution_target_min: 90,
}

// What the list endpoint returns. Tests reassign it before or after render.
let policies: (typeof POLICY)[]

beforeEach(() => {
  vi.restoreAllMocks()
  policies = [POLICY]
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin',
      mfa_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: { sla_enabled: true } })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/admin/sla/policies') return Promise.resolve({ data: policies })
    if (url === '/admin/categories') {
      return Promise.resolve({
        data: [
          { id: 'c1', name: 'Network', active: true },
          { id: 'c2', name: 'Hardware', active: true },
        ],
      })
    }
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})

function deferred() {
  let resolve!: (v: unknown) => void
  let reject!: (e: unknown) => void
  const promise = new Promise((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

// Saves in the mutation cache, by status. The cache should hold only the ones
// still pending and the ones that failed.
const saveStatuses = (qc: QueryClient) =>
  qc.getMutationCache().findAll({ mutationKey: ['sla-policy-save'] }).map((m) => m.state.status).sort()

// Waits for every save to have answered, so no refetch runs after the test ends.
const savesAnswered = (qc: QueryClient) =>
  waitFor(() => expect(saveStatuses(qc)).not.toContain('pending'))

const rowOf = (name: string) => screen.getByText(name).closest('tr') as HTMLElement

// Draws another page into the tree the SLA page was rendered in: the way a test
// leaves Features now that each settings page is its own route.
let rerenderPage: (ui: ReactElement) => void

async function renderSLA() {
  const { queryClient, rerender } = renderWithQuery(<FeaturesSettingsPage />)
  rerenderPage = rerender
  await screen.findByRole('heading', { name: 'Features' })
  await waitFor(() => expect(screen.queryByRole('status', { name: 'Loading' })).toBeNull())
  await screen.findByText('Critical outage')
  return queryClient
}

describe('the SLA policy form', () => {
  it('labels every field so it is reachable by label', async () => {
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: '+ Add policy' }))

    expect(screen.getByLabelText('Policy name')).toBeTruthy()
    expect(screen.getByLabelText('Priority')).toBeTruthy()
    expect(screen.getByLabelText('Category')).toBeTruthy()
    expect(screen.getByLabelText('Response target')).toBeTruthy()
    expect(screen.getByLabelText('Resolution target')).toBeTruthy()
  })

  it('adds a policy with the typed fields', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {} } as any)
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: '+ Add policy' }))

    await userEvent.type(screen.getByLabelText('Policy name'), 'Printer fleet')
    await userEvent.selectOptions(screen.getByLabelText('Priority'), 'high')
    await userEvent.selectOptions(screen.getByLabelText('Category'), 'c1')
    fireEvent.change(screen.getByLabelText('Response target'), { target: { value: '30' } })
    fireEvent.change(screen.getByLabelText('Resolution target'), { target: { value: '120' } })
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith('/admin/sla/policies', {
      name: 'Printer fleet',
      priority: 'high',
      category_id: 'c1',
      response_target_min: 30,
      resolution_target_min: 120,
    })
  })

  it('edit pre-fills the fields and sends the changed policy', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))

    const name = screen.getByLabelText('Policy name') as HTMLInputElement
    expect(name.value).toBe('Critical outage')
    expect((screen.getByLabelText('Priority') as HTMLSelectElement).value).toBe('critical')
    expect((screen.getByLabelText('Category') as HTMLSelectElement).value).toBe('c1')
    expect((screen.getByLabelText('Response target') as HTMLInputElement).value).toBe('60')
    expect((screen.getByLabelText('Resolution target') as HTMLInputElement).value).toBe('240')

    await userEvent.clear(name)
    await userEvent.type(name, 'Major outage')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/sla/policies/p1', {
      name: 'Major outage',
      priority: 'critical',
      clear_priority: false,
      category_id: 'c1',
      clear_category: false,
      response_target_min: 60,
      resolution_target_min: 240,
    })
  })

  it('cancel on add closes the form without saving', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {} } as any)
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: '+ Add policy' }))
    await userEvent.type(screen.getByLabelText('Policy name'), 'Unsaved')
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(screen.queryByLabelText('Policy name')).toBeNull()
    expect(post).not.toHaveBeenCalled()
  })

  it('cancel on edit closes the form and restores the row without saving', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    expect(screen.getByLabelText('Policy name')).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(screen.queryByLabelText('Policy name')).toBeNull()
    const row = screen.getByText('Critical outage').closest('tr') as HTMLElement
    expect(within(row).getByRole('button', { name: 'Edit' })).toBeTruthy()
    expect(patch).not.toHaveBeenCalled()
  })

  it('renders the edit form outside the scrolling table, like the add form', async () => {
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    expect(screen.getByLabelText('Policy name').closest('table')).toBeNull()
    expect(screen.getByLabelText('Policy name').closest('.overflow-x-auto')).toBeNull()

    // The policy being edited stays in the list, marked, with no row actions.
    const row = screen.getByText('Critical outage').closest('tr') as HTMLElement
    expect(row.className).toContain('bg-selected')
    expect(within(row).queryByRole('button', { name: 'Edit' })).toBeNull()
    // Marked by text and aria-current, not by colour alone (#672).
    expect(row.getAttribute('aria-current')).toBe('true')
    expect(within(row).getByText('Editing')).toBeTruthy()
  })

  it('edit sends clear_priority and clear_category when set to Any priority and All categories', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    await userEvent.selectOptions(screen.getByLabelText('Priority'), '')
    await userEvent.selectOptions(screen.getByLabelText('Category'), '')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    const body = patch.mock.calls[0][1] as Record<string, unknown>
    expect(body.clear_priority).toBe(true)
    expect(body.clear_category).toBe(true)
    expect(body.priority).toBeUndefined()
    expect(body.category_id).toBeUndefined()
  })

  it('hides the Add button while a form is open', async () => {
    await renderSLA()
    expect(screen.getByRole('button', { name: '+ Add policy' })).toBeTruthy()

    await userEvent.click(screen.getByRole('button', { name: '+ Add policy' }))
    expect(screen.queryByRole('button', { name: '+ Add policy' })).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    expect(screen.queryByRole('button', { name: '+ Add policy' })).toBeNull()
  })

  it('focuses the Name field when Edit opens the form, and again when Edit is clicked on another policy', async () => {
    policies = [POLICY, POLICY_B]
    await renderSLA()
    await screen.findByText('Printer fleet')
    const rowA = screen.getByText('Critical outage').closest('tr') as HTMLElement
    await userEvent.click(within(rowA).getByRole('button', { name: 'Edit' }))

    const name = screen.getByLabelText('Policy name') as HTMLInputElement
    expect(document.activeElement).toBe(name)
    expect(name.value).toBe('Critical outage')

    // Focus moved away from the form, so the second Edit has to take it back.
    await userEvent.click(document.body)
    expect(document.activeElement).not.toBe(name)
    const rowB = screen.getByText('Printer fleet').closest('tr') as HTMLElement
    await userEvent.click(within(rowB).getByRole('button', { name: 'Edit' }))

    const nameB = screen.getByLabelText('Policy name') as HTMLInputElement
    expect(document.activeElement).toBe(nameB)
    expect(nameB.value).toBe('Printer fleet')
  })

  it('focuses the Name field when + Add policy opens the form', async () => {
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: '+ Add policy' }))
    expect(document.activeElement).toBe(screen.getByLabelText('Policy name'))
  })

  it('does not take focus back while the user types in the open form', async () => {
    await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    const response = screen.getByLabelText('Response target')
    await userEvent.click(response)
    await userEvent.type(response, '5')
    expect(document.activeElement).toBe(response)
  })

  it('hides the empty-state text while a form is open and shows it after Cancel', async () => {
    const queryClient = await renderSLA()
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))

    // The policy being edited disappears from the list on a refetch.
    policies = []
    await queryClient.invalidateQueries({ queryKey: ['sla-policies'] })
    await waitFor(() => expect(screen.queryByText('Critical outage')).toBeNull())

    expect(screen.getByLabelText('Policy name')).toBeTruthy()
    expect(screen.queryByText('No SLA policies defined.')).toBeNull()

    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.getByText('No SLA policies defined.')).toBeTruthy()
  })

  it('switching Edit from one policy to another re-points the shared form at the second', async () => {
    policies = [POLICY, POLICY_B]
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
    await renderSLA()
    await screen.findByText('Printer fleet')
    const rowA = () => screen.getByText('Critical outage').closest('tr') as HTMLElement
    const rowB = () => screen.getByText('Printer fleet').closest('tr') as HTMLElement

    await userEvent.click(within(rowA()).getByRole('button', { name: 'Edit' }))
    expect((screen.getByLabelText('Policy name') as HTMLInputElement).value).toBe('Critical outage')
    await userEvent.click(within(rowB()).getByRole('button', { name: 'Edit' }))

    // One form, showing B and nothing of A.
    expect(screen.getAllByLabelText('Policy name')).toHaveLength(1)
    expect((screen.getByLabelText('Policy name') as HTMLInputElement).value).toBe('Printer fleet')
    expect((screen.getByLabelText('Priority') as HTMLSelectElement).value).toBe('low')
    expect((screen.getByLabelText('Category') as HTMLSelectElement).value).toBe('c2')
    expect((screen.getByLabelText('Response target') as HTMLInputElement).value).toBe('15')
    expect((screen.getByLabelText('Resolution target') as HTMLInputElement).value).toBe('90')

    // A is back to a plain row, B is the marked one without actions.
    expect(within(rowA()).getByRole('button', { name: 'Edit' })).toBeTruthy()
    expect(within(rowA()).getByRole('button', { name: 'Delete' })).toBeTruthy()
    expect(rowA().className).not.toContain('bg-selected')
    expect(within(rowB()).queryByRole('button', { name: 'Edit' })).toBeNull()
    expect(within(rowB()).queryByRole('button', { name: 'Delete' })).toBeNull()

    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/sla/policies/p2', {
      name: 'Printer fleet',
      priority: 'low',
      clear_priority: false,
      category_id: 'c2',
      clear_category: false,
      response_target_min: 15,
      resolution_target_min: 90,
    })
  })

  describe('asynchronous saves (#672)', () => {
    const typedEditBody = (name: string) => ({
      name,
      priority: 'critical',
      clear_priority: false,
      category_id: 'c1',
      clear_category: false,
      response_target_min: 60,
      resolution_target_min: 240,
    })

    // A failure the way the API client sees one: an axios error carrying the
    // server's { error: { code, message } } body.
    const apiError = (message: string) =>
      new AxiosError('Request failed', 'ERR_BAD_REQUEST', undefined, undefined, {
        status: 409, statusText: 'Conflict', headers: {}, config: {} as never,
        data: { error: { code: 'conflict', message } },
      })

    async function addPolicyNamed(name: string) {
      await userEvent.click(screen.getByRole('button', { name: '+ Add policy' }))
      await userEvent.type(screen.getByLabelText('Policy name'), name)
      await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    }

    it('Save on add closes the form at once and shows a Saving row without Edit or Delete', async () => {
      const d = deferred()
      vi.spyOn(api, 'post').mockReturnValue(d.promise as never)
      await renderSLA()
      await addPolicyNamed('Brand new')

      expect(screen.queryByLabelText('Policy name')).toBeNull()
      const row = await waitFor(() => rowOf('Brand new'))
      expect(within(row).getByText('Saving…')).toBeTruthy()
      expect(row.getAttribute('aria-current')).toBe('true')
      expect(within(row).queryByRole('button', { name: 'Edit' })).toBeNull()
      expect(within(row).queryByRole('button', { name: 'Delete' })).toBeNull()
      // The form is gone, so the Add button is back.
      expect(screen.getByRole('button', { name: '+ Add policy' })).toBeTruthy()

      // The server answers and the list refetches with the real row.
      policies = [POLICY, { ...POLICY_B, name: 'Brand new' }]
      d.resolve({ data: {} })
      await waitFor(() => expect(within(rowOf('Brand new')).queryByText('Saving…')).toBeNull())
      expect(within(rowOf('Brand new')).getByRole('button', { name: 'Edit' })).toBeTruthy()
      expect(rowOf('Brand new').getAttribute('aria-current')).toBeNull()
    })

    it('Save on edit closes the form at once; the row shows the new values marked Saving', async () => {
      const d = deferred()
      const patch = vi.spyOn(api, 'patch').mockReturnValue(d.promise as never)
      await renderSLA()
      await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
      const name = screen.getByLabelText('Policy name')
      await userEvent.clear(name)
      await userEvent.type(name, 'Major outage')
      fireEvent.change(screen.getByLabelText('Response target'), { target: { value: '30' } })
      await userEvent.click(screen.getByRole('button', { name: 'Save' }))

      expect(screen.queryByLabelText('Policy name')).toBeNull()
      await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
      const row = await waitFor(() => rowOf('Major outage'))
      expect(screen.queryByText('Critical outage')).toBeNull()
      expect(within(row).getByText('Saving…')).toBeTruthy()
      expect(within(row).queryByText('Editing')).toBeNull()
      expect(row.getAttribute('aria-current')).toBe('true')
      expect(within(row).getByText('30m')).toBeTruthy()

      policies = [{ ...POLICY, name: 'Major outage', response_target_min: 30 }]
      d.resolve({ data: {} })
      await waitFor(() => expect(within(rowOf('Major outage')).queryByText('Saving…')).toBeNull())
      expect(within(rowOf('Major outage')).getByRole('button', { name: 'Edit' })).toBeTruthy()
    })

    it('editing while an add is pending keeps the edit form intact when the add resolves', async () => {
      const d = deferred()
      vi.spyOn(api, 'post').mockReturnValue(d.promise as never)
      const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as never)
      await renderSLA()
      await addPolicyNamed('Brand new')
      await userEvent.click(within(rowOf('Critical outage')).getByRole('button', { name: 'Edit' }))
      const name = screen.getByLabelText('Policy name') as HTMLInputElement
      await userEvent.clear(name)
      await userEvent.type(name, 'Edited meanwhile')

      policies = [POLICY, { ...POLICY_B, name: 'Brand new' }]
      d.resolve({ data: {} })
      await waitFor(() => expect(within(rowOf('Brand new')).queryByText('Saving…')).toBeNull())

      // The add finishing did not blank or close the edit form.
      expect((screen.getByLabelText('Policy name') as HTMLInputElement).value).toBe('Edited meanwhile')
      expect((screen.getByLabelText('Priority') as HTMLSelectElement).value).toBe('critical')
      expect((screen.getByLabelText('Response target') as HTMLInputElement).value).toBe('60')
      await userEvent.click(screen.getByRole('button', { name: 'Save' }))
      await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
      expect(patch).toHaveBeenCalledWith('/admin/sla/policies/p1', typedEditBody('Edited meanwhile'))
    })

    it('editing policy B while an update of A is pending keeps B open when A resolves', async () => {
      policies = [POLICY, POLICY_B]
      const dA = deferred()
      const patch = vi.spyOn(api, 'patch')
        .mockReturnValueOnce(dA.promise as never)
        .mockResolvedValue({ data: {} } as never)
      await renderSLA()
      await screen.findByText('Printer fleet')
      await userEvent.click(within(rowOf('Critical outage')).getByRole('button', { name: 'Edit' }))
      await userEvent.click(screen.getByRole('button', { name: 'Save' }))
      await userEvent.click(within(rowOf('Printer fleet')).getByRole('button', { name: 'Edit' }))
      await userEvent.type(screen.getByLabelText('Policy name'), ' 2')

      dA.resolve({ data: {} })
      await waitFor(() => expect(within(rowOf('Critical outage')).queryByText('Saving…')).toBeNull())

      expect((screen.getByLabelText('Policy name') as HTMLInputElement).value).toBe('Printer fleet 2')
      // B's Save was never held up by A's request.
      await userEvent.click(screen.getByRole('button', { name: 'Save' }))
      await waitFor(() => expect(patch).toHaveBeenCalledTimes(2))
      expect(patch.mock.calls[1][0]).toBe('/admin/sla/policies/p2')
      expect((patch.mock.calls[1][1] as { name: string }).name).toBe('Printer fleet 2')
    })

    it('a failed edit rolls the row back and shows the named error above the list', async () => {
      const d = deferred()
      vi.spyOn(api, 'patch').mockReturnValue(d.promise as never)
      await renderSLA()
      await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
      const name = screen.getByLabelText('Policy name')
      await userEvent.clear(name)
      await userEvent.type(name, 'Major outage')
      await userEvent.click(screen.getByRole('button', { name: 'Save' }))
      await waitFor(() => expect(within(rowOf('Major outage')).getByText('Saving…')).toBeTruthy())

      d.reject(apiError('name already taken'))
      const alert = await screen.findByRole('alert')
      expect(alert.textContent).toContain("Saving 'Major outage' failed: name already taken")

      // Previous values are back, with no marker and with row actions.
      expect(screen.queryByText('Major outage', { selector: 'td' })).toBeNull()
      const row = rowOf('Critical outage')
      expect(within(row).queryByText('Saving…')).toBeNull()
      expect(within(row).getByRole('button', { name: 'Edit' })).toBeTruthy()
      // The error sits above the list.
      const table = screen.getByRole('table')
      expect(alert.compareDocumentPosition(table) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    })

    it('Reopen restores the typed values and the policy being edited; dismiss removes the error', async () => {
      const patch = vi.spyOn(api, 'patch').mockRejectedValue(apiError('boom'))
      await renderSLA()
      await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
      const name = screen.getByLabelText('Policy name')
      await userEvent.clear(name)
      await userEvent.type(name, 'Major outage')
      await userEvent.selectOptions(screen.getByLabelText('Priority'), '')
      await userEvent.click(screen.getByRole('button', { name: 'Save' }))
      await screen.findByRole('alert')

      await userEvent.click(screen.getByRole('button', { name: "Reopen 'Major outage'" }))
      expect(screen.queryByRole('alert')).toBeNull()
      expect((screen.getByLabelText('Policy name') as HTMLInputElement).value).toBe('Major outage')
      expect((screen.getByLabelText('Priority') as HTMLSelectElement).value).toBe('')
      expect(within(rowOf('Critical outage')).getByText('Editing')).toBeTruthy()

      // Save sends it to the same policy again.
      await userEvent.click(screen.getByRole('button', { name: 'Save' }))
      await waitFor(() => expect(patch).toHaveBeenCalledTimes(2))
      expect(patch.mock.calls[1][0]).toBe('/admin/sla/policies/p1')
      expect((patch.mock.calls[1][1] as { name: string }).name).toBe('Major outage')
      await screen.findByRole('alert')

      await userEvent.click(screen.getByRole('button', { name: "Dismiss 'Major outage'" }))
      expect(screen.queryByRole('alert')).toBeNull()
      expect(screen.queryByLabelText('Policy name')).toBeNull()
    })

    it('a failed add removes the temporary row, and Reopen restores the add form', async () => {
      vi.spyOn(api, 'post').mockRejectedValue(apiError('nope'))
      await renderSLA()
      await userEvent.click(screen.getByRole('button', { name: '+ Add policy' }))
      await userEvent.type(screen.getByLabelText('Policy name'), 'Brand new')
      await userEvent.selectOptions(screen.getByLabelText('Priority'), 'high')
      fireEvent.change(screen.getByLabelText('Response target'), { target: { value: '30' } })
      await userEvent.click(screen.getByRole('button', { name: 'Save' }))

      const alert = await screen.findByRole('alert')
      expect(alert.textContent).toContain("Saving 'Brand new' failed: nope")
      expect(screen.queryByText('Brand new', { selector: 'td' })).toBeNull()

      await userEvent.click(screen.getByRole('button', { name: "Reopen 'Brand new'" }))
      expect((screen.getByLabelText('Policy name') as HTMLInputElement).value).toBe('Brand new')
      expect((screen.getByLabelText('Priority') as HTMLSelectElement).value).toBe('high')
      expect((screen.getByLabelText('Response target') as HTMLInputElement).value).toBe('30')
      expect(screen.queryByRole('button', { name: '+ Add policy' })).toBeNull()
    })

    it('a failure landing while another form is open leaves that form alone; Reopen replaces it', async () => {
      policies = [POLICY, POLICY_B]
      const d = deferred()
      vi.spyOn(api, 'patch').mockReturnValue(d.promise as never)
      await renderSLA()
      await screen.findByText('Printer fleet')
      await userEvent.click(within(rowOf('Critical outage')).getByRole('button', { name: 'Edit' }))
      await userEvent.click(screen.getByRole('button', { name: 'Save' }))
      await userEvent.click(within(rowOf('Printer fleet')).getByRole('button', { name: 'Edit' }))
      await userEvent.type(screen.getByLabelText('Policy name'), ' typing')

      d.reject(apiError('late failure'))
      await screen.findByText(/late failure/)
      expect((screen.getByLabelText('Policy name') as HTMLInputElement).value).toBe('Printer fleet typing')

      await userEvent.click(screen.getByRole('button', { name: "Reopen 'Critical outage'" }))
      expect((screen.getByLabelText('Policy name') as HTMLInputElement).value).toBe('Critical outage')
      expect(screen.getAllByLabelText('Policy name')).toHaveLength(1)
    })

    it('names an add that failed with an empty name as an unnamed policy, and the buttons say which', async () => {
      vi.spyOn(api, 'post').mockRejectedValue(apiError('name required'))
      await renderSLA()
      await userEvent.click(screen.getByRole('button', { name: '+ Add policy' }))
      await userEvent.click(screen.getByRole('button', { name: 'Save' }))

      const alert = await screen.findByRole('alert')
      expect(alert.textContent).toContain("Saving 'unnamed policy' failed: name required")
      expect(screen.getByRole('button', { name: "Reopen 'unnamed policy'" })).toBeTruthy()
      expect(screen.getByRole('button', { name: "Dismiss 'unnamed policy'" })).toBeTruthy()
    })

    it('lists several failures at once', async () => {
      vi.spyOn(api, 'post').mockRejectedValue(apiError('no'))
      await renderSLA()
      await addPolicyNamed('First')
      await screen.findByText(/Saving 'First' failed/)
      await addPolicyNamed('Second')
      await screen.findByText(/Saving 'Second' failed/)
      expect(screen.getAllByRole('button', { name: /^Reopen/ })).toHaveLength(2)
    })

    it('Delete is not offered on an unsaved add row, and still works on a saved one', async () => {
      const dAdd = deferred()
      vi.spyOn(api, 'post').mockReturnValue(dAdd.promise as never)
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const del = vi.spyOn(api, 'delete').mockResolvedValue({ data: {} } as any)
      const qc = await renderSLA()
      await addPolicyNamed('Brand new')
      expect(within(rowOf('Brand new')).queryByRole('button', { name: 'Delete' })).toBeNull()

      await userEvent.click(within(rowOf('Critical outage')).getByRole('button', { name: 'Delete' }))
      await userEvent.click(await screen.findByRole('button', { name: 'Delete policy' }))
      await waitFor(() => expect(del).toHaveBeenCalledWith('/admin/sla/policies/p1'))
      dAdd.resolve({ data: {} })
      await savesAnswered(qc)
    })
  })

  // The async-save state lives in react-query's mutation cache, not in the
  // section, so one save settling cannot disturb another and nothing is lost
  // when the section unmounts (leaving the page).
  describe('saves in flight are independent of each other and of the section (#672)', () => {
    const apiError = (message: string) =>
      new AxiosError('Request failed', 'ERR_BAD_REQUEST', undefined, undefined, {
        status: 409, statusText: 'Conflict', headers: {}, config: {} as never,
        data: { error: { code: 'conflict', message } },
      })

    async function addPolicyNamed(name: string) {
      await userEvent.click(screen.getByRole('button', { name: '+ Add policy' }))
      await userEvent.type(screen.getByLabelText('Policy name'), name)
      await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    }

    async function editRowTo(rowName: string, newName: string) {
      await userEvent.click(within(rowOf(rowName)).getByRole('button', { name: 'Edit' }))
      const name = screen.getByLabelText('Policy name')
      await userEvent.clear(name)
      await userEvent.type(name, newName)
      await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    }

    // Leaving the Features page unmounts the SLA section; coming back mounts a new one.
    async function leaveAndReturn() {
      rerenderPage(<GeneralSettingsPage />)
      await waitFor(() => expect(screen.queryByRole('button', { name: '+ Add policy' })).toBeNull())
      rerenderPage(<FeaturesSettingsPage />)
      await screen.findByRole('button', { name: '+ Add policy' })
    }

    it('another save settling does not remove an add that is still pending', async () => {
      policies = [POLICY, POLICY_B]
      const dAdd = deferred()
      vi.spyOn(api, 'post').mockReturnValue(dAdd.promise as never)
      vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as never)
      const qc = await renderSLA()
      await screen.findByText('Printer fleet')
      await addPolicyNamed('Brand new')
      await waitFor(() => expect(within(rowOf('Brand new')).getByText('Saving…')).toBeTruthy())

      policies = [POLICY, { ...POLICY_B, name: 'Printer fleet 2' }]
      await editRowTo('Printer fleet', 'Printer fleet 2')
      await waitFor(() => expect(within(rowOf('Printer fleet 2')).queryByText('Saving…')).toBeNull())

      // The edit settled and the list refetched; the add is still waiting.
      expect(within(rowOf('Brand new')).getByText('Saving…')).toBeTruthy()
      dAdd.resolve({ data: {} })
      await savesAnswered(qc)
    })

    it('a save that succeeds leaves the mutation cache; pending and failed saves stay', async () => {
      policies = [POLICY, POLICY_B]
      const dPending = deferred()
      vi.spyOn(api, 'post')
        .mockReturnValueOnce(dPending.promise as never)
        .mockRejectedValueOnce(apiError('nope'))
      vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as never)
      const qc = await renderSLA()
      await screen.findByText('Printer fleet')
      await addPolicyNamed('Still pending')
      await addPolicyNamed('Will fail')
      await screen.findByText(/Saving 'Will fail' failed/)

      policies = [POLICY, { ...POLICY_B, name: 'Printer fleet 2' }]
      await editRowTo('Printer fleet', 'Printer fleet 2')
      await waitFor(() => expect(within(rowOf('Printer fleet 2')).queryByText('Saving…')).toBeNull())

      expect(saveStatuses(qc)).toEqual(['error', 'pending'])
      expect(within(rowOf('Still pending')).getByText('Saving…')).toBeTruthy()
      expect(screen.getByText(/Saving 'Will fail' failed/)).toBeTruthy()

      dPending.resolve({ data: {} })
      await waitFor(() => expect(saveStatuses(qc)).toEqual(['error']))
    })

    it('two adds in flight keep both Saving rows until each is answered', async () => {
      const d1 = deferred()
      const d2 = deferred()
      vi.spyOn(api, 'post')
        .mockReturnValueOnce(d1.promise as never)
        .mockReturnValueOnce(d2.promise as never)
      const qc = await renderSLA()
      await addPolicyNamed('One')
      await addPolicyNamed('Two')
      await waitFor(() => expect(within(rowOf('One')).getByText('Saving…')).toBeTruthy())
      await waitFor(() => expect(within(rowOf('Two')).getByText('Saving…')).toBeTruthy())

      policies = [POLICY, { ...POLICY_B, name: 'One' }]
      d1.resolve({ data: {} })
      await waitFor(() => expect(within(rowOf('One')).queryByText('Saving…')).toBeNull())
      expect(within(rowOf('Two')).getByText('Saving…')).toBeTruthy()

      policies = [POLICY, { ...POLICY_B, name: 'One' }, { ...POLICY_B, id: 'p3', name: 'Two' }]
      d2.resolve({ data: {} })
      await waitFor(() => expect(within(rowOf('Two')).queryByText('Saving…')).toBeNull())
      expect(within(rowOf('One')).getByRole('button', { name: 'Edit' })).toBeTruthy()
      await savesAnswered(qc)
    })

    it('an edit pending on one policy survives another policy’s save settling', async () => {
      policies = [POLICY, POLICY_B]
      const dA = deferred()
      vi.spyOn(api, 'patch')
        .mockReturnValueOnce(dA.promise as never)
        .mockResolvedValue({ data: {} } as never)
      const qc = await renderSLA()
      await screen.findByText('Printer fleet')
      await editRowTo('Critical outage', 'Major outage')
      policies = [POLICY, { ...POLICY_B, name: 'Printer fleet 2' }]
      await editRowTo('Printer fleet', 'Printer fleet 2')
      await waitFor(() => expect(within(rowOf('Printer fleet 2')).queryByText('Saving…')).toBeNull())

      // A's request is still open: its new values and marker are still shown.
      expect(within(rowOf('Major outage')).getByText('Saving…')).toBeTruthy()
      dA.resolve({ data: {} })
      await savesAnswered(qc)
    })

    it('two saves of one policy: the row shows the later values throughout, and each failure is named', async () => {
      const d1 = deferred()
      const d2 = deferred()
      vi.spyOn(api, 'patch')
        .mockReturnValueOnce(d1.promise as never)
        .mockReturnValueOnce(d2.promise as never)
      await renderSLA()
      await editRowTo('Critical outage', 'First try')
      await editRowTo('First try', 'Second try')

      await waitFor(() => expect(within(rowOf('Second try')).getByText('Saving…')).toBeTruthy())
      await waitFor(() => expect(screen.queryByText('First try')).toBeNull())

      // The first fails while the second is still open: the row keeps the
      // second's values, not the server's and not the first's.
      d1.reject(apiError('first no'))
      await screen.findByText(/Saving 'First try' failed: first no/)
      expect(within(rowOf('Second try')).getByText('Saving…')).toBeTruthy()
      expect(screen.queryByText('Critical outage', { selector: 'td' })).toBeNull()

      // The second then fails: the row shows what the server has, and both failures are listed.
      d2.reject(apiError('second no'))
      await screen.findByText(/Saving 'Second try' failed: second no/)
      expect(screen.getByText(/Saving 'First try' failed: first no/)).toBeTruthy()
      const row = rowOf('Critical outage')
      expect(within(row).queryByText('Saving…')).toBeNull()
      expect(screen.queryByText('Second try', { selector: 'td' })).toBeNull()
    })

    it('a save still pending when the section is left is marked Saving on return', async () => {
      const d = deferred()
      vi.spyOn(api, 'post').mockReturnValue(d.promise as never)
      const qc = await renderSLA()
      await addPolicyNamed('Brand new')
      await leaveAndReturn()

      const row = await waitFor(() => rowOf('Brand new'))
      expect(within(row).getByText('Saving…')).toBeTruthy()
      expect(row.getAttribute('aria-current')).toBe('true')
      expect(within(row).queryByRole('button', { name: 'Edit' })).toBeNull()
      d.resolve({ data: {} })
      await savesAnswered(qc)
    })

    it('a save that fails while the section is unmounted shows its named error with Reopen on return', async () => {
      const d = deferred()
      vi.spyOn(api, 'post').mockReturnValue(d.promise as never)
      const qc = await renderSLA()
      await addPolicyNamed('Brand new')
      rerenderPage(<GeneralSettingsPage />)
      await waitFor(() => expect(screen.queryByRole('button', { name: '+ Add policy' })).toBeNull())

      d.reject(apiError('nope'))
      await waitFor(() => expect(saveStatuses(qc)).toEqual(['error']))
      rerenderPage(<FeaturesSettingsPage />)

      const alert = await screen.findByRole('alert')
      expect(alert.textContent).toContain("Saving 'Brand new' failed: nope")
      expect(screen.queryByText('Brand new', { selector: 'td' })).toBeNull()
      await userEvent.click(screen.getByRole('button', { name: /^Reopen/ }))
      expect((screen.getByLabelText('Policy name') as HTMLInputElement).value).toBe('Brand new')
    })

    it('a save that fails after the section was left and re-entered is still reported', async () => {
      const d = deferred()
      vi.spyOn(api, 'patch').mockReturnValue(d.promise as never)
      await renderSLA()
      await editRowTo('Critical outage', 'Major outage')
      await leaveAndReturn()
      await waitFor(() => expect(within(rowOf('Major outage')).getByText('Saving…')).toBeTruthy())

      d.reject(apiError('boom'))
      const alert = await screen.findByRole('alert')
      expect(alert.textContent).toContain("Saving 'Major outage' failed: boom")
      expect(within(rowOf('Critical outage')).queryByText('Saving…')).toBeNull()
    })

    it('temporary rows stay distinct across a remount, so a failure removes only its own row', async () => {
      const d1 = deferred()
      const d2 = deferred()
      vi.spyOn(api, 'post')
        .mockReturnValueOnce(d1.promise as never)
        .mockReturnValueOnce(d2.promise as never)
      const qc = await renderSLA()
      await addPolicyNamed('First')
      await leaveAndReturn()
      await addPolicyNamed('Second')
      await waitFor(() => expect(within(rowOf('First')).getByText('Saving…')).toBeTruthy())
      await waitFor(() => expect(within(rowOf('Second')).getByText('Saving…')).toBeTruthy())

      d1.reject(apiError('no'))
      await screen.findByText(/Saving 'First' failed/)
      expect(screen.queryByText('First', { selector: 'td' })).toBeNull()
      expect(within(rowOf('Second')).getByText('Saving…')).toBeTruthy()
      d2.resolve({ data: {} })
      await savesAnswered(qc)
    })
  })
})
