import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, AxiosHeaders } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { FieldDef, FieldPlacement, FieldUsage } from '@/api/types'

// Custom Fields A, PR-6: the Custom Fields page against the API shapes in the
// design (§8), faked here. Real drag and keyboard reordering is a browser
// matter and is covered in e2e/custom-fields.spec.ts; jsdom has no layout for
// dnd-kit to measure.

let search: { field?: string } = {}

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/custom-fields' } }),
  useSearch: () => search,
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children }: any) => <a href={to}>{children}</a>,
}))

// A stand-in for the real list: it renders the rows with a plain handle, and
// offers one button that "drops" the last row first so a test can see what the
// page sends for a reorder.
vi.mock('@/components/admin/SortableList', () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  SortableList: ({ items, onReorder, renderItem }: any) => (
    <>
      <button onClick={() => onReorder([items[items.length - 1].id, ...items.slice(0, -1).map((i: { id: string }) => i.id)])}>
        stub: move last to first
      </button>
      <ul>
        {/* eslint-disable-next-line @typescript-eslint/no-explicit-any */}
        {items.map((item: any) => (
          <li key={item.id}>{renderItem(item, <button aria-label={`Reorder ${item.label}`}>::</button>)}</li>
        ))}
      </ul>
    </>
  ),
}))

import { CustomFieldsPage } from './CustomFieldsPage'

const D = '2026-01-01T00:00:00Z'
function def(over: Partial<FieldDef> & Pick<FieldDef, 'id' | 'name'>): FieldDef {
  return { field_type: 'text', sort_order: 0, active: true, created_at: D, ...over }
}

const DEFS: FieldDef[] = [
  def({ id: 'f-dept', name: 'Department', field_type: 'select', options: ['Finance', 'Sales, EMEA', 'IT'], retired_options: ['Marketing', 'Legal'], sort_order: 1 }),
  def({ id: 'f-old', name: 'Old cost centre', field_type: 'number', sort_order: 2, active: false }),
  def({ id: 'f-phone', name: 'Phone number', sort_order: 3 }),
  def({ id: 'f-asset', name: 'Asset Tag', sort_order: 4 }),
]

const node = (id: string, name: string, active = true) => ({ id, name, active })
const HARDWARE = node('c-1', 'Hardware')
const NETWORK = node('c-2', 'Network')
const VPN = node('t-2', 'VPN')

function placement(id: string, field: string, p: Partial<FieldPlacement> & Pick<FieldPlacement, 'scope_type' | 'scope_id' | 'category'>): FieldPlacement {
  return { id, field_def_id: field, sort_order: 0, visible_on_new: true, required_on_new: false, type: null, item: null, ...p }
}

const PLACEMENTS: Record<string, FieldPlacement[]> = {
  'f-dept': [
    placement('a-1', 'f-dept', { scope_type: 'category', scope_id: 'c-1', category: HARDWARE, required_on_new: true }),
    placement('a-2', 'f-dept', { scope_type: 'type', scope_id: 't-2', category: NETWORK, type: VPN }),
  ],
  'f-old': [placement('a-3', 'f-old', { scope_type: 'category', scope_id: 'c-1', category: HARDWARE })],
  'f-phone': [
    placement('a-4', 'f-phone', { scope_type: 'item', scope_id: 'i-1', category: HARDWARE, type: node('t-1', 'Laptop'), item: node('i-1', 'Battery') }),
  ],
  'f-asset': [],
}

const USAGE: Record<string, FieldUsage> = {
  'f-dept': {
    tickets_with_value: 34,
    values: [
      { value: 'Finance', tickets: 17 }, { value: 'Sales, EMEA', tickets: 14 }, { value: 'IT', tickets: 0 },
      { value: 'Marketing', tickets: 3 }, { value: 'Legal', tickets: 0 },
    ],
  },
  'f-old': { tickets_with_value: 0, values: [] },
  'f-phone': { tickets_with_value: 0, values: [] },
  'f-asset': { tickets_with_value: 0, values: [] },
}

const TREE = {
  categories: [node('c-1', 'Hardware'), node('c-2', 'Network'), node('c-3', 'Software')],
  types: {
    'c-1': [{ ...node('t-1', 'Laptop'), category_id: 'c-1' }],
    'c-2': [{ ...node('t-2', 'VPN'), category_id: 'c-2' }, { ...node('t-3', 'WiFi'), category_id: 'c-2' }],
    'c-3': [],
  } as Record<string, object[]>,
  items: { 't-1': [{ ...node('i-1', 'Battery'), type_id: 't-1' }, { ...node('i-2', 'Screen'), type_id: 't-1' }] } as Record<string, object[]>,
}

function refusal(status: number, code: string, message: string) {
  const headers = new AxiosHeaders()
  return new AxiosError('Request failed', 'ERR_BAD_REQUEST', { headers } as never, null, {
    status, statusText: 'x', headers, config: { headers } as never, data: { error: { code, message } },
  } as never)
}

/** A small stateful stand-in for the server, so a refetch after a write sees the write. */
function fakeServer(opts: { defs?: FieldDef[]; placements?: Record<string, FieldPlacement[]>; usage?: Record<string, FieldUsage> } = {}) {
  let defs = structuredClone(opts.defs ?? DEFS)
  const placements = structuredClone(opts.placements ?? PLACEMENTS)
  const usage = structuredClone(opts.usage ?? USAGE)
  const calls = { get: [] as string[] }
  const fail: { patch?: unknown; put?: unknown } = {}
  // While set, the list GET waits for it: lets a test look at the page before a refetch lands.
  const hold: { list?: Promise<void> } = {}

  const get = vi.spyOn(api, 'get').mockImplementation((async (url: string) => {
    calls.get.push(url)
    const m = url.match(/^\/admin\/custom-fields\/([^/]+)\/(assignments|usage)$/)
    // place_count is on the list only, as on the real API.
    if (url === '/admin/custom-fields' && hold.list) await hold.list
    if (url === '/admin/custom-fields') return { data: defs.map((d) => ({ ...d, place_count: (placements[d.id] ?? []).length })) }
    if (m && m[2] === 'assignments') return { data: placements[m[1]] ?? [] }
    if (m && m[2] === 'usage') return { data: usage[m[1]] }
    if (url === '/admin/categories') return { data: TREE.categories }
    const t = url.match(/^\/admin\/categories\/([^/]+)\/types$/)
    if (t) return { data: TREE.types[t[1]] ?? [] }
    const i = url.match(/^\/admin\/categories\/[^/]+\/types\/([^/]+)\/items$/)
    if (i) return { data: TREE.items[i[1]] ?? [] }
    return { data: [] }
  }) as never)

  const post = vi.spyOn(api, 'post').mockImplementation((async (url: string, body: Record<string, unknown>) => {
    if (url === '/admin/custom-fields') {
      const created = def({ id: 'f-new', name: body.name as string, field_type: body.field_type as FieldDef['field_type'], options: body.options as string[] | undefined, sort_order: body.sort_order as number })
      defs = [...defs, created]
      placements['f-new'] = []
      usage['f-new'] = { tickets_with_value: 0, values: [] }
      return { data: created }
    }
    // Attaching a place: POST /admin/categories/:c[/types/:t[/items/:i]]/fields
    const at = url.match(/^\/admin\/categories\/([^/]+)(?:\/types\/([^/]+))?(?:\/items\/([^/]+))?\/fields$/)
    if (at) {
      const cat = TREE.categories.find((c) => c.id === at[1])!
      const type = at[2] ? (TREE.types[at[1]] as { id: string; name: string; active: boolean }[]).find((t) => t.id === at[2]) : undefined
      const item = at[3] ? (TREE.items[at[2]] as { id: string; name: string; active: boolean }[]).find((i) => i.id === at[3]) : undefined
      const id = `a-new-${(placements[body.field_def_id as string] ?? []).length}`
      const added = placement(id, body.field_def_id as string, {
        scope_type: item ? 'item' : type ? 'type' : 'category', scope_id: (item ?? type ?? cat).id, category: cat,
        type: type ? node(type.id, type.name) : null, item: item ? node(item.id, item.name) : null,
        visible_on_new: body.visible_on_new as boolean, required_on_new: body.required_on_new as boolean,
      })
      placements[added.field_def_id] = [...(placements[added.field_def_id] ?? []), added]
      return { data: added }
    }
    return { data: { id: 'a-new', ...body } }
  }) as never)

  const patch = vi.spyOn(api, 'patch').mockImplementation((async (url: string, body: Partial<FieldDef> & Record<string, unknown>) => {
    if (fail.patch) throw fail.patch
    const id = url.split('/').pop()!
    if (url.startsWith('/admin/custom-fields/')) {
      defs = defs.map((d) => (d.id === id ? { ...d, ...body } : d))
      return { data: defs.find((d) => d.id === id) }
    }
    return { data: {} }
  }) as never)

  const put = vi.spyOn(api, 'put').mockImplementation((async (_url: string, body: { field_def_ids: string[] }) => {
    if (fail.put) throw fail.put
    defs = body.field_def_ids.map((id, n) => ({ ...defs.find((d) => d.id === id)!, sort_order: n + 1 }))
    return { data: defs.map((d) => ({ ...d, place_count: (placements[d.id] ?? []).length })) }
  }) as never)

  const del = vi.spyOn(api, 'delete').mockImplementation((async (url: string) => {
    const id = url.split('/').pop()!
    for (const k of Object.keys(placements)) placements[k] = placements[k].filter((p) => p.id !== id)
    return { data: {} }
  }) as never)

  return { get, post, patch, put, del, calls, fail, hold, setUsage: (id: string, u: FieldUsage) => { usage[id] = u },
    // Someone else changes a definition.
    changeDef: (id: string, over: Partial<FieldDef>) => { defs = defs.map((d) => (d.id === id ? { ...d, ...over } : d)) }, setPlacements: (id: string, p: FieldPlacement[]) => { placements[id] = p } }
}

beforeEach(() => {
  vi.restoreAllMocks()
  search = {}
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'a@example.com', display_name: 'Ada', role: 'admin', mfa_enabled: false,
      created_at: D, updated_at: D,
    },
  })
})

const rowButton = (name: string) => screen.findByRole('button', { name: new RegExp(`^${name}`) })
const rowOf = (name: string) => screen.findByRole('group', { name })

async function expand(name: string) {
  await userEvent.click(await rowButton(name))
  return rowOf(name)
}

async function renderPage() {
  const server = fakeServer()
  const view = renderWithQuery(<CustomFieldsPage />)
  await screen.findByRole('button', { name: /^Department/ })
  return { server, ...view }
}

describe('header and list', () => {
  it('explains what a field is and drops the old "cannot be deleted" warning', async () => {
    await renderPage()
    expect(screen.getByRole('heading', { name: 'Custom Fields' })).toBeTruthy()
    expect(
      screen.getByText('Extra questions on a ticket. A field appears only where you attach it: to a category, a type or an item.'),
    ).toBeTruthy()
    expect(document.body.textContent).not.toMatch(/cannot be deleted/i)
    expect(document.body.textContent).not.toMatch(/only deactivated/i)
  })

  it('says how the order works', async () => {
    await renderPage()
    expect(
      screen.getByText(
        "Drag to set the order fields appear in. On a ticket, fields still appear by level: the category's first, then the type's, then the item's.",
      ),
    ).toBeTruthy()
  })

  it('lists active fields in the order the server sends, with their type and where they appear', async () => {
    await renderPage()
    const names = screen.getAllByRole('button', { name: /^Reorder / }).map((b) => b.getAttribute('aria-label'))
    expect(names).toEqual(['Reorder Department', 'Reorder Phone number', 'Reorder Asset Tag'])

    expect(within(await rowOf('Department')).getByText('Select')).toBeTruthy()
    expect(await within(await rowOf('Department')).findByText('On 2 places')).toBeTruthy()
    expect(await within(await rowOf('Phone number')).findByText('On 1 place')).toBeTruthy()
    expect(await within(await rowOf('Asset Tag')).findByText('Not on any form yet')).toBeTruthy()
  })

  it('reads place counts from the list and loads places and usage only for a row that is opened', async () => {
    const { server } = await renderPage()
    await within(await rowOf('Department')).findByText('On 2 places')
    expect(server.calls.get.some((u) => u.endsWith('/assignments') || u.endsWith('/usage'))).toBe(false)

    await expand('Department')
    await waitFor(() => expect(server.calls.get).toContain('/admin/custom-fields/f-dept/usage'))
    expect(server.calls.get).toContain('/admin/custom-fields/f-dept/assignments')
    expect(server.calls.get.some((u) => u.includes('f-asset'))).toBe(false)
  })

  it('keeps a row\'s place count after a save, whose response has none', async () => {
    const { server } = await renderPage()
    const row = await expand('Asset Tag')
    const name = await within(row).findByLabelText('Name')
    await userEvent.clear(name)
    await userEvent.type(name, 'Asset number')
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(server.patch).toHaveBeenCalled())
    // Department was not touched; its count must still be there, and the renamed row shows its own.
    expect(await within(await rowOf('Asset number')).findByText('Not on any form yet')).toBeTruthy()
    expect(within(await rowOf('Department')).getByText('On 2 places')).toBeTruthy()
  })

  it('clears the resolved-fields cache the new-ticket form and ticket card read, when a place is added', async () => {
    const { queryClient } = await renderPage()
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    const row = await expand('Asset Tag')
    const form = within(await within(row).findByRole('group', { name: 'Add a place' }))
    await userEvent.selectOptions(form.getByLabelText('Category'), 'c-3')
    await userEvent.click(form.getByRole('button', { name: 'Add' }))
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ['ctiFieldsResolved'] }))
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['ctiFields'] })
  })

  it('refreshes the count on the row after a place is added', async () => {
    await renderPage()
    const row = await expand('Asset Tag')
    await within(row).findByText('Not on any form yet.')
    const form = within(await within(row).findByRole('group', { name: 'Add a place' }))
    await userEvent.selectOptions(form.getByLabelText('Category'), 'c-3')
    await userEvent.click(form.getByRole('button', { name: 'Add' }))
    expect(await within(row).findByText('On 1 place')).toBeTruthy()
  })

  it('opens the row named by ?field= and, for an inactive field, shows it', async () => {
    search = { field: 'f-old' }
    fakeServer()
    renderWithQuery(<CustomFieldsPage />)
    const row = await rowOf('Old cost centre')
    expect(await within(row).findByLabelText('Name')).toBeTruthy()
    expect(within(row).getByText('Inactive')).toBeTruthy()
  })
})

describe('Show inactive', () => {
  it('hides an inactive field until Show inactive is on, then marks it and offers Reactivate', async () => {
    const { server } = await renderPage()
    expect(screen.queryByText('Old cost centre')).toBeNull()

    await userEvent.click(screen.getByRole('checkbox', { name: 'Show inactive' }))
    const row = await rowOf('Old cost centre')
    expect(within(row).getByText('Inactive')).toBeTruthy()

    await userEvent.click(within(row).getByRole('button', { name: 'Reactivate Old cost centre' }))
    await waitFor(() => expect(server.patch).toHaveBeenCalledWith('/admin/custom-fields/f-old', { active: true }))
  })
})

describe('Reactivate', () => {
  it('shows a refusal as an alert on the row, with the dialog closed', async () => {
    const { server } = await renderPage()
    await userEvent.click(screen.getByRole('checkbox', { name: 'Show inactive' }))
    const row = await rowOf('Old cost centre')
    server.fail.patch = refusal(500, 'internal_error', 'could not save')
    await userEvent.click(within(row).getByRole('button', { name: 'Reactivate Old cost centre' }))

    expect((await within(row).findByRole('alert')).textContent).toContain('could not save')
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})

describe('Deactivate', () => {
  it('asks first, explains the effect, sends active=false and the row goes away', async () => {
    const { server } = await renderPage()
    const row = await expand('Asset Tag')

    await userEvent.click(within(row).getByRole('button', { name: 'Deactivate…' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Deactivate Asset Tag?')).toBeTruthy()
    expect(
      within(dialog).getByText(
        'It stops appearing on new tickets and can no longer be changed on any ticket. Tickets that already have a value keep showing it. It will be hidden from this list. Turn on Show inactive to reactivate it.',
      ),
    ).toBeTruthy()
    expect(server.patch).not.toHaveBeenCalled()

    await userEvent.click(within(dialog).getByRole('button', { name: 'Deactivate' }))
    await waitFor(() => expect(server.patch).toHaveBeenCalledWith('/admin/custom-fields/f-asset', { active: false }))
    await waitFor(() => expect(screen.queryByRole('group', { name: 'Asset Tag' })).toBeNull())

    await userEvent.click(screen.getByRole('checkbox', { name: 'Show inactive' }))
    const back = await rowOf('Asset Tag')
    expect(within(back).getByRole('button', { name: 'Reactivate Asset Tag' })).toBeTruthy()
  })
})

describe('Create', () => {
  async function openForm() {
    const view = await renderPage()
    await userEvent.click(screen.getByRole('button', { name: /New field/ }))
    return view
  }

  it('sends a select option containing a comma as one option, numbered after the highest field', async () => {
    const { server } = await openForm()
    await userEvent.type(screen.getByLabelText('Name'), 'Region')
    await userEvent.selectOptions(screen.getByLabelText('Type'), 'select')
    await userEvent.type(screen.getByLabelText('Option 1'), 'Sales, EMEA')
    await userEvent.click(screen.getByRole('button', { name: 'Add option' }))
    await userEvent.type(screen.getByLabelText('Option 2'), 'IT')
    await userEvent.click(screen.getByRole('button', { name: 'Create field' }))

    // Highest number on the list is 4, an inactive field's included.
    await waitFor(() =>
      expect(server.post).toHaveBeenCalledWith('/admin/custom-fields', {
        name: 'Region', field_type: 'select', options: ['Sales, EMEA', 'IT'], sort_order: 5,
      }),
    )
  })

  it('says the new field is on no form, opens Where it appears and moves focus to Add a place', async () => {
    await openForm()
    await userEvent.type(screen.getByLabelText('Name'), 'Serial')
    await userEvent.click(screen.getByRole('button', { name: 'Create field' }))

    const banner = await screen.findByRole('status')
    expect(banner.textContent).toContain("Serial created. It isn't on any ticket form yet. Choose where it appears.")

    const row = await rowOf('Serial')
    expect(within(row).getByText('Where it appears')).toBeTruthy()
    expect(within(row).getByText('Not on any form yet.')).toBeTruthy()
    await waitFor(() => expect(document.activeElement).toBe(within(row).getByRole('button', { name: 'Add a place' })))
  })

  it('takes the banner down once the new field has a place', async () => {
    await openForm()
    await userEvent.type(screen.getByLabelText('Name'), 'Serial')
    await userEvent.click(screen.getByRole('button', { name: 'Create field' }))
    await screen.findByRole('status')

    const row = await rowOf('Serial')
    const form = within(await within(row).findByRole('group', { name: 'Add a place' }))
    await userEvent.selectOptions(form.getByLabelText('Category'), 'c-3')
    await userEvent.click(form.getByRole('button', { name: 'Add' }))
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
  })

  it('refuses a blank option and a select with no options, and sends nothing', async () => {
    const { server } = await openForm()
    await userEvent.type(screen.getByLabelText('Name'), 'Region')
    await userEvent.selectOptions(screen.getByLabelText('Type'), 'select')
    await userEvent.click(screen.getByRole('button', { name: 'Create field' }))

    expect((await screen.findAllByRole('alert')).map((a) => a.textContent).join(' ')).toMatch(/can't be blank/)
    expect(server.post).not.toHaveBeenCalled()
  })

  it('has no field-number input: the number is set by dragging', async () => {
    await openForm()
    expect(screen.queryByLabelText(/sort order/i)).toBeNull()
  })
})

describe('Type', () => {
  it('can be changed while no ticket has a value', async () => {
    const { server } = await renderPage()
    const row = await expand('Asset Tag')
    const type = await within(row).findByLabelText('Type')
    await waitFor(() => expect((type as HTMLSelectElement).disabled).toBe(false))

    await userEvent.selectOptions(type, 'number')
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(server.patch).toHaveBeenCalledWith('/admin/custom-fields/f-asset', { field_type: 'number' }))
  })

  it('is fixed text with the count once tickets hold a value', async () => {
    await renderPage()
    const row = await expand('Department')
    expect(await within(row).findByText('Fixed: 34 tickets have a value for this field.')).toBeTruthy()
    expect(within(row).queryByLabelText('Type')).toBeNull()
  })

  it('says "ticket" for a count of one', async () => {
    fakeServer({ usage: { ...USAGE, 'f-phone': { tickets_with_value: 1, values: [] } } })
    renderWithQuery(<CustomFieldsPage />)
    await userEvent.click(await rowButton('Phone number'))
    expect(await screen.findByText('Fixed: 1 ticket has a value for this field.')).toBeTruthy()
  })
})

describe('Details', () => {
  it('sends only what changed', async () => {
    const { server } = await renderPage()
    const row = await expand('Asset Tag')
    const name = await within(row).findByLabelText('Name')
    await userEvent.clear(name)
    await userEvent.type(name, 'Asset number')
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(server.patch).toHaveBeenCalledWith('/admin/custom-fields/f-asset', { name: 'Asset number' }))
  })

  it('puts the old name back on Cancel', async () => {
    await renderPage()
    const row = await expand('Asset Tag')
    const name = await within(row).findByLabelText('Name')
    await userEvent.type(name, 'xyz')
    await userEvent.click(within(row).getByRole('button', { name: 'Cancel' }))
    expect((within(row).getByLabelText('Name') as HTMLInputElement).value).toBe('Asset Tag')
  })
})

describe('A definition changed elsewhere while its row is open', () => {
  const NOTICE = 'This field was changed elsewhere. Cancel to load the latest.'
  const refetchList = (qc: { invalidateQueries: (o: { queryKey: string[]; exact: boolean }) => Promise<void> }) =>
    qc.invalidateQueries({ queryKey: ['admin', 'custom-fields'], exact: true })

  it('keeps unsaved edits, says so, and lets Save through (last writer wins)', async () => {
    const { server, queryClient } = await renderPage()
    const row = await expand('Asset Tag')
    const name = await within(row).findByLabelText('Name')
    await userEvent.clear(name)
    await userEvent.type(name, 'Asset number')

    server.changeDef('f-asset', { name: 'Asset Tag (EU)' })
    await refetchList(queryClient)

    expect(await within(row).findByText(NOTICE)).toBeTruthy()
    expect((within(row).getByLabelText('Name') as HTMLInputElement).value).toBe('Asset number')

    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(server.patch).toHaveBeenCalledWith('/admin/custom-fields/f-asset', { name: 'Asset number' }))
  })

  it('Cancel loads the latest', async () => {
    const { server, queryClient } = await renderPage()
    const row = await expand('Asset Tag')
    await userEvent.type(await within(row).findByLabelText('Name'), ' x')
    server.changeDef('f-asset', { name: 'Asset Tag (EU)' })
    await refetchList(queryClient)
    await within(row).findByText(NOTICE)

    await userEvent.click(within(row).getByRole('button', { name: 'Cancel' }))
    expect((within(row).getByLabelText('Name') as HTMLInputElement).value).toBe('Asset Tag (EU)')
    expect(within(row).queryByText(NOTICE)).toBeNull()
  })

  it('shows the latest at once when nothing was edited, with no notice', async () => {
    const { server, queryClient } = await renderPage()
    const row = await expand('Asset Tag')
    await within(row).findByLabelText('Name')

    server.changeDef('f-asset', { name: 'Asset Tag (EU)' })
    await refetchList(queryClient)

    await waitFor(() => expect((within(row).getByLabelText('Name') as HTMLInputElement).value).toBe('Asset Tag (EU)'))
    expect(within(row).queryByText(NOTICE)).toBeNull()
  })

  it('does not call an unchanged refetch a change', async () => {
    const { queryClient } = await renderPage()
    const row = await expand('Asset Tag')
    await userEvent.type(await within(row).findByLabelText('Name'), ' x')
    await refetchList(queryClient)
    expect(within(row).queryByText(NOTICE)).toBeNull()
    expect((within(row).getByLabelText('Name') as HTMLInputElement).value).toBe('Asset Tag x')
  })
})

describe('Options', () => {
  async function openDept() {
    const view = await renderPage()
    const row = await expand('Department')
    await within(row).findByText('Fixed: 34 tickets have a value for this field.')
    return { ...view, row }
  }

  it('shows each option with how many tickets hold it', async () => {
    const { row } = await openDept()
    expect(within(row).getByDisplayValue('Finance')).toBeTruthy()
    expect(within(row).getByDisplayValue('Sales, EMEA')).toBeTruthy()
    expect(within(row).getByText('17 tickets')).toBeTruthy()
    expect(within(row).getByText('14 tickets')).toBeTruthy()
  })

  it('offers Delete only on an option no ticket holds', async () => {
    const { row } = await openDept()
    expect(within(row).getByRole('button', { name: 'Delete IT' })).toBeTruthy()
    expect(within(row).queryByRole('button', { name: 'Delete Finance' })).toBeNull()
    expect(within(row).queryByRole('button', { name: 'Delete Sales, EMEA' })).toBeNull()
  })

  it('does not offer Delete on a retired option until its count is known to be 0', async () => {
    const { row } = await openDept()
    await userEvent.click(within(row).getByRole('checkbox', { name: 'Show retired' }))
    expect(within(row).getByRole('button', { name: 'Delete Legal' })).toBeTruthy()
    expect(within(row).queryByRole('button', { name: 'Delete Marketing' })).toBeNull()
    expect(within(row).getByText('3 tickets')).toBeTruthy()
  })

  it('keeps retired options out of sight until Show retired is on', async () => {
    const { row } = await openDept()
    expect(within(row).queryByText('Marketing')).toBeNull()
    await userEvent.click(within(row).getByRole('checkbox', { name: 'Show retired' }))
    expect(within(row).getByText('Marketing')).toBeTruthy()
  })

  it('Retire moves the option to the retired list and Save sends both lists', async () => {
    const { server, row } = await openDept()
    await userEvent.click(within(row).getByRole('checkbox', { name: 'Show retired' }))
    await userEvent.click(within(row).getByRole('button', { name: 'Retire Finance' }))
    expect(within(row).queryByDisplayValue('Finance')).toBeNull()
    expect(within(row).getByRole('button', { name: 'Bring back Finance' })).toBeTruthy()

    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(server.patch).toHaveBeenCalledWith('/admin/custom-fields/f-dept', {
        options: ['Sales, EMEA', 'IT'],
        retired_options: ['Marketing', 'Legal', 'Finance'],
      }),
    )
  })

  it('keeps Show retired on after a Save, so the option just retired stays in view', async () => {
    const { server, row } = await openDept()
    await userEvent.click(within(row).getByRole('checkbox', { name: 'Show retired' }))
    await userEvent.click(within(row).getByRole('button', { name: 'Retire Finance' }))
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(server.patch).toHaveBeenCalled())
    // The saved field comes back and the form is rebuilt from it.
    expect(await within(row).findByRole('button', { name: 'Bring back Finance' })).toBeTruthy()
    await waitFor(() => expect(server.calls.get.filter((u) => u === '/admin/custom-fields').length).toBeGreaterThan(1))
    expect((within(row).getByRole('checkbox', { name: 'Show retired' }) as HTMLInputElement).checked).toBe(true)
    expect(within(row).getByRole('button', { name: 'Bring back Finance' })).toBeTruthy()
  })

  it('Bring back moves a retired option to the end of the options in use', async () => {
    const { server, row } = await openDept()
    await userEvent.click(within(row).getByRole('checkbox', { name: 'Show retired' }))
    await userEvent.click(within(row).getByRole('button', { name: 'Bring back Marketing' }))
    expect(within(row).getByDisplayValue('Marketing')).toBeTruthy()

    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(server.patch).toHaveBeenCalledWith('/admin/custom-fields/f-dept', {
        options: ['Finance', 'Sales, EMEA', 'IT', 'Marketing'],
        retired_options: ['Legal'],
      }),
    )
  })

  it('Delete drops the option from the list Save sends, and from nowhere else', async () => {
    const { server, row } = await openDept()
    await userEvent.click(within(row).getByRole('button', { name: 'Delete IT' }))
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(server.patch).toHaveBeenCalledWith('/admin/custom-fields/f-dept', { options: ['Finance', 'Sales, EMEA'] }),
    )
  })

  it('refuses a blank option inline and sends nothing', async () => {
    const { server, row } = await openDept()
    await userEvent.click(within(row).getByRole('button', { name: 'Add option' }))
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))
    expect((await within(row).findByRole('alert')).textContent).toMatch(/can't be blank/)
    expect(server.patch).not.toHaveBeenCalled()
  })

  it('refuses a duplicate option inline, a retired one included, and sends nothing', async () => {
    const { server, row } = await openDept()
    await userEvent.click(within(row).getByRole('button', { name: 'Add option' }))
    await userEvent.type(within(row).getByLabelText('Option 4'), 'Marketing')
    expect((await within(row).findByRole('alert')).textContent).toMatch(/already an option/i)
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))
    expect(server.patch).not.toHaveBeenCalled()
  })

  it('renaming an option tickets hold retires the old text and adds the new', async () => {
    const { server, row } = await openDept()
    const input = within(row).getByDisplayValue('Finance')
    await userEvent.clear(input)
    await userEvent.type(input, 'Finance and Ops')
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(server.patch).toHaveBeenCalledWith('/admin/custom-fields/f-dept', {
        options: ['Finance and Ops', 'Sales, EMEA', 'IT'],
        retired_options: ['Marketing', 'Legal', 'Finance'],
      }),
    )
  })

  it('renaming an unused option edits it in place', async () => {
    const { server, row } = await openDept()
    const input = within(row).getByDisplayValue('IT')
    await userEvent.clear(input)
    await userEvent.type(input, 'IT Support')
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(server.patch).toHaveBeenCalledWith('/admin/custom-fields/f-dept', { options: ['Finance', 'Sales, EMEA', 'IT Support'] }),
    )
  })

  it('shows a 409 from the delete rule in place and reloads the counts', async () => {
    const { server, row } = await openDept()
    server.fail.patch = refusal(409, 'conflict', 'option "IT" is in use; retire it instead')
    await userEvent.click(within(row).getByRole('button', { name: 'Delete IT' }))
    const before = server.calls.get.filter((u) => u === '/admin/custom-fields/f-dept/usage').length
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))

    expect((await within(row).findByRole('alert')).textContent).toContain('option "IT" is in use; retire it instead')
    await waitFor(() =>
      expect(server.calls.get.filter((u) => u === '/admin/custom-fields/f-dept/usage').length).toBeGreaterThan(before),
    )
  })

  it('puts a refused delete back, with its fresh count, so it can be retired without Cancel', async () => {
    const { server, row } = await openDept()
    // Someone files a ticket with IT after the counts were read.
    server.setUsage('f-dept', {
      tickets_with_value: 35,
      values: [
        { value: 'Finance', tickets: 17 }, { value: 'Sales, EMEA', tickets: 14 }, { value: 'IT', tickets: 1 },
        { value: 'Marketing', tickets: 3 }, { value: 'Legal', tickets: 0 },
      ],
    })
    server.fail.patch = refusal(409, 'conflict', 'option "IT" is in use; retire it instead')
    // Another edit in the same draft must survive the refusal.
    await userEvent.click(within(row).getByRole('button', { name: 'Add option' }))
    await userEvent.type(within(row).getByLabelText('Option 4'), 'Ops')
    await userEvent.click(within(row).getByRole('button', { name: 'Delete IT' }))
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))

    await within(row).findByRole('alert')
    const retire = await within(row).findByRole('button', { name: 'Retire IT' })
    expect(within(row).getByText('1 ticket')).toBeTruthy()
    expect(within(row).queryByRole('button', { name: 'Delete IT' })).toBeNull()
    expect(within(row).getByDisplayValue('Ops')).toBeTruthy()

    // And the retire goes through.
    server.fail.patch = undefined
    await userEvent.click(retire)
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(server.patch).toHaveBeenLastCalledWith('/admin/custom-fields/f-dept', {
        options: ['Finance', 'Sales, EMEA', 'Ops'],
        retired_options: ['Marketing', 'Legal', 'IT'],
      }),
    )
  })
})

// Opens the Add a place form of an expanded row and returns queries inside it.
// A row with no places has it open already; clicking again does not close it.
async function openAddPlace(row: HTMLElement) {
  await userEvent.click(await within(row).findByRole('button', { name: 'Add a place' }))
  return within(await within(row).findByRole('group', { name: 'Add a place' }))
}

describe('Where it appears', () => {
  async function openDept() {
    const view = await renderPage()
    const row = await expand('Department')
    await within(row).findByText('Network › VPN')
    return { ...view, row }
  }

  it('lists each place and describes the effect', async () => {
    const { row } = await openDept()
    expect(within(row).getByText('Hardware')).toBeTruthy()
    expect(
      within(row).getByText('Shown when a ticket is filed under Hardware (any type), required, or under Network › VPN.'),
    ).toBeTruthy()
  })

  it('warns that requesters do not pick items', async () => {
    await renderPage()
    const row = await expand('Phone number')
    await within(row).findByText('Hardware › Laptop › Battery')
    expect(within(row).getByText("Requesters don't pick items, so only staff will see this.")).toBeTruthy()
  })

  it('marks an archived node', async () => {
    fakeServer({
      placements: {
        ...PLACEMENTS,
        'f-asset': [placement('a-9', 'f-asset', { scope_type: 'category', scope_id: 'c-9', category: node('c-9', 'Old stuff', false) })],
      },
    })
    renderWithQuery(<CustomFieldsPage />)
    const row = await (async () => { await userEvent.click(await rowButton('Asset Tag')); return rowOf('Asset Tag') })()
    expect(await within(row).findByText('Archived')).toBeTruthy()
  })

  it('toggling Show on new-ticket form patches the node route for a category', async () => {
    const { server, row } = await openDept()
    const line = within(row).getByText('Hardware').closest('li') as HTMLElement
    await userEvent.click(within(line).getByRole('checkbox', { name: /Show on new-ticket form/ }))
    await waitFor(() => expect(server.patch).toHaveBeenCalledWith('/admin/categories/c-1/fields/a-1', { visible_on_new: false }))
  })

  it('toggling Required patches the node route for a type', async () => {
    const { server, row } = await openDept()
    const line = within(row).getByText('Network › VPN').closest('li') as HTMLElement
    await userEvent.click(within(line).getByRole('checkbox', { name: /Required/ }))
    await waitFor(() =>
      expect(server.patch).toHaveBeenCalledWith('/admin/categories/c-2/types/t-2/fields/a-2', { required_on_new: true }),
    )
  })

  it('disables Required while Show on new-ticket form is off', async () => {
    fakeServer({
      placements: { ...PLACEMENTS, 'f-asset': [placement('a-9', 'f-asset', { scope_type: 'category', scope_id: 'c-1', category: HARDWARE, visible_on_new: false })] },
    })
    renderWithQuery(<CustomFieldsPage />)
    await userEvent.click(await rowButton('Asset Tag'))
    const line = (await screen.findByText('Hardware')).closest('li') as HTMLElement
    expect((within(line).getByRole('checkbox', { name: /Required/ }) as HTMLInputElement).disabled).toBe(true)
    expect((within(line).getByRole('checkbox', { name: /Show on new-ticket form/ }) as HTMLInputElement).checked).toBe(false)
  })

  it('removing an item place deletes through the item route', async () => {
    const { server } = await renderPage()
    const row = await expand('Phone number')
    await within(row).findByText('Hardware › Laptop › Battery')
    await userEvent.click(within(row).getByRole('button', { name: 'Remove Hardware › Laptop › Battery' }))
    await waitFor(() =>
      expect(server.del).toHaveBeenCalledWith('/admin/categories/c-1/types/t-1/items/i-1/fields/a-4'),
    )
  })

  it('adds a type place through the type route, with no attachment sort_order', async () => {
    const { server } = await renderPage()
    const row = await expand('Asset Tag')
    const form = await openAddPlace(row)
    await userEvent.selectOptions(form.getByLabelText('Category'), 'c-2')
    await userEvent.selectOptions(await form.findByLabelText('Type'), 't-3')
    await userEvent.click(form.getByRole('button', { name: 'Add' }))
    await waitFor(() =>
      expect(server.post).toHaveBeenCalledWith('/admin/categories/c-2/types/t-3/fields', {
        field_def_id: 'f-asset', visible_on_new: true, required_on_new: false,
      }),
    )
  })

  it('adds a whole-category place through the category route, Required ticked', async () => {
    const { server } = await renderPage()
    const row = await expand('Asset Tag')
    const form = await openAddPlace(row)
    await userEvent.selectOptions(form.getByLabelText('Category'), 'c-3')
    await userEvent.click(form.getByRole('checkbox', { name: 'Required' }))
    await userEvent.click(form.getByRole('button', { name: 'Add' }))
    await waitFor(() =>
      expect(server.post).toHaveBeenCalledWith('/admin/categories/c-3/fields', {
        field_def_id: 'f-asset', visible_on_new: true, required_on_new: true,
      }),
    )
  })

  it('adds an item place through the item route', async () => {
    const { server } = await renderPage()
    const row = await expand('Asset Tag')
    const form = await openAddPlace(row)
    await userEvent.selectOptions(form.getByLabelText('Category'), 'c-1')
    await userEvent.selectOptions(await form.findByLabelText('Type'), 't-1')
    await userEvent.selectOptions(await form.findByLabelText('Item'), 'i-2')
    await userEvent.click(form.getByRole('button', { name: 'Add' }))
    await waitFor(() =>
      expect(server.post).toHaveBeenCalledWith('/admin/categories/c-1/types/t-1/items/i-2/fields', {
        field_def_id: 'f-asset', visible_on_new: true, required_on_new: false,
      }),
    )
  })

  it('only shows Item once a type is chosen', async () => {
    await renderPage()
    const row = await expand('Asset Tag')
    const form = await openAddPlace(row)
    await userEvent.selectOptions(form.getByLabelText('Category'), 'c-1')
    expect(form.queryByLabelText('Item')).toBeNull()
    await userEvent.selectOptions(await form.findByLabelText('Type'), 't-1')
    expect(await form.findByLabelText('Item')).toBeTruthy()
  })

  it('disables a place that is already taken, and says why', async () => {
    await renderPage()
    const row = await expand('Department')
    await within(row).findByText('Network › VPN')
    const form = await openAddPlace(row)

    // Attached to the whole of Hardware: nothing under it can be added.
    const hardware = await form.findByRole('option', { name: /^Hardware/ })
    expect((hardware as HTMLOptionElement).disabled).toBe(true)
    expect(hardware.textContent).toMatch(/already attached/)

    await userEvent.selectOptions(form.getByLabelText('Category'), 'c-2')
    // Network › VPN is taken; WiFi is not.
    const vpn = await form.findByRole('option', { name: /^VPN/ })
    expect((vpn as HTMLOptionElement).disabled).toBe(true)
    expect(vpn.textContent).toMatch(/already attached/)
    expect((form.getByRole('option', { name: 'WiFi' }) as HTMLOptionElement).disabled).toBe(false)
    // The whole of Network would cover Network › VPN.
    const whole = form.getByRole('option', { name: /^Whole category/ })
    expect((whole as HTMLOptionElement).disabled).toBe(true)
    expect(whole.textContent).toMatch(/overlaps/)
  })

  it('does not offer Add for a place that is not chosen or would overlap', async () => {
    const { server } = await renderPage()
    const row = await expand('Department')
    await within(row).findByText('Network › VPN')
    const form = await openAddPlace(row)
    await userEvent.selectOptions(form.getByLabelText('Category'), 'c-2')
    // "Whole category" is the default and it overlaps VPN.
    expect((form.getByRole('button', { name: 'Add' }) as HTMLButtonElement).disabled).toBe(true)
    expect(server.post).not.toHaveBeenCalled()
  })

  it('notes an overlap that already exists, and which place wins', async () => {
    fakeServer({
      placements: {
        ...PLACEMENTS,
        'f-asset': [
          placement('a-7', 'f-asset', { scope_type: 'category', scope_id: 'c-1', category: HARDWARE }),
          placement('a-8', 'f-asset', { scope_type: 'type', scope_id: 't-1', category: HARDWARE, type: node('t-1', 'Laptop') }),
        ],
      },
    })
    renderWithQuery(<CustomFieldsPage />)
    await userEvent.click(await rowButton('Asset Tag'))
    const line = (await screen.findByText('Hardware › Laptop')).closest('li')!.parentElement as HTMLElement
    expect(within(line).getByText(/Overlaps Hardware › Laptop/)).toBeTruthy()
  })
})

describe('Reorder', () => {
  it('sends every field, active and inactive, with hidden fields keeping their place', async () => {
    const { server } = await renderPage()
    await userEvent.click(screen.getByRole('button', { name: 'stub: move last to first' }))
    // Visible: Department, Phone number, Asset Tag -> Asset Tag, Department, Phone number.
    // The hidden Old cost centre stays second.
    await waitFor(() =>
      expect(server.put).toHaveBeenCalledWith('/admin/custom-fields/order', {
        field_def_ids: ['f-asset', 'f-old', 'f-dept', 'f-phone'],
      }),
    )
  })

  it('puts the old order back and shows the error when the save fails', async () => {
    const { server } = await renderPage()
    server.fail.put = refusal(409, 'conflict', 'the field list has changed; reload and try again')
    // Hold the refetch that follows a failed save, so what is on screen is the
    // page's own rollback and not the server's list coming back.
    let release: () => void = () => {}
    server.hold.list = new Promise<void>((r) => { release = r })
    await userEvent.click(screen.getByRole('button', { name: 'stub: move last to first' }))

    expect((await screen.findByRole('alert')).textContent).toContain('the field list has changed; reload and try again')
    const names = screen.getAllByRole('button', { name: /^Reorder / }).map((b) => b.getAttribute('aria-label'))
    expect(names).toEqual(['Reorder Department', 'Reorder Phone number', 'Reorder Asset Tag'])
    release()
  })
})
