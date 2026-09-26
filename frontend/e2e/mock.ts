import type { Page } from '@playwright/test'

// The same shape the vitest component tests give api.get: a table of
// path -> response, falling back to an empty array/object rather than a
// network error for anything not named. These tests are about layout, not
// data, so the fixtures are the minimum each page needs to reach its
// authenticated, populated state.

export const ME_STAFF = {
  id: 'u-staff',
  email: 'sam.staff@example.com',
  display_name: 'Sam Staff',
  role: 'staff',
  mfa_enabled: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

export const ME_REPORTER = {
  id: 'u-reporter',
  email: 'rita.reporter@example.com',
  display_name: 'Rita Reporter',
  role: 'user',
  mfa_enabled: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

export const STATUSES = [
  { id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: '#3b82f6', active: true, ticket_count: 2 },
  { id: 'st-resolved', name: 'Resolved', kind: 'system', sort_order: 2, color: '#22c55e', active: true, ticket_count: 0 },
  { id: 'st-closed', name: 'Closed', kind: 'system', sort_order: 3, color: '#6b7280', active: true, ticket_count: 0 },
]

export const CATEGORIES = [
  { id: 'cat-1', name: 'Hardware', active: true },
  { id: 'cat-2', name: 'Software', active: true },
]

export const TICKETS = [
  {
    id: 'tk-1',
    tracking_number: 'GHD-2026-000001',
    subject: 'Printer on fire, second floor',
    description: 'Smoke coming from the tray.',
    category_id: 'cat-1',
    priority: 'critical',
    status_id: 'st-new',
    created_at: '2026-09-20T10:00:00Z',
    updated_at: '2026-09-20T10:00:00Z',
    sla: null,
  },
  {
    id: 'tk-2',
    tracking_number: 'GHD-2026-000002',
    subject: 'VPN drops every hour on the hour',
    description: 'Started after the router firmware update.',
    category_id: 'cat-2',
    priority: 'medium',
    status_id: 'st-new',
    created_at: '2026-09-21T09:00:00Z',
    updated_at: '2026-09-21T09:00:00Z',
    sla: null,
  },
]

export const TICKET_DETAIL = {
  ...TICKETS[0],
}

/**
 * Routes every /api/v1/* call the four in-scope pages make to a fixture,
 * falling back to `{ data: [] }` for anything not named — the same
 * fallback the existing vitest component-test mocks use.
 */
export async function mockApi(page: Page, opts: { me?: object } = {}) {
  const me = opts.me ?? ME_STAFF
  const byPath: Record<string, unknown> = {
    '/api/v1/me': me,
    '/api/v1/site': { name: 'Go Help Desk', logo_url: '', version: '1.3.0' },
    '/api/v1/statuses': STATUSES,
    '/api/v1/tickets': TICKETS,
    '/api/v1/categories': CATEGORIES,
    [`/api/v1/tickets/${TICKET_DETAIL.id}`]: TICKET_DETAIL,
    [`/api/v1/tickets/${TICKET_DETAIL.id}/attachments`]: [],
    [`/api/v1/tickets/${TICKET_DETAIL.id}/replies`]: [],
    [`/api/v1/tickets/${TICKET_DETAIL.id}/history`]: [],
    [`/api/v1/tickets/${TICKET_DETAIL.id}/links`]: [],
    [`/api/v1/tickets/${TICKET_DETAIL.id}/tags`]: [],
    [`/api/v1/tickets/${TICKET_DETAIL.id}/custom-fields`]: [],
    [`/api/v1/tickets/${TICKET_DETAIL.id}/canned-responses`]: [],
    '/api/v1/admin/users': [],
    '/api/v1/tickets/fields': [],
  }

  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname
    if (path in byPath) {
      await route.fulfill({ json: byPath[path] })
      return
    }
    // /categories/:id/types and /types/:id/items — no CTI fixtures needed
    // for a layout check, so every one of these is "none configured".
    await route.fulfill({ json: [] })
  })
}
