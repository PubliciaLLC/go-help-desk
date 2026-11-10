import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { AdminAuditEntry, AdminAuditListResponse } from '@/api/types'

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/audit' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { AdminAuditPage } from './AdminAuditPage'

function asAdmin() {
  useAuthStore.setState({
    user: {
      id: 'admin-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin',
      mfa_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

function entry(overrides: Partial<AdminAuditEntry> = {}): AdminAuditEntry {
  return {
    id: 'e-1', entity_type: 'ticket', entity_id: 't-1', action: 'resolved',
    actor_id: 'u-1', actor_name: 'Sam Staff', created_at: '2026-01-01T12:00:00Z',
    ...overrides,
  }
}

// total_capped is always sent by the server; most tests do not care about it.
type ListFixture = Omit<AdminAuditListResponse, 'total_capped'> & { total_capped?: boolean }

function mockList({ total_capped = false, ...rest }: ListFixture) {
  const response: AdminAuditListResponse = { ...rest, total_capped }
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/audit') return Promise.resolve({ data: response })
    if (url === '/staff') return Promise.resolve({ data: [] })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

beforeEach(() => {
  asAdmin()
})

describe('AdminAuditPage', () => {
  it('renders what the server sends', async () => {
    mockList({ entries: [entry()], total: 1, has_more: false })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('Resolved')).toBeTruthy())
    const row = screen.getByText('Resolved').closest('tr')!
    expect(within(row).getByText('ticket')).toBeTruthy()
    expect(within(row).getByText('Sam Staff')).toBeTruthy()
  })

  it('shows an entity-type filter for admin', async () => {
    mockList({ entries: [], total: 0, has_more: false })
    renderWithQuery(<AdminAuditPage />)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Audit Log' })).toBeTruthy())

    expect(screen.getByLabelText(/entity type/i)).toBeTruthy()
  })

  it('sends an edited action filter to the server', async () => {
    const get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
      if (url === '/admin/audit') return Promise.resolve({ data: { entries: [], total: 0, total_capped: false, has_more: false } })
      return Promise.resolve({ data: [] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    renderWithQuery(<AdminAuditPage />)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Audit Log' })).toBeTruthy())

    await userEvent.type(screen.getByLabelText(/^action$/i), 'resolved')

    await waitFor(() => {
      const call = get.mock.calls.find((c) => {
        const params = (c[1] as { params?: Record<string, unknown> } | undefined)?.params
        return c[0] === '/admin/audit' && params?.action === 'resolved'
      })
      expect(call, 'expected a request with action=resolved').toBeTruthy()
    })
  })

  it('shows the diff inline when the server sends one', async () => {
    mockList({
      entries: [entry({ before: { status_id: 'a' }, after: { status_id: 'b' } })],
      total: 1,
      has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('Resolved')).toBeTruthy())
    expect(screen.getByText(/status_id/)).toBeTruthy()
    expect(screen.getByText(/a → b/)).toBeTruthy()
  })

  it('shows names in the diff and keeps the id as a tooltip (#422)', async () => {
    mockList({
      entries: [entry({
        before: { status_id: 'id-new' }, after: { status_id: 'id-prog' },
        names: { 'id-new': { name: 'New' }, 'id-prog': { name: 'In Progress' } },
      })],
      total: 1,
      has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('In Progress')).toBeTruthy())
    expect(screen.getByText('New').getAttribute('title')).toBe('id-new')
  })

  it('shows an empty state rather than a blank table', async () => {
    mockList({ entries: [], total: 0, has_more: false })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText(/nothing matches/i)).toBeTruthy())
  })

  it('disables Previous on the first page and Next when there is nothing more', async () => {
    mockList({ entries: [entry()], total: 1, has_more: false })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByRole('button', { name: /previous/i })).toBeTruthy())
    expect((screen.getByRole('button', { name: /previous/i }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: /next/i }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('enables Next from has_more, not from the count', async () => {
    mockList({ entries: [entry()], total: 2, has_more: true })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByRole('button', { name: /next/i })).toBeTruthy())
    expect((screen.getByRole('button', { name: /next/i }) as HTMLButtonElement).disabled).toBe(false)
  })

  // The pager runs on has_more. The count is for display, and a page that has
  // already said it is the last must not offer a Next off the back of it.
  it('does not enable Next from a count when has_more is false', async () => {
    mockList({ entries: [entry()], total: 999, has_more: false })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByRole('button', { name: /next/i })).toBeTruthy())
    expect((screen.getByRole('button', { name: /next/i }) as HTMLButtonElement).disabled).toBe(true)
  })

  // #331: the server stops counting at 10,000 and says so. A capped total is a
  // floor, not a count, so it reads "10,000+"; an exact one never gets the plus,
  // including one that happens to equal the cap.
  it('shows a capped total as n+', async () => {
    mockList({ entries: [entry()], total: 10000, total_capped: true, has_more: true })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('1–1 of 10,000+')).toBeTruthy())
  })

  it('shows an uncapped total of exactly the cap without a plus', async () => {
    mockList({ entries: [entry()], total: 10000, total_capped: false, has_more: false })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('1–1 of 10,000')).toBeTruthy())
  })

  it('keeps paging on has_more when the total is capped', async () => {
    const seen: number[] = []
    vi.spyOn(api, 'get').mockImplementation(((url: string, config?: { params?: { offset?: number } }) => {
      if (url !== '/admin/audit') return Promise.resolve({ data: [] })
      const offset = config?.params?.offset ?? 0
      seen.push(offset)
      return Promise.resolve({
        data: { entries: [entry({ id: `e-${offset}` })], total: 10000, total_capped: true, has_more: true },
      })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    renderWithQuery(<AdminAuditPage />)

    const next = () => screen.getByRole('button', { name: /next/i }) as HTMLButtonElement
    await waitFor(() => expect(screen.getByText('1–1 of 10,000+')).toBeTruthy())
    await userEvent.click(next())
    await waitFor(() => expect(screen.getByText('51–51 of 10,000+')).toBeTruthy())
    expect(seen).toContain(50)
    expect(next().disabled).toBe(false)
    expect((screen.getByRole('button', { name: /previous/i }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('does not offer Next on the last page of a capped total', async () => {
    mockList({ entries: [entry()], total: 10000, total_capped: true, has_more: false })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('1–1 of 10,000+')).toBeTruthy())
    expect((screen.getByRole('button', { name: /next/i }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('shows a masked requester distinctly from an account named "Requester"', async () => {
    mockList({
      entries: [
        entry({ id: 'm', action: 'created', actor_name: 'Requester', actor_masked: true }),
        entry({ id: 'r', action: 'resolved', actor_name: 'Requester' }),
      ],
      total: 2,
      has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('Created')).toBeTruthy())
    const masked = screen.getByText('Created').closest('tr')!
    expect(within(masked).getByText('Requester (name hidden)')).toBeTruthy()

    const named = screen.getByText('Resolved').closest('tr')!
    expect(within(named).getByText('Requester')).toBeTruthy()
    expect(within(named).queryByText(/name hidden/)).toBeNull()
  })

  // #382: the label is text a display name can copy, so the marker is an icon,
  // and there is no hover-only tooltip a keyboard or touch user cannot reach.
  it('marks a masked requester with an icon a display name cannot copy', async () => {
    mockList({
      entries: [
        entry({ id: 'm', action: 'created', actor_name: 'Requester', actor_masked: true }),
        entry({ id: 'r', action: 'resolved', actor_name: 'Requester (name hidden)' }),
      ],
      total: 2,
      has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('Created')).toBeTruthy())
    const masked = within(screen.getByText('Created').closest('tr')!).getByText('Requester (name hidden)')
    expect(masked.querySelector('svg')).not.toBeNull()
    // Decorative: the words already say it, and a screen reader should not
    // hear the icon announce itself as well.
    expect(masked.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true')
    expect(masked.getAttribute('title')).toBeNull()

    const named = within(screen.getByText('Resolved').closest('tr')!).getByText('Requester (name hidden)')
    expect(named.querySelector('svg')).toBeNull()
  })

  // #158: the revert dialog says the edited text "stays in the audit log". A
  // revert has only a before side, which AuditDiff does not render, so the page
  // shows it for this entity type.
  describe('an email template revert', () => {
    const revert = (o: Partial<AdminAuditEntry> = {}) =>
      entry({
        entity_type: 'email_template', action: 'email_template_reverted',
        before: { template_name: 'ticket_created', template_subject: 'Old subject', template_body: 'Old body {{.TicketURL}}' },
        after: null, ...o,
      })

    it('shows the removed subject and body', async () => {
      mockList({ entries: [revert()], total: 1, has_more: false })
      renderWithQuery(<AdminAuditPage />)
      const row = (await screen.findByText('email_template', { selector: 'span' })).closest('tr')!
      expect(row.textContent).toContain('Old subject')
      expect(row.textContent).toContain('Old body {{.TicketURL}}')
      expect(row.textContent).toMatch(/removed/i)
    })

    it('shows the removed HTML part in a second block, and no block when there was none', async () => {
      mockList({
        entries: [revert({ before: { template_name: 'ticket_created', template_subject: 's', template_body: 'Plain body', template_html: '<p>Old html {{.TicketURL}}</p>' } })],
        total: 1, has_more: false,
      })
      const { unmount } = renderWithQuery(<AdminAuditPage />)
      const row = (await screen.findByText('email_template', { selector: 'span' })).closest('tr')!
      const blocks = row.querySelectorAll('pre')
      expect(blocks).toHaveLength(2)
      expect(blocks[0].textContent).toBe('Plain body')
      expect(blocks[1].textContent).toBe('<p>Old html {{.TicketURL}}</p>')
      // Shown as text: nothing of it became an element.
      expect(row.querySelector('pre p')).toBeNull()
      unmount()

      mockList({ entries: [revert({ before: { template_name: 'ticket_created', template_subject: 's', template_body: 'Plain body', template_html: '' } })], total: 1, has_more: false })
      renderWithQuery(<AdminAuditPage />)
      const row2 = (await screen.findByText('email_template', { selector: 'span' })).closest('tr')!
      expect(row2.querySelectorAll('pre')).toHaveLength(1)
    })

    it('leaves every other before-only entry exactly as it was', async () => {
      mockList({
        entries: [entry({ entity_type: 'user', action: 'deleted', before: { id: 'x', template_body: 'not shown' }, after: null })],
        total: 1, has_more: false,
      })
      renderWithQuery(<AdminAuditPage />)
      const row = (await screen.findByText('user', { selector: 'span' })).closest('tr')!
      expect(row.textContent).not.toContain('not shown')
      expect(row.textContent).not.toMatch(/removed/i)
    })

    it('does not add a second copy to a save, which AuditDiff already shows', async () => {
      mockList({
        entries: [revert({ action: 'email_template_saved', after: { template_name: 'ticket_created', template_subject: 'New', template_body: 'New body' } })],
        total: 1, has_more: false,
      })
      renderWithQuery(<AdminAuditPage />)
      const row = (await screen.findByText('email_template', { selector: 'span' })).closest('tr')!
      expect(row.textContent).not.toMatch(/removed/i)
      expect(row.textContent).toContain('New body')
    })
  })
})

describe('custom field entries', () => {
  const field = (overrides: Partial<AdminAuditEntry>) =>
    entry({ entity_type: 'custom_field', entity_id: 'f-1', ...overrides })

  it('offers custom_field in the entity-type filter', async () => {
    mockList({ entries: [], total: 0, has_more: false })
    renderWithQuery(<AdminAuditPage />)
    const select = await screen.findByLabelText(/entity type/i)
    expect(within(select).getByRole('option', { name: 'custom_field' })).toBeTruthy()
  })

  it('names the field on a create entry, which has no diff', async () => {
    mockList({
      entries: [field({ action: 'custom_field_created', before: null, after: { field_name: 'Department', field_type: 'select', sort_order: 1, active: true } })],
      total: 1, has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)
    const row = (await screen.findByText('Custom field created')).closest('tr')!
    expect(within(row).getByText('Department')).toBeTruthy()
  })

  it('names the field and option on an option-deleted entry', async () => {
    mockList({
      entries: [field({ action: 'custom_field_option_deleted', before: { field_name: 'Department', option_value: 'Marketing' }, after: null })],
      total: 1, has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)
    const row = (await screen.findByText('Custom field option deleted')).closest('tr')!
    expect(row.textContent).toContain('Department · option "Marketing"')
  })

  it('names the field and place on an attached entry', async () => {
    mockList({
      entries: [field({ action: 'custom_field_attached', before: null, after: { field_name: 'Department', scope_path: 'Hardware › Laptop' } })],
      total: 1, has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)
    const row = (await screen.findByText('Custom field attached')).closest('tr')!
    expect(row.textContent).toContain('Department · Hardware › Laptop')
  })

  it('shows only the label when the entry has no allow-listed text', async () => {
    mockList({
      entries: [field({ action: 'custom_field_deactivated', before: null, after: null })],
      total: 1, has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)
    const row = (await screen.findByText('Custom field deactivated')).closest('tr')!
    expect(row.textContent).not.toContain('·')
  })

  it('adds no context line to other entity types', async () => {
    mockList({
      entries: [entry({ entity_type: 'category', action: 'updated', before: { field_name: 'x' }, after: { field_name: 'y' } })],
      total: 1, has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)
    const row = (await screen.findByText('Updated')).closest('tr')!
    expect(row.textContent).not.toContain('·')
  })
})

describe('OAuth client and webhook entries', () => {
  it('offers oauth_client and webhook in the entity-type filter', async () => {
    mockList({ entries: [], total: 0, has_more: false })
    renderWithQuery(<AdminAuditPage />)
    const select = await screen.findByLabelText(/entity type/i)
    expect(within(select).getByRole('option', { name: 'oauth_client' })).toBeTruthy()
    expect(within(select).getByRole('option', { name: 'webhook' })).toBeTruthy()
  })

  // A create has no "before" and a delete has no "after", and AuditDiff shows
  // neither, so without a line of its own the row would not say which client
  // or hook it is about.
  it('names the client and its scopes on a create entry', async () => {
    mockList({
      entries: [entry({ entity_type: 'oauth_client', action: 'oauth_client_created', before: null,
        after: { client_name: 'Nightly sync', scopes: 'tickets:read,tickets:write', service_user_id: 'u-9' } })],
      total: 1, has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)
    const row = (await screen.findByText('Oauth client created')).closest('tr')!
    expect(row.textContent).toContain('Nightly sync · tickets:read,tickets:write')
  })

  it('names the client on a delete entry, from the before side', async () => {
    mockList({
      entries: [entry({ entity_type: 'oauth_client', action: 'oauth_client_deleted',
        before: { client_name: 'Nightly sync', scopes: 'tickets:read', service_user_id: 'u-9' }, after: null })],
      total: 1, has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)
    const row = (await screen.findByText('Oauth client deleted')).closest('tr')!
    expect(row.textContent).toContain('Nightly sync')
  })

  it('names the webhook by its host and events on a delete entry', async () => {
    mockList({
      entries: [entry({ entity_type: 'webhook', action: 'webhook_deleted',
        before: { url_host: 'hooks.example.com', events: 'ticket.created', enabled: true, signed: true }, after: null })],
      total: 1, has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)
    const row = (await screen.findByText('Webhook deleted')).closest('tr')!
    expect(row.textContent).toContain('hooks.example.com · ticket.created')
  })

  it('shows what an update changed, and not the keys it left alone', async () => {
    mockList({
      entries: [entry({ entity_type: 'webhook', action: 'webhook_updated',
        before: { url_host: 'hooks.example.com', enabled: true, signed: true },
        after: { url_host: 'hooks.example.com', enabled: false, signed: true, url_changed: true } })],
      total: 1, has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)
    const row = (await screen.findByText('Webhook updated')).closest('tr')!
    expect(row.textContent).toContain('enabled: true → false')
    expect(row.textContent).toContain('url_changed: — → true')
    expect(row.textContent).not.toContain('signed:')
  })
})

describe('a failed load or refresh', () => {
  // /admin/audit answers normally until `failAudit` is set, then rejects.
  function mockFlaky() {
    const state = { failAudit: false }
    vi.spyOn(api, 'get').mockImplementation(((url: string) => {
      if (url === '/admin/audit') {
        if (state.failAudit) return Promise.reject(new Error('boom'))
        return Promise.resolve({ data: { entries: [entry()], total: 1, total_capped: false, has_more: false } })
      }
      return Promise.resolve({ data: [] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    return state
  }

  it('with nothing loaded, shows the full error', async () => {
    const state = mockFlaky()
    state.failAudit = true
    renderWithQuery(<AdminAuditPage />)

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe('Could not load the audit log.')
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('with results on screen, keeps them and says the refresh failed', async () => {
    const state = mockFlaky()
    const { queryClient } = renderWithQuery(<AdminAuditPage />)
    await screen.findByText('Resolved')

    state.failAudit = true
    await queryClient.invalidateQueries({ queryKey: ['adminAudit'] })

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe("Couldn't refresh the results. Showing what was loaded before.")
    expect(screen.getByText('Resolved')).toBeTruthy()
    expect(screen.queryByText('Could not load the audit log.')).toBeNull()
  })

  it('takes the notice down once a refresh works', async () => {
    const state = mockFlaky()
    const { queryClient } = renderWithQuery(<AdminAuditPage />)
    await screen.findByText('Resolved')
    state.failAudit = true
    await queryClient.invalidateQueries({ queryKey: ['adminAudit'] })
    expect((await screen.findByRole('alert')).textContent).toContain("Couldn't refresh the results")

    state.failAudit = false
    await queryClient.invalidateQueries({ queryKey: ['adminAudit'] })
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    expect(screen.getByText('Resolved')).toBeTruthy()
  })

  it("shows the full error when a changed filter fails, not the previous filter's results", async () => {
    const state = mockFlaky()
    renderWithQuery(<AdminAuditPage />)
    await screen.findByText('Resolved')

    // A new filter is a new query key with no data of its own.
    state.failAudit = true
    await userEvent.type(screen.getByLabelText('Action'), 'x')

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe('Could not load the audit log.')
    expect(screen.queryByText('Resolved')).toBeNull()
  })
})
