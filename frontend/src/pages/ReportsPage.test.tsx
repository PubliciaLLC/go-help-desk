import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { AxiosError } from 'axios'
import { renderWithQuery } from '@/test/render'
import { kpiResponse } from '@/test/kpiFixture'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { KpiResponse, Role } from '@/api/types'

// #131: the Reports page. The address is real here (navigate() writes it and
// useSearch() reads it back); the painter and the file saver are mocked, since
// jsdom has no canvas and the tests are about what the page does with them.

const router = vi.hoisted(() => ({
  search: {} as Record<string, unknown>,
  listeners: new Set<() => void>(),
}))

vi.mock('@tanstack/react-router', async () => {
  const React = await import('react')
  return {
    useSearch: () =>
      React.useSyncExternalStore(
        (cb) => {
          router.listeners.add(cb)
          return () => router.listeners.delete(cb)
        },
        () => router.search,
      ),
    useNavigate: () => {
      const nav = React.useRef((opts: { search: Record<string, unknown> | ((p: Record<string, unknown>) => Record<string, unknown>) }) => {
        router.search = typeof opts.search === 'function' ? opts.search(router.search) : opts.search
        router.listeners.forEach((l) => l())
      })
      return nav.current
    },
    useRouterState: () => ({ location: { pathname: '/reports', search: {} } }),
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    Link: ({ to, children, ...rest }: any) => (
      <a href={to} {...rest}>
        {children}
      </a>
    ),
  }
})

const exporter = vi.hoisted(() => ({
  readBoxes: vi.fn(() => []),
  paintPng: vi.fn(),
}))
vi.mock('@/components/charts/exportImage', async (orig) => ({
  ...(await orig<typeof import('@/components/charts/exportImage')>()),
  readBoxes: exporter.readBoxes,
  paintPng: exporter.paintPng,
}))

const saver = vi.hoisted(() => ({ saveDataUrl: vi.fn() }))
vi.mock('@/lib/saveFile', () => saver)

import { ReportsPage } from './ReportsPage'

const CATEGORIES = [
  { id: 'cat-1', name: 'Hardware', sort_order: 1, active: true },
  { id: 'cat-2', name: 'Software', sort_order: 2, active: true },
]
const GROUPS = [{ id: 'g1', name: 'Service Desk', description: '' }]

type Answer = { data: unknown } | Error
let answers: Answer[] = []
let get: ReturnType<typeof vi.spyOn>

function serve(...a: Answer[]) {
  answers = a
}
const ok = (resp: KpiResponse = kpiResponse()): Answer => ({ data: resp })

function badRequest(message: string): AxiosError {
  return new AxiosError('Request failed with status code 400', 'ERR_BAD_REQUEST', undefined, undefined, {
    status: 400,
    statusText: 'Bad Request',
    headers: {},
    config: {} as never,
    data: { error: { code: 'bad_request', message } },
  })
}

function signInAs(role: Role) {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'someone@example.com', display_name: 'Someone', role, mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

function render(role: Role = 'admin', search: Record<string, unknown> = {}) {
  router.search = search
  signInAs(role)
  return renderWithQuery(<ReportsPage />)
}

const kpiCalls = () => get.mock.calls.filter((c: unknown[]) => c[0] === '/reports/kpi')
const lastParams = () => (kpiCalls().at(-1)![1] as { params: Record<string, unknown> }).params
const board = () => document.getElementById('reports-board') as HTMLElement
const card = (title: string) => within(board()).getByRole('region', { name: title })
const tzNow = () => Intl.DateTimeFormat().resolvedOptions().timeZone
// A card, once the numbers have arrived (until then the same names are spinners).
const region = async (name: string) => {
  await screen.findByText(/compared with/)
  return card(name)
}
const isDisabled = (el: HTMLElement) => (el as HTMLButtonElement).disabled

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null)
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date(2026, 10, 24, 12, 0, 0)) // 24 Nov 2026, local
  router.search = {}
  router.listeners.clear()
  exporter.readBoxes.mockReset().mockReturnValue([])
  exporter.paintPng.mockReset().mockResolvedValue('data:image/png;base64,AAAA')
  saver.saveDataUrl.mockReset()
  answers = [ok()]
  get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/reports/kpi') {
      const a = answers.length > 1 ? answers.shift()! : answers[0]
      return a instanceof Error ? Promise.reject(a) : Promise.resolve(a)
    }
    if (url === '/categories') return Promise.resolve({ data: CATEGORIES })
    if (url === '/groups') return Promise.resolve({ data: GROUPS })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})

afterEach(() => {
  vi.useRealTimers()
})

describe('the Reports page', () => {
  it.each(['admin', 'staff'] as const)('shows %s the header and the seven cards in order', async (role) => {
    render(role)
    expect(await screen.findByRole('heading', { name: 'Reports', level: 1 })).toBeTruthy()
    await screen.findByText('26 Oct – 24 Nov 2026, compared with 26 Sep – 25 Oct')
    expect(screen.getByText('All categories · All priorities · All groups')).toBeTruthy()

    const titles = within(board()).getAllByRole('region').map((r) => r.getAttribute('aria-label') ?? within(r).getByRole('heading', { level: 2 }).textContent)
    expect(titles).toEqual(['Time to resolve', 'First response', 'SLA attainment', 'Reopen rate', 'Volume', 'Backlog aging', 'Workload'])
  })

  it('asks for the last 30 days ending today in the browser zone, with no filters', async () => {
    render()
    await screen.findByText(/compared with/)
    expect(kpiCalls()).toHaveLength(1)
    expect(lastParams()).toEqual({ tz: tzNow(), from: '2026-10-26', to: '2026-11-24' })
  })

  it('draws each card from its model, with the server comparison', async () => {
    render()
    const ttr = await region('Time to resolve')
    expect(within(ttr).getByText('14 h 30 min')).toBeTruthy() // 52200 s
    expect(within(ttr).getByText('4 d')).toBeTruthy() // 345600 s
    expect(within(ttr).getByText('79 tickets resolved')).toBeTruthy()
    // The arrow is drawn; the sentence is for a screen reader.
    expect(within(ttr).getByText('▼ 5%')).toBeTruthy()
    expect(within(ttr).getByText('down 5% from the previous period')).toBeTruthy()
    expect(within(card('SLA attainment')).getByText('▲ 5.3 pts')).toBeTruthy()
    expect(within(card('Backlog aging')).getByText('Open now')).toBeTruthy()
  })

  it('says what a card lacks instead of showing zeros', async () => {
    const empty = kpiResponse()
    empty.measures.current.time_to_resolve = { count: 0, median_seconds: null, p90_seconds: null }
    serve(ok(empty))
    render()
    expect(await within(board()).findByText('No tickets were resolved in this period.', { selector: 'p' })).toBeTruthy()
  })

  it('shows the filter line with the names of what is chosen', async () => {
    serve(ok(kpiResponse({ filters: { category_id: 'cat-1', priority: ['critical', 'high'], assignee_group_id: 'g1' } })))
    render('admin', { category_id: 'cat-1', priority: ['critical', 'high'], assignee_group_id: 'g1' })
    expect(await screen.findByText('Hardware · Critical, High · Service Desk')).toBeTruthy()
    expect(lastParams()).toMatchObject({ category_id: 'cat-1', priority: ['critical', 'high'], assignee_group_id: 'g1' })
  })

  it('puts a draw of the volume chart and the bar charts in the Volume card', async () => {
    render()
    const volume = await region('Volume')
    expect(within(volume).getByRole('img', { name: 'Tickets created and resolved per day, 26 Oct – 24 Nov 2026' })).toBeTruthy()
    expect(within(volume).getByRole('img', { name: 'Volume created by priority' })).toBeTruthy()
    expect(within(volume).getByText('Hardware', { selector: '[title]' })).toBeTruthy()
  })
})

describe('loading and failure', () => {
  it('shows a spinner in each card until the numbers arrive', async () => {
    let release!: (a: Answer) => void
    get.mockImplementation(((url: string) => {
      if (url === '/reports/kpi') return new Promise((r) => (release = r))
      return Promise.resolve({ data: [] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    render()
    const regions = await within(board()).findAllByRole('region')
    expect(regions).toHaveLength(7)
    for (const r of regions) expect(within(r).getByRole('status')).toBeTruthy()
    expect(isDisabled(screen.getByRole('button', { name: 'Download CSV' }))).toBe(true)
    expect(isDisabled(screen.getByRole('button', { name: 'Download image' }))).toBe(true)

    release(ok())
    await screen.findByText(/compared with/)
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('replaces the cards with one message and Retry, and keeps the controls', async () => {
    serve(new Error('boom'))
    render()
    expect(await screen.findByText('The reports could not be loaded.', undefined, { timeout: 4000 })).toBeTruthy()
    expect(within(board()).queryByRole('region')).toBeNull()
    expect(screen.getByRole('button', { name: '30 days' })).toBeTruthy()
    expect(screen.getByLabelText('Category')).toBeTruthy()

    serve(ok())
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await region('Volume')).toBeTruthy()
    expect(screen.queryByText('The reports could not be loaded.')).toBeNull()
  })

  it('shows the server message under the dates when a custom range is refused', async () => {
    serve(badRequest('from and to go together'))
    render('admin', { from: '2026-11-01' })
    const dates = await screen.findByRole('group', { name: 'Custom range' })
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('from and to go together')
    // Under the dates, not in the cards; the controls are still there to fix it.
    expect(dates.compareDocumentPosition(alert) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.queryByText('The reports could not be loaded.')).toBeNull()
    expect((screen.getByLabelText('From') as HTMLInputElement).value).toBe('2026-11-01')
    // A bad request is not retried: it would only get the same answer.
    expect(kpiCalls()).toHaveLength(1)
  })
})

describe('the controls', () => {
  it('writes a preset to the address and asks again', async () => {
    render()
    await screen.findByText(/compared with/)
    fireEvent.click(screen.getByRole('button', { name: '7 days' }))
    await waitFor(() => expect(lastParams()).toEqual({ tz: tzNow(), from: '2026-11-18', to: '2026-11-24' }))
    expect(router.search).toEqual({ range: 7 })
    expect(screen.getByRole('button', { name: '7 days' }).getAttribute('aria-pressed')).toBe('true')
    expect(screen.getByRole('button', { name: '30 days' }).getAttribute('aria-pressed')).toBe('false')
  })

  it('reads the preset out of the address', async () => {
    render('admin', { range: 90 })
    await screen.findByText(/compared with/)
    expect(lastParams()).toMatchObject({ from: '2026-08-27', to: '2026-11-24' })
    expect(screen.getByRole('button', { name: '90 days' }).getAttribute('aria-pressed')).toBe('true')
  })

  it('Custom shows the dates, starting from the range on screen, and Apply writes them', async () => {
    render()
    await screen.findByText(/compared with/)
    expect(screen.queryByLabelText('From')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Custom' }))
    expect(((await screen.findByLabelText('From')) as HTMLInputElement).value).toBe('2026-10-26')
    expect((screen.getByLabelText('To') as HTMLInputElement).value).toBe('2026-11-24')

    fireEvent.change(screen.getByLabelText('From'), { target: { value: '2026-11-01' } })
    fireEvent.change(screen.getByLabelText('To'), { target: { value: '2026-11-10' } })
    // Nothing is asked until Apply.
    expect(router.search).toEqual({ from: '2026-10-26', to: '2026-11-24' })
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    await waitFor(() => expect(lastParams()).toEqual({ tz: tzNow(), from: '2026-11-01', to: '2026-11-10' }))
    expect(router.search).toEqual({ from: '2026-11-01', to: '2026-11-10' })
  })

  it('offers the top-level categories, the four priorities and the groups', async () => {
    render()
    await screen.findByText(/compared with/)
    const category = screen.getByLabelText('Category')
    await within(category).findByRole('option', { name: 'Hardware' })
    expect(within(category).getAllByRole('option').map((o) => o.textContent)).toEqual(['Any category', 'Hardware', 'Software'])
    expect(within(screen.getByLabelText('Priority')).getAllByRole('option').map((o) => o.textContent)).toEqual([
      'Any priority', 'Critical', 'High', 'Medium', 'Low',
    ])
    await within(screen.getByLabelText('Group')).findByRole('option', { name: 'Service Desk' })
  })

  it('a select writes its filter to the address and asks again', async () => {
    render()
    await screen.findByText(/compared with/)
    await within(screen.getByLabelText('Category')).findByRole('option', { name: 'Hardware' })
    fireEvent.change(screen.getByLabelText('Category'), { target: { value: 'cat-1' } })
    fireEvent.change(screen.getByLabelText('Priority'), { target: { value: 'high' } })
    await within(screen.getByLabelText('Group')).findByRole('option', { name: 'Service Desk' })
    fireEvent.change(screen.getByLabelText('Group'), { target: { value: 'g1' } })
    await waitFor(() =>
      expect(lastParams()).toMatchObject({ category_id: 'cat-1', priority: ['high'], assignee_group_id: 'g1' }),
    )
    expect(router.search).toMatchObject({ range: 30, category_id: 'cat-1', priority: 'high', assignee_group_id: 'g1' })
    fireEvent.change(screen.getByLabelText('Category'), { target: { value: '' } })
    expect(router.search).not.toHaveProperty('category_id')
  })

  it('keeps several priorities from a shared link visible in the select', async () => {
    render('admin', { priority: ['critical', 'high'] })
    await screen.findByText(/compared with/)
    expect((screen.getByLabelText('Priority') as HTMLSelectElement).selectedOptions[0].textContent).toBe('Critical, High')
  })
})

describe('exporting', () => {
  it('the board button repaints the board and saves it under the board name', async () => {
    render()
    await screen.findByText(/compared with/)
    fireEvent.click(screen.getByRole('button', { name: 'Download image' }))
    await waitFor(() => expect(saver.saveDataUrl).toHaveBeenCalledTimes(1))
    expect(saver.saveDataUrl).toHaveBeenCalledWith('data:image/png;base64,AAAA', 'kpis-2026-10-26-to-2026-11-24.png')
    expect(exporter.readBoxes).toHaveBeenCalledWith(board(), expect.any(Function))
    expect(exporter.paintPng).toHaveBeenCalledTimes(1)
  })

  it('a card button repaints that card and says which period and filters the strip shows', async () => {
    render()
    const ttr = await region('Time to resolve')
    fireEvent.click(within(ttr).getByRole('button', { name: 'Download Time to resolve as an image' }))
    await waitFor(() => expect(saver.saveDataUrl).toHaveBeenCalledTimes(1))
    expect(saver.saveDataUrl).toHaveBeenCalledWith('data:image/png;base64,AAAA', 'kpi-time_to_resolve-2026-10-26-to-2026-11-24.png')
    expect(exporter.readBoxes).toHaveBeenCalledWith(ttr, expect.any(Function))
    // The strip: period, filters, when it was made.
    const boxes = exporter.paintPng.mock.calls[0][0] as { kind: string; text?: string }[]
    const text = boxes.filter((b) => b.kind === 'text').map((b) => b.text).join(' ')
    expect(text).toContain('26 Oct – 24 Nov 2026, compared with 26 Sep – 25 Oct')
    expect(text).toContain('All categories · All priorities · All groups')
    expect(text).toMatch(/Created .*Go Help Desk/)
  })

  it('is disabled and busy while the image is made, then works again', async () => {
    let finish!: (url: string) => void
    exporter.paintPng.mockReturnValue(new Promise<string>((r) => (finish = r)))
    render()
    const ttr = await region('Time to resolve')
    const button = within(ttr).getByRole('button', { name: 'Download Time to resolve as an image' })
    fireEvent.click(button)
    await waitFor(() => expect(isDisabled(button)).toBe(true))
    expect(button.getAttribute('aria-busy')).toBe('true')
    // Another card's button is not held up by it.
    expect(isDisabled(within(card('Volume')).getByRole('button', { name: 'Download Volume as an image' }))).toBe(false)

    finish('data:image/png;base64,BBBB')
    await waitFor(() => expect(isDisabled(button)).toBe(false))
    expect(button.getAttribute('aria-busy')).not.toBe('true')
    expect(saver.saveDataUrl).toHaveBeenCalledWith('data:image/png;base64,BBBB', expect.stringMatching(/\.png$/))
  })

  it('says so beside the button, and saves nothing, when the image cannot be made', async () => {
    exporter.paintPng.mockRejectedValue(new Error('tainted'))
    render()
    const ttr = await region('Time to resolve')
    const button = within(ttr).getByRole('button', { name: 'Download Time to resolve as an image' })
    fireEvent.click(button)
    const message = 'The image could not be created in this browser. Download the CSV for the same numbers.'
    expect(await within(ttr).findByText(message)).toBeTruthy()
    expect(saver.saveDataUrl).not.toHaveBeenCalled()
    expect(isDisabled(button)).toBe(false)
    // A second try clears the message first.
    exporter.paintPng.mockResolvedValue('data:image/png;base64,CCCC')
    fireEvent.click(button)
    await waitFor(() => expect(saver.saveDataUrl).toHaveBeenCalledTimes(1))
    expect(within(ttr).queryByText(message)).toBeNull()
  })

  it('the board button reports its own failure', async () => {
    exporter.paintPng.mockRejectedValue(new Error('too large'))
    render()
    await screen.findByText(/compared with/)
    fireEvent.click(screen.getByRole('button', { name: 'Download image' }))
    expect(await screen.findByText(/The image could not be created in this browser/)).toBeTruthy()
    expect(saver.saveDataUrl).not.toHaveBeenCalled()
  })

  it('Download CSV saves the numbers on screen as a data: URL', async () => {
    serve(ok(kpiResponse({ filters: { category_id: 'cat-1', priority: [], assignee_group_id: null } })))
    render('admin', { category_id: 'cat-1' })
    await screen.findByText(/compared with/)
    await screen.findByText(/^Hardware ·/)
    fireEvent.click(screen.getByRole('button', { name: 'Download CSV' }))
    expect(saver.saveDataUrl).toHaveBeenCalledTimes(1)
    const [url, name] = saver.saveDataUrl.mock.calls[0] as [string, string]
    expect(name).toBe('kpis-2026-10-26-to-2026-11-24.csv')
    expect(url.startsWith('data:text/csv;charset=utf-8,%EF%BB%BF')).toBe(true)
    const text = decodeURIComponent(url.slice('data:text/csv;charset=utf-8,'.length))
    expect(text.startsWith('﻿measure,key,label,current,previous,change,unit\r\n')).toBe(true)
    expect(text).toContain('filter_category,,Hardware,')
    expect(text).toContain('created,,Created,84,72,0.1667,count')
  })

  it('marks every control that is not content to be left out of an image', async () => {
    const wl = kpiResponse()
    wl.workload.by_assignee = Array.from({ length: 12 }, (_, i) => ({ id: `u${i}`, name: `Person ${i}`, count: 12 - i }))
    serve(ok(wl))
    render()
    await screen.findByText(/compared with/)
    const buttons = [
      screen.getByRole('button', { name: 'Download CSV' }),
      screen.getByRole('button', { name: 'Download image' }),
      ...screen.getAllByRole('button', { name: /^Download .* as an image$/ }),
      screen.getByRole('button', { name: 'Show all (12)' }),
    ]
    expect(buttons).toHaveLength(2 + 7 + 1)
    for (const b of buttons) expect(b.closest('[data-export="skip"]'), b.textContent ?? '').not.toBeNull()
    // The controls are outside the board altogether.
    expect(board().contains(screen.getByRole('button', { name: '7 days' }))).toBe(false)
    expect(board().contains(screen.getByLabelText('Category'))).toBe(false)
    // The failure message is not content either.
    exporter.paintPng.mockRejectedValue(new Error('x'))
    fireEvent.click(screen.getByRole('button', { name: 'Download Volume as an image' }))
    const message = await within(card('Volume')).findByText(/The image could not be created/)
    expect(message.closest('[data-export="skip"]')).not.toBeNull()
  })
})

describe('Workload', () => {
  it('shows the ten largest rows of a list and Show all (N) for the rest', async () => {
    const wl = kpiResponse()
    wl.workload.by_assignee = Array.from({ length: 12 }, (_, i) => ({ id: `u${i}`, name: `Person ${i}`, count: 12 - i }))
    serve(ok(wl))
    render()
    const workload = await region('Workload')
    expect(within(workload).getByText('Person 9', { selector: '[title]' })).toBeTruthy()
    expect(within(workload).queryByText('Person 10', { selector: '[title]' })).toBeNull()
    // The group list has two rows: nothing to show.
    expect(within(workload).getAllByRole('button')).toHaveLength(2) // image + Show all

    fireEvent.click(within(workload).getByRole('button', { name: 'Show all (12)' }))
    expect(within(workload).getByText('Person 11', { selector: '[title]' })).toBeTruthy()
    expect(within(workload).queryByRole('button', { name: 'Show all (12)' })).toBeNull()
  })

  it('names the two rows of tickets nobody holds', async () => {
    render()
    const workload = await region('Workload')
    expect(within(workload).getByText('Nobody', { selector: '[title]' })).toBeTruthy()
    expect(within(workload).getByText('No group', { selector: '[title]' })).toBeTruthy()
  })
})
