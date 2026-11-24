import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { Dashboard, Role } from '@/api/types'

// #675: the dashboard gains a queue strip, a needs-attention list and a 30-day
// trend, from one request. The status cards, View all tickets and By Client are
// what they were.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/dashboard', search: {} } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, search, params, children, ...rest }: any) => {
    const path = params ? to.replace('$id', params.id) : to
    const qs = search ? new URLSearchParams(Object.entries(search).filter(([, v]) => v !== undefined) as [string, string][]).toString() : ''
    return (
      <a href={qs ? `${path}?${qs}` : path} {...rest}>
        {children}
      </a>
    )
  },
}))

import { DashboardPage } from './DashboardPage'

const STATUSES = [
  { id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: '#3b82f6', active: true, ticket_count: 3 },
  { id: 'st-resolved', name: 'Resolved', kind: 'system', sort_order: 2, color: '#22c55e', active: true, ticket_count: 8 },
]

const USERS = [
  { id: 'u-c1', email: 'client@example.com', display_name: 'Client One', role: 'user', disabled: false },
  { id: 'u-s1', email: 'staff@example.com', display_name: 'Staff One', role: 'staff', disabled: false },
]

const ago = (ms: number) => new Date(Date.now() - ms).toISOString()

const ticket = (id: string, over: object = {}) => ({
  id,
  tracking_number: `GHD-2026-${id}`,
  subject: `Subject ${id}`,
  description: '',
  description_format: 'plain',
  category_id: 'cat-1',
  priority: 'high',
  status_id: 'st-new',
  created_at: ago(5 * 24 * 3600 * 1000),
  ...over,
})

const STAFF_DASHBOARD: Dashboard = {
  queues: [
    { key: 'assigned_to_me', count: 4 },
    { key: 'my_groups', count: 11 },
    { key: 'sla_breached', count: 1 },
    { key: 'sla_at_risk', count: 0 },
    { key: 'awaiting_my_reply', count: 3 },
  ],
  attention: [
    { reason: 'breached', since: ago(3 * 24 * 3600 * 1000), ticket: ticket('b1') },
    { reason: 'reopened', since: ago(2 * 3600 * 1000), ticket: ticket('r1', { reopen_count: 1 }) },
  ] as Dashboard['attention'],
  volume: {
    tz: 'UTC',
    days: Array.from({ length: 30 }, (_, i) => ({
      date: new Date(Date.UTC(2026, 9, 26 + i)).toISOString().slice(0, 10),
      created: 2,
      resolved: 1,
    })),
  },
}

const REQUESTER_DASHBOARD: Dashboard = {
  queues: [{ key: 'my_tickets', count: 6 }],
  attention: null,
  volume: null,
}

type Answer = { data: unknown } | Error | Promise<{ data: unknown }>

let dashboardAnswers: Answer[] = []
let get: ReturnType<typeof vi.spyOn>

function serve(...answers: Answer[]) {
  dashboardAnswers = answers
}

function signInAs(role: Role) {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'someone@example.com', display_name: 'Someone', role, mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

function render(role: Role) {
  signInAs(role)
  return renderWithQuery(<DashboardPage />)
}

beforeEach(() => {
  vi.restoreAllMocks()
  dashboardAnswers = []
  get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/dashboard') {
      const a = dashboardAnswers.length > 1 ? dashboardAnswers.shift()! : dashboardAnswers[0]
      return a instanceof Error ? Promise.reject(a) : Promise.resolve(a)
    }
    if (url === '/statuses') return Promise.resolve({ data: STATUSES })
    if (url === '/admin/users') return Promise.resolve({ data: USERS })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [], attachment_scanning: { policy: 'off', configured: false, reachable: false, effect: '' } } })
    if (url === '/admin/settings') return Promise.resolve({ data: { sla_enabled: true, sla_alerts_enabled: true } })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})

const strip = (name: string) => screen.findByRole('navigation', { name })

describe('the dashboard blocks per role', () => {
  it.each(['staff', 'admin'] as const)('shows %s the strip, the attention list and the trend', async (role) => {
    serve({ data: STAFF_DASHBOARD })
    render(role)

    const nav = await strip('Your queue')
    expect(within(nav).getAllByRole('link')).toHaveLength(5)
    expect(screen.getByRole('heading', { name: 'Needs attention' })).toBeTruthy()
    expect(screen.getByText('GHD-2026-b1')).toBeTruthy()
    expect(screen.getByText('GHD-2026-r1')).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'Tickets in the last 30 days' })).toBeTruthy()
    expect(screen.getAllByText('Created').length).toBeGreaterThan(0)
  })

  it('shows a requester the strip of one cell and nothing else new', async () => {
    serve({ data: REQUESTER_DASHBOARD })
    render('user')

    const nav = await strip('Your tickets')
    expect(within(nav).getAllByRole('link')).toHaveLength(1)
    expect(screen.queryByRole('heading', { name: 'Needs attention' })).toBeNull()
    expect(screen.queryByRole('heading', { name: 'Tickets in the last 30 days' })).toBeNull()
  })

  it('keeps the status cards, View all tickets and By Client as they were (admin)', async () => {
    serve({ data: STAFF_DASHBOARD })
    render('admin')

    await strip('Your queue')
    const newCard = await screen.findByRole('link', { name: /^New \d/ })
    expect(newCard.getAttribute('href')).toBe('/tickets?status=st-new')
    expect(screen.getByRole('link', { name: /^Resolved \d/ }).getAttribute('href')).toBe('/tickets?status=st-resolved')
    expect(screen.getByRole('link', { name: 'View all tickets' }).getAttribute('href')).toBe('/tickets')
    expect(await screen.findByRole('heading', { name: 'By Client' })).toBeTruthy()
    expect(screen.getByText('Client One')).toBeTruthy()
    expect(screen.queryByText('Staff One')).toBeNull()
  })

  it('does not show staff By Client', async () => {
    serve({ data: STAFF_DASHBOARD })
    render('staff')
    await strip('Your queue')
    await screen.findByRole('link', { name: /^New \d/ })
    expect(screen.queryByRole('heading', { name: 'By Client' })).toBeNull()
  })

  it('asks for the summary in the browser time zone, and not for a requester an admin-only thing', async () => {
    serve({ data: STAFF_DASHBOARD })
    render('staff')
    await strip('Your queue')
    expect(get).toHaveBeenCalledWith('/dashboard', {
      params: { tz: Intl.DateTimeFormat().resolvedOptions().timeZone },
    })
    expect(get).not.toHaveBeenCalledWith('/admin/users', expect.anything())
  })

  it('shows an administrator the health card at the top, above the queue strip', async () => {
    serve({ data: STAFF_DASHBOARD })
    render('admin')
    const nav = await strip('Your queue')
    const card = await screen.findByText('No problems to report.')
    expect(card.compareDocumentPosition(nav) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it.each(['staff', 'user'] as const)('does not show %s the health card, and does not ask for its data', async (role) => {
    serve({ data: role === 'user' ? REQUESTER_DASHBOARD : STAFF_DASHBOARD })
    render(role)
    await screen.findByRole('navigation', { name: role === 'user' ? 'Your tickets' : 'Your queue' })
    await screen.findByRole('link', { name: /^New \d/ })
    expect(screen.queryByText(/No problems to report/)).toBeNull()
    expect(screen.queryByText(/Health checks/)).toBeNull()
    const asked = get.mock.calls.map((c: unknown[]) => c[0])
    expect(asked).not.toContain('/admin/settings')
    expect(asked).not.toContain('/admin/security-warnings')
  })

  it('makes no request of its own for the card: the banner and the card share one of each', async () => {
    serve({ data: STAFF_DASHBOARD })
    render('admin')
    await screen.findByText('No problems to report.')
    const count = (url: string) => get.mock.calls.filter((c: unknown[]) => c[0] === url).length
    expect(count('/admin/security-warnings')).toBe(1)
    expect(count('/admin/settings')).toBe(1)
  })
})

describe('the dashboard while loading', () => {
  it('shows a spinner in each of the three blocks, and the status cards meanwhile', async () => {
    serve(new Promise(() => {}))
    render('staff')

    await screen.findByRole('link', { name: /^New \d/ })
    for (const id of ['dashboard-strip-loading', 'dashboard-attention-loading', 'dashboard-trend-loading']) {
      const box = screen.getByTestId(id)
      expect(within(box).getByRole('status')).toBeTruthy()
      expect(box.className).toContain('min-h-')
    }
  })

  it('shows a requester one spinner', async () => {
    serve(new Promise(() => {}))
    render('user')
    await screen.findByRole('link', { name: /^New \d/ })
    expect(within(screen.getByTestId('dashboard-strip-loading')).getByRole('status')).toBeTruthy()
    expect(screen.queryByTestId('dashboard-attention-loading')).toBeNull()
    expect(screen.queryByTestId('dashboard-trend-loading')).toBeNull()
  })
})

describe('the dashboard when the summary fails', () => {
  it('replaces the three blocks with one message and keeps the rest', async () => {
    serve(new Error('boom'))
    render('admin')

    expect(await screen.findByText('The dashboard summary could not be loaded.')).toBeTruthy()
    expect(screen.getAllByText('The dashboard summary could not be loaded.')).toHaveLength(1)
    expect(screen.queryByRole('navigation', { name: 'Your queue' })).toBeNull()
    expect(screen.queryByRole('heading', { name: 'Needs attention' })).toBeNull()
    expect(screen.queryByRole('heading', { name: 'Tickets in the last 30 days' })).toBeNull()

    expect(await screen.findByRole('link', { name: /^New \d/ })).toBeTruthy()
    expect(screen.getByRole('link', { name: 'View all tickets' })).toBeTruthy()
    expect(await screen.findByText('Client One')).toBeTruthy()
  })

  it('Retry asks again and shows the blocks when it works', async () => {
    serve(new Error('boom'), { data: STAFF_DASHBOARD })
    const user = userEvent.setup()
    render('staff')

    await user.click(await screen.findByRole('button', { name: 'Retry' }))
    expect(await strip('Your queue')).toBeTruthy()
    await waitFor(() => expect(screen.queryByText('The dashboard summary could not be loaded.')).toBeNull())
    expect(get.mock.calls.filter((c: unknown[]) => c[0] === '/dashboard')).toHaveLength(2)
  })

  it('also covers a requester', async () => {
    serve(new Error('boom'))
    render('user')
    expect(await screen.findByText('The dashboard summary could not be loaded.')).toBeTruthy()
    expect(await screen.findByRole('link', { name: /^New \d/ })).toBeTruthy()
  })
})
