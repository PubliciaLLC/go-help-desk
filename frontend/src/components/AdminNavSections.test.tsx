import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, within, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { User } from '@/api/types'

// The admin sidebar is one expanding "Admin" item holding four collapsible
// sections. The pages and URLs are unchanged; only where the links sit is.
// Audit Log is administrators only (the server refuses staff), so it is in
// System and nowhere else.

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

// hidden: true because a section's heading is itself inside the Admin panel,
// which is hidden while Admin is closed. State is read off aria-expanded.
const heading = (name: string) => screen.getByRole('button', { name, hidden: true })
const isOpen = (name: string) => heading(name).getAttribute('aria-expanded') === 'true'
const adminOpen = () => isOpen('Admin')
const openSections = () => SECTIONS.filter((s) => isOpen(s.name)).map((s) => s.name)
// What a person can see: a section counts only while Admin is open too.
const visibleSections = () => (adminOpen() ? openSections() : [])

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
  it('has Admin, then the four sections inside it, each controlling the panel that holds its entries', () => {
    renderAt('/dashboard')

    const buttons = screen.getAllByRole('button', { hidden: true }).filter((b) => b.hasAttribute('aria-controls'))
    expect(buttons.map((b) => b.textContent)).toEqual(['Admin', ...SECTIONS.map((s) => s.name)])
    for (const b of buttons) expect(b.getAttribute('aria-expanded')).not.toBeNull()

    // Nesting is what the indentation draws: sections sit in Admin's panel,
    // links in their section's.
    const adminPanel = panelOf('Admin')
    for (const s of SECTIONS) {
      expect(adminPanel.contains(heading(s.name)), `${s.name} is not inside Admin`).toBe(true)
      expect(panelOf(s.name).contains(heading(s.name))).toBe(false)
      expect(adminPanel.contains(panelOf(s.name))).toBe(true)
      expect(linksIn(panelOf(s.name))).toEqual(s.links.map(([label, href]) => [label, href]))
    }
  })

  it('puts Admin after Dashboard and Tickets, and keeps no Audit Log entry outside it', () => {
    renderAt('/dashboard')
    const nav = document.querySelector('nav') as HTMLElement
    const outside = Array.from(nav.querySelectorAll('a')).filter((a) => !panelOf('Admin').contains(a))
    expect(outside.map((a) => a.textContent)).toEqual(['Dashboard', 'Tickets'])
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
    expect(panelOf('Admin').hidden).toBe(true)
    expect(panelOf('People').hidden).toBe(true)

    await user.click(heading('Admin'))
    expect(panelOf('Admin').hidden).toBe(false)
    expect(panelOf('People').hidden).toBe(true)

    await user.click(heading('People'))
    expect(isOpen('People')).toBe(true)
    expect(panelOf('People').hidden).toBe(false)
    expect(screen.getByRole('link', { name: 'Users' })).toBeTruthy()

    await user.click(heading('People'))
    expect(isOpen('People')).toBe(false)
    expect(screen.queryByRole('link', { name: 'Users' })).toBeNull()

    await user.click(heading('Admin'))
    expect(screen.queryByRole('button', { name: 'People' })).toBeNull()
  })
})

describe('what is open', () => {
  it('is nothing on a page outside the admin sections', () => {
    renderAt('/dashboard')
    expect(adminOpen()).toBe(false)
    expect(openSections()).toEqual([])
    expect(panelOf('Admin').hidden).toBe(true)
  })

  it.each(SECTIONS.flatMap((s) => s.links.map(([label, href]) => [s.name, label, href] as const)))(
    'is Admin and only %s when the page is %s (%s)',
    (section, _label, href) => {
      renderAt(href)
      expect(adminOpen()).toBe(true)
      expect(openSections()).toEqual([section])
    },
  )

  it('follows a sub-page of an admin page', () => {
    renderAt('/admin/users/u-123')
    expect(adminOpen()).toBe(true)
    expect(openSections()).toEqual(['People'])
  })

  it('opens Admin and the section of the page navigated to, and closes what only the last page had open', () => {
    const { rerender } = renderAt('/dashboard')
    expect(visibleSections()).toEqual([])

    router.pathname = '/admin/tags'
    rerender(<Layout>content</Layout>)
    expect(adminOpen()).toBe(true)
    expect(openSections()).toEqual(['Tickets'])

    router.pathname = '/admin/webhooks'
    rerender(<Layout>content</Layout>)
    expect(openSections()).toEqual(['Integrations'])

    router.pathname = '/dashboard'
    rerender(<Layout>content</Layout>)
    expect(adminOpen()).toBe(false)
    expect(openSections()).toEqual([])
  })

  it('opens a section again when a page links to another page in it that the visitor had closed', async () => {
    const user = userEvent.setup()
    const { rerender } = renderAt('/admin/users')
    await user.click(heading('People'))
    expect(isOpen('People')).toBe(false)

    router.pathname = '/admin/groups'
    rerender(<Layout>content</Layout>)
    expect(isOpen('People')).toBe(true)
  })

  it('opens Admin again on navigation when the visitor had closed it', async () => {
    const user = userEvent.setup()
    const { rerender } = renderAt('/admin/users')
    await user.click(heading('Admin'))
    expect(adminOpen()).toBe(false)
    expect(screen.queryByRole('link', { name: 'Users' })).toBeNull()

    router.pathname = '/admin/roles'
    rerender(<Layout>content</Layout>)
    expect(adminOpen()).toBe(true)
    expect(screen.getByRole('link', { name: 'Roles' })).toBeTruthy()
  })

  it('lets the visitor close the current section, and leaves it closed until the next navigation', async () => {
    const user = userEvent.setup()
    renderAt('/admin/users')
    await user.click(heading('People'))
    await user.click(heading('System'))
    await user.click(heading('System'))
    expect(openSections()).toEqual([])
    expect(adminOpen()).toBe(true)
  })

  // One section at a time (an accordion): the menu has to fit a laptop screen
  // with Admin open, and two long sections do not.
  it('opens one section at a time: opening one closes the one that was open', async () => {
    const user = userEvent.setup()
    renderAt('/admin/users')
    expect(openSections()).toEqual(['People'])

    await user.click(heading('Integrations'))
    expect(openSections()).toEqual(['Integrations'])
    await user.click(heading('System'))
    expect(openSections()).toEqual(['System'])
    await user.click(heading('System'))
    expect(openSections()).toEqual([])
  })

  it('closes the section the visitor opened when navigation opens the current page\'s', async () => {
    const user = userEvent.setup()
    const { rerender } = renderAt('/admin/users')
    await user.click(heading('Integrations'))
    expect(openSections()).toEqual(['Integrations'])

    router.pathname = '/admin/groups'
    rerender(<Layout>content</Layout>)
    expect(openSections()).toEqual(['People'])
  })

  it('keeps the open section as it was when Admin is closed and opened again', async () => {
    const user = userEvent.setup()
    renderAt('/dashboard')
    await user.click(heading('Admin'))
    await user.click(heading('System'))
    await user.click(heading('Admin'))
    await user.click(heading('Admin'))
    expect(openSections()).toEqual(['System'])
  })
})

describe('keyboard', () => {
  it('reaches Admin and then each section with Tab, and Enter and Space toggle them', async () => {
    const user = userEvent.setup()
    renderAt('/dashboard')

    // Sections are inside a closed Admin, so they are reached once it is open.
    for (let i = 0; i < 10 && document.activeElement !== heading('Admin'); i++) await user.tab()
    expect(document.activeElement).toBe(heading('Admin'))
    await user.keyboard('{Enter}')
    expect(adminOpen()).toBe(true)

    const seen: string[] = []
    for (let i = 0; i < 40 && seen.length < 4; i++) {
      await user.tab()
      const el = document.activeElement
      if (el?.hasAttribute('aria-controls')) seen.push(el.textContent ?? '')
    }
    expect(seen).toEqual(SECTIONS.map((s) => s.name))

    heading('People').focus()
    await user.keyboard('{Enter}')
    expect(isOpen('People')).toBe(true)
    await user.keyboard(' ')
    expect(isOpen('People')).toBe(false)
    heading('Admin').focus()
    await user.keyboard(' ')
    expect(adminOpen()).toBe(false)
  })

  it("does not put a closed panel's links in the tab order", async () => {
    const user = userEvent.setup()
    renderAt('/dashboard')
    for (let i = 0; i < 30; i++) {
      await user.tab()
      expect(document.activeElement?.getAttribute('href') ?? '').not.toMatch(/^\/admin\//)
      expect(document.activeElement?.textContent).not.toBe('People')
    }
  })
})

describe('visibility by role', () => {
  const linkNames = () => screen.queryAllByRole('link').map((a) => a.textContent)

  it('admin: all 15 admin pages are reachable through Admin, Audit Log among them, under System', async () => {
    const user = userEvent.setup()
    renderAt('/dashboard')
    await user.click(heading('Admin'))
    // One section is open at a time, so each is opened in turn.
    const reached: string[] = []
    for (const s of SECTIONS) {
      await user.click(heading(s.name))
      const names = linkNames()
      expect(names.slice(0, 2)).toEqual(['Dashboard', 'Tickets'])
      for (const [label, href] of s.links) {
        expect(names.filter((n) => n === label), label).toHaveLength(1)
        expect(within(panelOf(s.name)).getByRole('link', { name: label }).getAttribute('href')).toBe(href)
        reached.push(label)
      }
    }
    expect(reached).toHaveLength(15)
    expect(reached).toContain('Audit Log')
  })

  // Audit Log is administrators only. Staff have the history on each ticket.
  it.each(['staff', 'user'] as const)('%s: Dashboard and Tickets, no Admin, no Audit Log', (role) => {
    renderAt('/dashboard', role)

    expect(screen.queryAllByRole('link', { hidden: true }).map((a) => [a.textContent, a.getAttribute('href')])).toEqual([
      ['Dashboard', '/dashboard'],
      ['Tickets', '/tickets'],
      ['Your account', '/account'],
    ])
    expect(document.querySelectorAll('[aria-controls]')).toHaveLength(0)
    expect(screen.queryByRole('button', { name: 'Admin', hidden: true })).toBeNull()
    expect(screen.queryByRole('link', { name: 'Audit Log', hidden: true })).toBeNull()
  })

  it('staff on the Audit Log page see no Admin menu either', () => {
    renderAt('/admin/audit', 'staff')
    expect(document.querySelectorAll('[aria-controls]')).toHaveLength(0)
    expect(screen.queryByRole('link', { name: 'Audit Log', hidden: true })).toBeNull()
  })
})

describe('remembered state', () => {
  it('writes the section the visitor opened, and nothing for what only the current page opened', async () => {
    const user = userEvent.setup()
    renderAt('/admin/users')
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull()

    await user.click(heading('Integrations'))
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? 'null')).toEqual(['integrations'])
  })

  it('remembers Admin when the visitor opens it, and at most one section beside it', async () => {
    const user = userEvent.setup()
    renderAt('/dashboard')
    await user.click(heading('Admin'))
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? 'null')).toEqual(['admin'])
    await user.click(heading('System'))
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? 'null')).toEqual(['admin', 'system'])
    await user.click(heading('People'))
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? 'null')).toEqual(['admin', 'people'])
  })

  it('opens the remembered Admin and section where no section is current', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(['admin', 'system']))
    renderAt('/dashboard')
    expect(adminOpen()).toBe(true)
    expect(openSections()).toEqual(['System'])
  })

  it('lets the current page\'s section win over the remembered one', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(['admin', 'integrations']))
    renderAt('/admin/users')
    expect(adminOpen()).toBe(true)
    expect(openSections()).toEqual(['People'])
  })

  it('returns to the remembered state when navigation leaves the admin pages', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(['admin', 'integrations']))
    const { rerender } = renderAt('/admin/users')
    router.pathname = '/dashboard'
    rerender(<Layout>content</Layout>)
    expect(adminOpen()).toBe(true)
    expect(openSections()).toEqual(['Integrations'])
  })

  it('keeps a remembered section shut away while Admin itself is closed', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(['system']))
    renderAt('/dashboard')
    expect(adminOpen()).toBe(false)
    expect(visibleSections()).toEqual([])
  })

  it('forgets what the visitor closed', async () => {
    const user = userEvent.setup()
    localStorage.setItem(STORAGE_KEY, JSON.stringify(['admin', 'integrations']))
    renderAt('/dashboard')
    await user.click(heading('Integrations'))
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? 'null')).toEqual(['admin'])
    await user.click(heading('Admin'))
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? 'null')).toEqual([])
  })

  it('does not forget the remembered section when the visitor closes the current page\'s', async () => {
    const user = userEvent.setup()
    localStorage.setItem(STORAGE_KEY, JSON.stringify(['admin', 'integrations']))
    renderAt('/admin/users')
    await user.click(heading('People'))
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? 'null')).toEqual(['admin', 'integrations'])
  })

  // Earlier builds let several sections stay open and stored them all. That
  // is not a state the menu has now, so it is not guessed at.
  it.each([
    ['several sections', ['admin', 'integrations', 'system']],
    ['two sections without Admin', ['people', 'tickets']],
  ])('treats a stored value with %s as nothing remembered', (_name, ids) => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(ids))
    renderAt('/dashboard')
    expect(adminOpen()).toBe(false)
    expect(openSections()).toEqual([])
  })

  it.each(['not json', '{"a":1}', '"people"', '[1,null,{}]'])('ignores a stored value of %s', (junk) => {
    localStorage.setItem(STORAGE_KEY, junk)
    renderAt('/admin/users')
    expect(adminOpen()).toBe(true)
    expect(openSections()).toEqual(['People'])
  })

  it('ignores a remembered entry that no longer exists', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(['billing', 'admin', 'system']))
    renderAt('/dashboard')
    expect(adminOpen()).toBe(true)
    expect(openSections()).toEqual(['System'])
  })

  it('still works when storage cannot be read or written', async () => {
    const user = userEvent.setup()
    getItem = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })

    renderAt('/admin/users')
    expect(adminOpen()).toBe(true)
    expect(openSections()).toEqual(['People'])

    await act(async () => { await user.click(heading('System')) })
    expect(openSections()).toEqual(['System'])
    await act(async () => { await user.click(heading('Admin')) })
    expect(adminOpen()).toBe(false)
    expect(getItem).toHaveBeenCalled()
  })
})
