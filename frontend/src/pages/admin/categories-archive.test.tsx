import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, AxiosHeaders } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #446: an administrator can archive and restore a category, type or item from
// the page. The API already took `active`; the page had a badge and no control.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/categories' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { CategoriesPage } from './CategoriesPage'

const CAT = { id: 'c-1', name: 'Hardware', sort_order: 1, active: true }
const TYPE = { id: 't-1', category_id: 'c-1', name: 'Laptop', sort_order: 1, active: true }
const ITEM = { id: 'i-1', type_id: 't-1', name: 'Battery', sort_order: 1, active: true }

function refusal(status: number, code: string, message: string) {
  const headers = new AxiosHeaders()
  return new AxiosError('Request failed', 'ERR_BAD_REQUEST', { headers } as never, null, {
    status,
    statusText: 'x',
    headers,
    config: { headers } as never,
    data: { error: { code, message } },
  } as never)
}

function mockApi(over: { cat?: object; type?: object; item?: object } = {}) {
  const get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/categories') return Promise.resolve({ data: [{ ...CAT, ...over.cat }] })
    if (url === '/admin/categories/c-1/types') return Promise.resolve({ data: [{ ...TYPE, ...over.type }] })
    if (url === '/admin/categories/c-1/types/t-1/items') return Promise.resolve({ data: [{ ...ITEM, ...over.item }] })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
  return { get, patch }
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'a@example.com', display_name: 'Ada', role: 'admin', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

// The row element carrying the greyed state, found from the entry's name.
async function rowOf(name: string) {
  const row = (await screen.findByText(name)).closest('[data-archived]')
  expect(row).not.toBeNull()
  return row as HTMLElement
}

async function expandAll() {
  await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Expand' }))
  await userEvent.click(within(await rowOf('Laptop')).getByRole('button', { name: 'Expand' }))
  await screen.findByText('Battery')
}

describe('archiving', () => {
  it('asks first, explains the effect, then sends active=false for a category', async () => {
    const { patch } = mockApi()
    renderWithQuery(<CategoriesPage />)

    await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Archive category' }))
    const dialog = await screen.findByRole('dialog')
    expect(
      within(dialog).getByText(
        'Requesters will no longer see it or be able to file new tickets under it. Tickets already filed keep it. You can restore it at any time.',
      ),
    ).toBeTruthy()
    expect(patch).not.toHaveBeenCalled()

    await userEvent.click(within(dialog).getByRole('button', { name: 'Archive category' }))
    await waitFor(() => expect(patch).toHaveBeenCalledWith('/admin/categories/c-1', { active: false }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('cancelling the dialog sends nothing', async () => {
    const { patch } = mockApi()
    renderWithQuery(<CategoriesPage />)

    await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Archive category' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Cancel' }))

    expect(patch).not.toHaveBeenCalled()
  })

  it('archives a type and an item at their own URLs', async () => {
    const { patch } = mockApi()
    renderWithQuery(<CategoriesPage />)
    await expandAll()

    await userEvent.click(within(await rowOf('Laptop')).getByRole('button', { name: 'Archive type' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Archive type' }))
    await waitFor(() => expect(patch).toHaveBeenCalledWith('/admin/categories/c-1/types/t-1', { active: false }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await userEvent.click(within(await rowOf('Battery')).getByRole('button', { name: 'Archive item' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Archive item' }))
    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith('/admin/categories/c-1/types/t-1/items/i-1', { active: false }),
    )
  })
})

describe('archived entries', () => {
  it('are greyed and labelled Archived, and offer Restore instead of Archive', async () => {
    mockApi({ cat: { active: false }, type: { active: false }, item: { active: false } })
    renderWithQuery(<CategoriesPage />)
    await expandAll()

    for (const [name, kind] of [['Hardware', 'category'], ['Laptop', 'type'], ['Battery', 'item']] as const) {
      const row = await rowOf(name)
      expect(row.getAttribute('data-archived')).toBe('true')
      expect(within(row).getByText('Archived')).toBeTruthy()
      // The name is what fades; the row and its label stay at full strength.
      expect(within(row).getByText(name).parentElement?.className).toContain('opacity-60')
      expect(row.className).not.toContain('opacity-60')
      expect(within(row).getByText('Archived').className).not.toContain('opacity-60')
      expect(within(row).getByRole('button', { name: `Restore ${kind}` })).toBeTruthy()
      expect(within(row).queryByRole('button', { name: `Archive ${kind}` })).toBeNull()
    }
  })

  it('active entries are not greyed and carry no label', async () => {
    mockApi()
    renderWithQuery(<CategoriesPage />)

    const row = await rowOf('Hardware')
    expect(row.getAttribute('data-archived')).toBe('false')
    expect(within(row).getByText('Hardware').parentElement?.className).not.toContain('opacity-60')
    expect(within(row).queryByText('Archived')).toBeNull()
  })

  it('restore sends active=true straight away, without a dialog', async () => {
    const { patch } = mockApi({ cat: { active: false } })
    renderWithQuery(<CategoriesPage />)

    await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Restore category' }))

    await waitFor(() => expect(patch).toHaveBeenCalledWith('/admin/categories/c-1', { active: true }))
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('restores a type and an item at their own URLs', async () => {
    const { patch } = mockApi({ type: { active: false }, item: { active: false } })
    renderWithQuery(<CategoriesPage />)
    await expandAll()

    await userEvent.click(within(await rowOf('Laptop')).getByRole('button', { name: 'Restore type' }))
    await waitFor(() => expect(patch).toHaveBeenCalledWith('/admin/categories/c-1/types/t-1', { active: true }))
    await userEvent.click(within(await rowOf('Battery')).getByRole('button', { name: 'Restore item' }))
    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith('/admin/categories/c-1/types/t-1/items/i-1', { active: true }),
    )
  })
})

describe('a failed restore', () => {
  it('shows the server message inline', async () => {
    const { patch } = mockApi({ cat: { active: false } })
    patch.mockRejectedValue(refusal(500, 'internal_error', 'database unavailable'))
    renderWithQuery(<CategoriesPage />)

    const row = await rowOf('Hardware')
    await userEvent.click(within(row).getByRole('button', { name: 'Restore category' }))

    expect((await within(row).findByRole('alert')).textContent).toBe('database unavailable')
  })
})

describe('keyboard access', () => {
  it('archive, restore and delete buttons are focusable and show on focus', async () => {
    mockApi({ type: { active: false } })
    renderWithQuery(<CategoriesPage />)
    await expandAll()

    const buttons = [
      within(await rowOf('Hardware')).getByRole('button', { name: 'Archive category' }),
      within(await rowOf('Hardware')).getByRole('button', { name: 'Delete category' }),
      within(await rowOf('Laptop')).getByRole('button', { name: 'Restore type' }),
      within(await rowOf('Laptop')).getByRole('button', { name: 'Delete type' }),
      within(await rowOf('Battery')).getByRole('button', { name: 'Archive item' }),
      within(await rowOf('Battery')).getByRole('button', { name: 'Delete item' }),
    ]
    for (const b of buttons) {
      // visibility:hidden would take the button out of the tab order.
      expect(b.className).not.toMatch(/(^|\s)invisible(\s|$)/)
      expect(b.className).toContain('focus-visible:opacity-100')
      b.focus()
      expect(document.activeElement).toBe(b)
    }
  })
})

describe('deleting an entry that is in use', () => {
  it('suggests archiving a category', async () => {
    mockApi()
    vi.spyOn(api, 'delete').mockRejectedValue(refusal(409, 'category_in_use', 'category is in use'))
    renderWithQuery(<CategoriesPage />)

    await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Delete category' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete category' }))

    expect(await within(dialog).findByText(/category is in use/)).toBeTruthy()
    expect(within(dialog).getByText(/archive it instead/i)).toBeTruthy()
  })

  it('suggests archiving a type and an item too', async () => {
    mockApi()
    const del = vi.spyOn(api, 'delete')
    renderWithQuery(<CategoriesPage />)
    await expandAll()

    for (const [name, kind, code] of [['Laptop', 'type', 'type_in_use'], ['Battery', 'item', 'item_in_use']] as const) {
      del.mockRejectedValue(refusal(409, code, `${kind} is in use`))
      await userEvent.click(within(await rowOf(name)).getByRole('button', { name: `Delete ${kind}` }))
      const dialog = await screen.findByRole('dialog')
      await userEvent.click(within(dialog).getByRole('button', { name: `Delete ${kind}` }))
      expect(await within(dialog).findByText(/archive it instead/i)).toBeTruthy()
      await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    }
  })

  it('does not suggest archiving for an unrelated failure', async () => {
    mockApi()
    vi.spyOn(api, 'delete').mockRejectedValue(refusal(500, 'internal_error', 'database unavailable'))
    renderWithQuery(<CategoriesPage />)

    await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Delete category' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete category' }))

    expect(await within(dialog).findByText('database unavailable')).toBeTruthy()
    expect(within(dialog).queryByText(/archive it instead/i)).toBeNull()
  })
})
