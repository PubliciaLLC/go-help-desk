import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, within, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { User } from '@/api/types'

// The admin sidebar groups its 14 pages into four collapsible sections. The
// pages and URLs are unchanged; only where the links sit is.

const router = vi.hoisted(() => ({ pathname: '/dashboard' }))

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: router.pathname } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { Layout } from './Layout'

const STORAGE_KEY = 'ghd.admin-nav-open'

const SECTIONS = [
  { name: 'People', links: [['Users', '/admin/users'], ['Groups', '/admin/groups'], ['Roles', '/admin/roles']] },
  {
    name: 'Tickets',
    links: [
      ['Categories', '/admin/categories'], ['Statuses', '/admin/statuses'], ['Tags', '/admin/tags'],
      ['Custom Fields', '/admin/custom-fields'], ['Canned Responses', '/admin/canned-responses'], ['Routing', '/admin/routing'],
    ],
  },
  {
    name: 'Integrations',
    links: [
      ['API Keys', '/admin/api-keys'], ['OAuth Clients', '/admin/oauth-clients'],
      ['Webhooks', '/admin/webhooks'], ['Email Templates', '/admin/email-templates'],
    ],
  },
  { name: 'System', links: [['Settings', '/admin/settings'], ['Audit Log', '/admin/audit']] },
] as const

function signInAs(role: User['role']) {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'someone@example.com', display_name: 'Someone', role, mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

function renderAt(pathname: string, role: User['role'] = 'admin') {
  router.pathname = pathname
  signInAs(role)
  return renderWithQuery(<Layout>content</Layout>)
}

const heading = (name: string) => screen.getByRole('button', { name })
const isOpen = (name: string) => heading(name).getAttribute('aria-expanded') === 'true'
const openSections = () => SECTIONS.filter((s) => isOpen(s.name)).map((s) => s.name)

// A closed panel stays in the document (aria-controls must resolve) but is
// hidden, so it is read by id rather than through the accessibility tree.
function panelOf(name: string) {
  const id = heading(name).getAttribute('aria-controls')
  const panel = id ? document.getElementById(id) : null
  if (!panel) throw new Error(`no panel for ${name}`)
  return panel
}
const linksIn = (panel: HTMLElement) =>
  within(panel).queryAllByRole('link', { hidden: true }).map((a) => [a.textContent, a.getAttribute('href')])

let getItem: ReturnType<typeof vi.spyOn> | undefined

beforeEach(() => {
  vi.restoreAllMocks()
  localStorage.clear()
  getItem = undefined
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(api, 'get').mockImplementation((() => Promise.resolve({ data: {} })) as any)
})

afterEach(() => {
  router.pathname = '/dashboard'
})

describe('structure', () => {
  it('has four section headings, in order, each controlling the panel that holds its links', () => {
    renderAt('/dashboard')

    const buttons = screen.getAllByRole('button').filter((b) => b.hasAttribute('aria-controls'))
    expect(buttons.map((b) => b.textContent)).toEqual(SECTIONS.map((s) => s.name))
    for (const s of SECTIONS) {
      expect(heading(s.name).getAttribute('aria-expanded')).not.toBeNull()
      expect(linksIn(panelOf(s.name))).toEqual(s.links.map(([label, href]) => [label, href]))
    }
  })

  // The 14 admin pages plus Audit Log, which used to sit above them.
  it('keeps all 15 admin pages at the same URLs', () => {
    renderAt('/dashboard')
    const all = SECTIONS.flatMap((s) => linksIn(panelOf(s.name)))
    expect(all).toHaveLength(15)
    expect(all.map(([, href]) => href).sort()).toEqual(
      SECTIONS.flatMap((s) => s.links.map(([, href]) => href)).sort(),
    )
  })

  it('opens and closes a panel with its heading', async () => {
    const user = userEvent.setup()
    renderAt('/dashboard')
    expect(panelOf('People').hidden).toBe(true)

    await user.click(heading('People'))
    expect(isOpen('People')).toBe(true)
    expect(panelOf('People').hidden).toBe(false)
    expect(screen.getByRole('link', { name: 'Users' })).toBeTruthy()

    await user.click(heading('People'))
    expect(isOpen('People')).toBe(false)
    expect(screen.queryByRole('link', { name: 'Users' })).toBeNull()
  })
})

describe('what is open', () => {
  it('is nothing on a page outside the admin sections', () => {
    renderAt('/dashboard')
    expect(openSections()).toEqual([])
  })

  it.each(SECTIONS.flatMap((s) => s.links.map(([label, href]) => [s.name, label, href] as const)))(
    'is only %s when the page is %s (%s)',
    (section, _label, href) => {
      renderAt(href)
      expect(openSections()).toEqual([section])
    },
  )

  it('follows a sub-page of an admin page', () => {
    renderAt('/admin/users/u-123')
    expect(openSections()).toEqual(['People'])
  })

  it('opens the section of the page navigated to, when it is a different one', () => {
    const { rerender } = renderAt('/dashboard')
    expect(openSections()).toEqual([])

    router.pathname = '/admin/tags'
    rerender(<Layout>content</Layout>)
    expect(openSections()).toEqual(['Tickets'])

    router.pathname = '/admin/webhooks'
    rerender(<Layout>content</Layout>)
    expect(openSections()).toEqual(['Integrations'])
  })

  it('opens it again when a page links to another page of a section the visitor closed', async () => {
    const user = userEvent.setup()
    const { rerender } = renderAt('/admin/users')
    await user.click(heading('People'))
    expect(isOpen('People')).toBe(false)

    router.pathname = '/admin/groups'
    rerender(<Layout>content</Layout>)
    expect(isOpen('People')).toBe(true)
  })

  it('lets the visitor close the current section and leaves it closed until the next navigation', async () => {
    const user = userEvent.setup()
    renderAt('/admin/users')
    await user.click(heading('People'))
    await user.click(heading('System'))
    await user.click(heading('System'))
    expect(openSections()).toEqual([])
  })
})

describe('keyboard', () => {
  it('reaches every heading with Tab, and Enter and Space toggle it', async () => {
    const user = userEvent.setup()
    renderAt('/dashboard')

    const seen: string[] = []
    for (let i = 0; i < 40 && seen.length < 4; i++) {
      await user.tab()
      const el = document.activeElement
      if (el?.hasAttribute('aria-controls')) seen.push(el.textContent ?? '')
    }
    expect(seen).toEqual(SECTIONS.map((s) => s.name))

    await user.click(document.body)
    heading('People').focus()
    await user.keyboard('{Enter}')
    expect(isOpen('People')).toBe(true)
    await user.keyboard(' ')
    expect(isOpen('People')).toBe(false)
  })

  it('does not put a closed section\'s links in the tab order', async () => {
    const user = userEvent.setup()
    renderAt('/dashboard')
    for (let i = 0; i < 30; i++) {
      await user.tab()
      expect(document.activeElement?.getAttribute('href') ?? '').not.toMatch(/^\/admin\//)
    }
  })
})

describe('visibility by role', () => {
  const linkNames = () => screen.queryAllByRole('link').map((a) => a.textContent)

  it('admin: all 15 admin pages are reachable and Audit Log is in System, not above the sections', async () => {
    const user = userEvent.setup()
    renderAt('/dashboard')
    for (const s of SECTIONS) await user.click(heading(s.name))

    const names = linkNames()
    for (const s of SECTIONS) for (const [label] of s.links) expect(names.filter((n) => n === label), label).toHaveLength(1)
    expect(within(panelOf('System')).getByRole('link', { name: 'Audit Log' }).getAttribute('href')).toBe('/admin/audit')
    expect(names.slice(0, 2)).toEqual(['Dashboard', 'Tickets'])
  })

  it('staff: Dashboard, Tickets and Audit Log in the order they always had, and no sections', () => {
    renderAt('/dashboard', 'staff')

    expect(screen.queryAllByRole('link', { hidden: true }).map((a) => [a.textContent, a.getAttribute('href')])).toEqual([
      ['Dashboard', '/dashboard'],
      ['Tickets', '/tickets'],
      ['Audit Log', '/admin/audit'],
      ['Your account', '/account'],
    ])
    for (const s of SECTIONS) expect(screen.queryByRole('button', { name: s.name })).toBeNull()
  })

  it('staff on the Audit Log page: still no sections', () => {
    renderAt('/admin/audit', 'staff')
    expect(document.querySelectorAll('[aria-controls]')).toHaveLength(0)
  })

  it('user: Dashboard and Tickets only, and no sections', () => {
    renderAt('/dashboard', 'user')

    expect(screen.queryAllByRole('link', { hidden: true }).map((a) => a.getAttribute('href'))).toEqual([
      '/dashboard', '/tickets', '/account',
    ])
    for (const s of SECTIONS) expect(screen.queryByRole('button', { name: s.name })).toBeNull()
  })
})

describe('remembered state', () => {
  it('writes what the visitor opened, and nothing for a section only the current page opened', async () => {
    const user = userEvent.setup()
    renderAt('/admin/users')
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull()

    await user.click(heading('Integrations'))
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? 'null')).toEqual(['integrations'])
  })

  it('opens a remembered section next to the current one on the next visit', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(['integrations']))
    renderAt('/admin/users')
    expect(openSections()).toEqual(['People', 'Integrations'])
  })

  it('opens a remembered section even where no section is current', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(['system']))
    renderAt('/dashboard')
    expect(openSections()).toEqual(['System'])
  })

  it('forgets a section the visitor closed', async () => {
    const user = userEvent.setup()
    localStorage.setItem(STORAGE_KEY, JSON.stringify(['integrations', 'system']))
    renderAt('/dashboard')
    await user.click(heading('System'))
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? 'null')).toEqual(['integrations'])
  })

  it.each(['not json', '{"a":1}', '"people"', '[1,null,{}]'])('ignores a stored value of %s', (junk) => {
    localStorage.setItem(STORAGE_KEY, junk)
    renderAt('/admin/users')
    expect(openSections()).toEqual(['People'])
  })

  it('ignores a remembered section that no longer exists', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(['billing', 'system']))
    renderAt('/dashboard')
    expect(openSections()).toEqual(['System'])
  })

  it('still works when storage cannot be read or written', async () => {
    const user = userEvent.setup()
    getItem = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })

    renderAt('/admin/users')
    expect(openSections()).toEqual(['People'])

    await act(async () => { await user.click(heading('System')) })
    expect(openSections()).toEqual(['People', 'System'])
    expect(getItem).toHaveBeenCalled()
  })
})
