import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// An optional Description on a category, a type and an item, on create and on
// edit (docs/DESIGN.md, Ticket Classification -> Descriptions).

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

const CAT = { id: 'c-1', name: 'Hardware', description: 'Category text', sort_order: 1, active: true }
const TYPE = { id: 't-1', category_id: 'c-1', name: 'Laptop', description: 'Type text', sort_order: 1, active: true }
const ITEM = { id: 'i-1', type_id: 't-1', name: 'Battery', description: 'Item text', sort_order: 1, active: true }

function mockApi() {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/categories') return Promise.resolve({ data: [CAT] })
    if (url === '/admin/categories/c-1/types') return Promise.resolve({ data: [TYPE] })
    if (url === '/admin/categories/c-1/types/t-1/items') return Promise.resolve({ data: [ITEM] })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {} } as any)
  return { patch, post }
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

async function rowOf(name: string) {
  const row = (await screen.findByText(name)).closest('[data-archived]')
  expect(row).not.toBeNull()
  return row as HTMLElement
}

async function expandAll() {
  await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Expand' }))
  await userEvent.click(within(await rowOf('Laptop')).getByRole('button', { name: 'Expand' }))
  await screen.findByText('Battery')
  await userEvent.click(within(await rowOf('Battery')).getByRole('button', { name: 'Expand' }))
}

const boxes = () => screen.getAllByLabelText(/^description/i) as HTMLTextAreaElement[]

describe('editing a description', () => {
  it('has a field at all three levels, with what is stored', async () => {
    mockApi()
    renderWithQuery(<CategoriesPage />)
    await expandAll()
    await waitFor(() => expect(boxes()).toHaveLength(3))
    expect(boxes().map((b) => b.value)).toEqual(['Category text', 'Type text', 'Item text'])
  })

  it.each([
    { level: 0, url: '/admin/categories/c-1' },
    { level: 1, url: '/admin/categories/c-1/types/t-1' },
    { level: 2, url: '/admin/categories/c-1/types/t-1/items/i-1' },
  ])('saves a changed description to the node itself (level $level)', async ({ level, url }) => {
    const { patch } = mockApi()
    renderWithQuery(<CategoriesPage />)
    await expandAll()
    await waitFor(() => expect(boxes()).toHaveLength(3))

    const box = boxes()[level]
    await userEvent.clear(box)
    await userEvent.type(box, 'New <b>text</b>')
    await userEvent.click(within(box.closest('form') as HTMLElement).getByRole('button', { name: /save description/i }))

    await waitFor(() => expect(patch).toHaveBeenCalledWith(url, { description: 'New <b>text</b>' }))
  })

  it('clearing the field saves an empty description', async () => {
    const { patch } = mockApi()
    renderWithQuery(<CategoriesPage />)
    await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Expand' }))
    const box = (await screen.findByLabelText(/^description/i)) as HTMLTextAreaElement
    await userEvent.clear(box)
    await userEvent.click(screen.getByRole('button', { name: /save description/i }))
    await waitFor(() => expect(patch).toHaveBeenCalledWith('/admin/categories/c-1', { description: '' }))
  })

  it('does not offer Save until something changed', async () => {
    mockApi()
    renderWithQuery(<CategoriesPage />)
    await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Expand' }))
    const save = await screen.findByRole('button', { name: /save description/i })
    expect((save as HTMLButtonElement).disabled).toBe(true)
    await userEvent.type(screen.getByLabelText(/^description/i), '!')
    expect((save as HTMLButtonElement).disabled).toBe(false)
  })

  it('counts characters and will not save past 500', async () => {
    const { patch } = mockApi()
    renderWithQuery(<CategoriesPage />)
    await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Expand' }))
    const box = (await screen.findByLabelText(/^description/i)) as HTMLTextAreaElement
    expect(screen.getByText('13 / 500')).toBeDefined()

    await userEvent.clear(box)
    await userEvent.click(box)
    await userEvent.paste('😀'.repeat(501))
    expect(screen.getByText('501 / 500')).toBeDefined()
    expect(screen.getByRole('alert').textContent).toMatch(/too long/i)
    const save = screen.getByRole('button', { name: /save description/i }) as HTMLButtonElement
    expect(save.disabled).toBe(true)
    await userEvent.click(save)
    expect(patch).not.toHaveBeenCalled()
  })

  it("shows the server's refusal", async () => {
    const { patch } = mockApi()
    patch.mockRejectedValueOnce(
      Object.assign(new Error('x'), { isAxiosError: true, response: { status: 400, data: { error: { code: 'bad_request', message: 'description is too long' } } } }),
    )
    renderWithQuery(<CategoriesPage />)
    await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Expand' }))
    await userEvent.type(await screen.findByLabelText(/^description/i), '!')
    await userEvent.click(screen.getByRole('button', { name: /save description/i }))
    expect((await screen.findByRole('alert')).textContent).toMatch(/too long/)
  })
})

describe('creating with a description', () => {
  it('a new category sends it, and may leave it empty', async () => {
    const { post } = mockApi()
    renderWithQuery(<CategoriesPage />)
    await userEvent.click(await screen.findByRole('button', { name: /new category/i }))
    await userEvent.type(screen.getByPlaceholderText('Category name'), 'Network')
    await userEvent.type(screen.getByLabelText(/^description/i), 'Wired and wireless.')
    await userEvent.click(screen.getByRole('button', { name: 'Add' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/admin/categories', { name: 'Network', description: 'Wired and wireless.', sort_order: 2 }),
    )
  })

  it('a new type and a new item send theirs', async () => {
    const { post } = mockApi()
    renderWithQuery(<CategoriesPage />)
    await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Expand' }))
    await screen.findByText('Laptop')
    await userEvent.click(screen.getByRole('button', { name: /add type/i }))
    await userEvent.type(screen.getByPlaceholderText('Type name'), 'Desktop')
    // the editor of the category is the first box; the new one is the last
    const typeBoxes = screen.getAllByLabelText(/^description/i)
    await userEvent.type(typeBoxes[typeBoxes.length - 1], 'Towers.')
    await userEvent.click(screen.getByRole('button', { name: 'Add' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/admin/categories/c-1/types', { name: 'Desktop', description: 'Towers.', sort_order: 2 }),
    )

    await userEvent.click(within(await rowOf('Laptop')).getByRole('button', { name: 'Expand' }))
    await screen.findByText('Battery')
    await userEvent.click(screen.getByRole('button', { name: /add item/i }))
    await userEvent.type(screen.getByPlaceholderText('Item name'), 'Charger')
    const itemBoxes = screen.getAllByLabelText(/^description/i)
    await userEvent.type(itemBoxes[itemBoxes.length - 1], 'Mains.')
    await userEvent.click(screen.getByRole('button', { name: 'Add' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/admin/categories/c-1/types/t-1/items', { name: 'Charger', description: 'Mains.', sort_order: 2 }),
    )
  })

  it('will not send one over 500 characters', async () => {
    const { post } = mockApi()
    renderWithQuery(<CategoriesPage />)
    await userEvent.click(await screen.findByRole('button', { name: /new category/i }))
    await userEvent.type(screen.getByPlaceholderText('Category name'), 'Network')
    const box = screen.getByLabelText(/^description/i)
    await userEvent.click(box)
    await userEvent.paste('a'.repeat(501))
    const add = screen.getByRole('button', { name: 'Add' }) as HTMLButtonElement
    expect(add.disabled).toBe(true)
    await userEvent.click(add)
    expect(post).not.toHaveBeenCalled()
  })

  // The ticket forms in this browser read the public lists, which hold the text.
  it.each([
    { what: 'category', key: 'public-categories' },
    { what: 'type', key: 'public-types' },
    { what: 'item', key: 'public-items' },
  ])('a new $what refreshes the public list the forms read', async ({ what, key }) => {
    mockApi()
    const { queryClient } = renderWithQuery(<CategoriesPage />)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    if (what === 'category') {
      await userEvent.click(await screen.findByRole('button', { name: /new category/i }))
      await userEvent.type(screen.getByPlaceholderText('Category name'), 'Network')
    } else {
      await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Expand' }))
      await screen.findByText('Laptop')
      if (what === 'type') {
        await userEvent.click(screen.getByRole('button', { name: /add type/i }))
        await userEvent.type(screen.getByPlaceholderText('Type name'), 'Desktop')
      } else {
        await userEvent.click(within(await rowOf('Laptop')).getByRole('button', { name: 'Expand' }))
        await screen.findByText('Battery')
        await userEvent.click(screen.getByRole('button', { name: /add item/i }))
        await userEvent.type(screen.getByPlaceholderText('Item name'), 'Charger')
      }
    }
    await userEvent.click(screen.getByRole('button', { name: 'Add' }))
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: [key] }))
  })
})

describe('the edit box follows the row', () => {
  it('shows another administrator’s edit when the row is refetched and nothing here was typed', async () => {
    mockApi()
    const { queryClient } = renderWithQuery(<CategoriesPage />)
    await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Expand' }))
    const box = (await screen.findByLabelText(/^description/i)) as HTMLTextAreaElement
    expect(box.value).toBe('Category text')

    queryClient.setQueryData(['admin', 'categories'], [{ ...CAT, description: 'Edited elsewhere' }])
    await waitFor(() => expect(box.value).toBe('Edited elsewhere'))
  })

  it('keeps what is being typed when the row changes under it', async () => {
    mockApi()
    const { queryClient } = renderWithQuery(<CategoriesPage />)
    await userEvent.click(within(await rowOf('Hardware')).getByRole('button', { name: 'Expand' }))
    const box = (await screen.findByLabelText(/^description/i)) as HTMLTextAreaElement
    await userEvent.type(box, ' plus mine')

    queryClient.setQueryData(['admin', 'categories'], [{ ...CAT, description: 'Edited elsewhere' }])
    await screen.findByText('Hardware')
    expect(box.value).toBe('Category text plus mine')
  })
})
