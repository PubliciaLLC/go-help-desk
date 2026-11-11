import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The Fields subsection words its checkboxes the way the Custom Fields page
// does, and, like it, disables Required while the field is not shown.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/categories' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, search, children, ...rest }: any) => (
    <a href={search ? `${to}?${new URLSearchParams(search).toString()}` : to} {...rest}>
      {children}
    </a>
  ),
}))

import { CategoriesPage } from './CategoriesPage'

function assignment(id: string, name: string, visible: boolean, required: boolean) {
  return {
    id: `a-${id}`, field_def_id: `f-${id}`, scope_type: 'category', scope_id: 'c-1', sort_order: 1,
    visible_on_new: visible, required_on_new: required,
    field_def: { id: `f-${id}`, name, field_type: 'text', sort_order: 1, active: true, created_at: '' },
  }
}

function mockApi(assignments: object[]) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/categories') return Promise.resolve({ data: [{ id: 'c-1', name: 'Hardware', sort_order: 1, active: true }] })
    if (url === '/admin/categories/c-1/fields') return Promise.resolve({ data: assignments })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
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

async function openCategory() {
  const row = (await screen.findByText('Hardware')).closest('[data-archived]') as HTMLElement
  await userEvent.click(within(row).getByRole('button', { name: 'Expand' }))
}

describe('Categories page: Fields subsection labels', () => {
  it('words the checkboxes like the Custom Fields page', async () => {
    mockApi([assignment('1', 'Region', true, false)])
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    await screen.findByRole('link', { name: 'Region' })
    expect(screen.getByText('Show on new-ticket form')).toBeTruthy()
    expect(screen.getByText('Required')).toBeTruthy()
    expect(screen.queryByText('visible')).toBeNull()
    expect(screen.queryByText('required')).toBeNull()
  })

  it('names each checkbox for its field, so a screen reader can tell the rows apart', async () => {
    mockApi([assignment('1', 'Region', true, false), assignment('2', 'Asset tag', true, false)])
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    expect(await screen.findByRole('checkbox', { name: 'Show on new-ticket form for Region' })).toBeTruthy()
    expect(screen.getByRole('checkbox', { name: 'Required for Region' })).toBeTruthy()
    expect(screen.getByRole('checkbox', { name: 'Show on new-ticket form for Asset tag' })).toBeTruthy()
    expect(screen.getByRole('checkbox', { name: 'Required for Asset tag' })).toBeTruthy()
  })

  it('makes each label a 44px target on a coarse pointer', async () => {
    mockApi([assignment('1', 'Region', true, false)])
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    for (const name of ['Show on new-ticket form for Region', 'Required for Region']) {
      const label = (await screen.findByRole('checkbox', { name })).closest('label') as HTMLElement
      expect(label.className).toContain('pointer-coarse:min-h-11')
      expect(label.className).toContain('pointer-coarse:min-w-11')
    }
  })

  it('disables Required while the field is not shown', async () => {
    mockApi([assignment('1', 'Region', false, false)])
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    const required = (await screen.findByRole('checkbox', { name: 'Required for Region' })) as HTMLInputElement
    expect(required.disabled).toBe(true)
    expect((screen.getByRole('checkbox', { name: 'Show on new-ticket form for Region' }) as HTMLInputElement).disabled).toBe(false)
  })

  it('leaves Required enabled while the field is shown, and saves it', async () => {
    mockApi([assignment('1', 'Region', true, false)])
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    const required = (await screen.findByRole('checkbox', { name: 'Required for Region' })) as HTMLInputElement
    expect(required.disabled).toBe(false)
    await userEvent.click(required)
    await waitFor(() => expect(patch).toHaveBeenCalledWith('/admin/categories/c-1/fields/a-1', { required_on_new: true }))
  })
})

describe('Categories page: field name link', () => {
  it('is a link to that field on the Custom Fields page', async () => {
    mockApi([assignment('1', 'Region', true, false)])
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    const link = await screen.findByRole('link', { name: 'Region' })
    expect(link.getAttribute('href')).toBe('/admin/custom-fields?field=f-1')
  })

  it('looks like a link and shows where keyboard focus is', async () => {
    mockApi([assignment('1', 'Region', true, false)])
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    const cls = (await screen.findByRole('link', { name: 'Region' })).className
    // Same classes as the other in-app links on the admin pages.
    expect(cls).toContain('text-blue-600')
    expect(cls).toContain('hover:underline')
    expect(cls).toContain('focus-visible:ring-2')
  })
})
