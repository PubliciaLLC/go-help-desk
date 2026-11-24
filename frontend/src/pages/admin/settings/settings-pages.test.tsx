import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import { GeneralSettingsPage } from './GeneralSettingsPage'
import { FeaturesSettingsPage } from './FeaturesSettingsPage'
import { BrandingSettingsPage } from './BrandingSettingsPage'
import { EmailSettingsPage } from './EmailSettingsPage'
import { EmailTemplatesSettingsPage } from './EmailTemplatesSettingsPage'
import { AttachmentSettingsPage } from './AttachmentSettingsPage'
import { GuestAccessSettingsPage } from './GuestAccessSettingsPage'
import { AuthenticationSettingsPage } from './AuthenticationSettingsPage'
import { PrivacySettingsPage } from './PrivacySettingsPage'

// #674: each settings page holds exactly its sections, saves only what was
// edited on it, and asks before it is left with unsaved edits. Rendered in a
// real memory router, because the leave guard is the router's own blocker.

interface PageDef {
  path: string
  Page: () => React.JSX.Element
  h1: string
  h2: string[]
}

const PAGES: PageDef[] = [
  { path: '/admin/settings/general', Page: GeneralSettingsPage, h1: 'General', h2: ['Tickets', 'Ticket lifecycle', 'Updates'] },
  { path: '/admin/settings/features', Page: FeaturesSettingsPage, h1: 'Features', h2: ['SLA'] },
  { path: '/admin/settings/branding', Page: BrandingSettingsPage, h1: 'Branding', h2: ['Identity'] },
  { path: '/admin/settings/email', Page: EmailSettingsPage, h1: 'Email', h2: ['Staff email about tickets', 'SLA alerts'] },
  {
    path: '/admin/settings/attachments',
    Page: AttachmentSettingsPage,
    h1: 'File Attachments',
    h2: ['Uploads', 'File size', 'Malware scanning', 'Reputation lookup'],
  },
  { path: '/admin/settings/guest-access', Page: GuestAccessSettingsPage, h1: 'Guest Access', h2: ['Submissions'] },
  {
    path: '/admin/settings/authentication',
    Page: AuthenticationSettingsPage,
    h1: 'Authentication',
    h2: ['SAML', 'OIDC / OpenID Connect', 'Multi-factor authentication', 'Passwords', 'API keys', 'Registration'],
  },
  {
    path: '/admin/settings/privacy',
    Page: PrivacySettingsPage,
    h1: 'Privacy',
    h2: ['Ticket visibility', 'Available queue', 'Audit log', 'Privacy'],
  },
]

const TEMPLATES: PageDef = {
  path: '/admin/settings/email/templates',
  Page: EmailTemplatesSettingsPage,
  h1: 'Email',
  h2: ['Email templates'],
}

const ALL_ROUTES = [...PAGES, TEMPLATES]

let dump: Record<string, unknown>
let patchSpy: ReturnType<typeof vi.spyOn>

const DEFAULT_DUMP = { sla_enabled: false, ticket_prefix: 'GHD', staff_email_assigned_override: 'user' }

beforeEach(() => {
  dump = { ...DEFAULT_DUMP }
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'admin@example.com',
      display_name: 'Ada Admin',
      role: 'admin',
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: { ...dump } })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
  }) as never)
  patchSpy = vi.spyOn(api, 'patch').mockImplementation(((_url: string, body: Record<string, unknown>) => {
    dump = { ...dump, ...body }
    return Promise.resolve({ data: undefined })
  }) as never)
})
afterEach(() => vi.restoreAllMocks())

function renderAt(path: string, opts: { cached?: boolean } = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } },
  })
  if (opts.cached) queryClient.setQueryData(['admin', 'settings'], { ...dump })
  const root = createRootRoute({ component: Outlet })
  const routes = [
    ...ALL_ROUTES.map(({ path: p, Page }) => createRoute({ getParentRoute: () => root, path: p, component: Page })),
    createRoute({ getParentRoute: () => root, path: '/dashboard', component: () => <div>Dashboard stub</div> }),
  ]
  const router = createRouter({
    routeTree: root.addChildren(routes),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { router, queryClient }
}

const h2Names = () => screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)
const saveButton = () => screen.getByRole('button', { name: /save changes/i })
const sidebarDashboard = () => screen.getByRole('link', { name: 'Dashboard' })
const dialogTitle = () => screen.queryByText('Leave without saving?')

async function loaded(def: PageDef) {
  await screen.findByRole('heading', { level: 1, name: def.h1 })
  await waitFor(() => expect(h2Names()).toEqual(def.h2))
}

async function editPrefix(value: string) {
  const box = await screen.findByLabelText('Tracking number prefix')
  fireEvent.change(box, { target: { value } })
}

describe('each page holds exactly its sections', () => {
  it.each(PAGES)('$h1 ($path)', async (def) => {
    renderAt(def.path)
    await loaded(def)
  })

  it('Templates holds the template list under the h2', async () => {
    renderAt(TEMPLATES.path)
    await loaded(TEMPLATES)
  })

  it('Features has one tab, SLA', async () => {
    renderAt('/admin/settings/features')
    await loaded(PAGES[1])
    const nav = screen.getByRole('navigation', { name: 'Features sections' })
    expect(within(nav).getAllByRole('link').map((a) => a.textContent)).toEqual(['SLA'])
  })

  it('Email Notifications has the four overrides and the SLA alerts switch', async () => {
    renderAt('/admin/settings/email')
    await loaded(PAGES[3])
    expect(screen.getAllByLabelText(/: administrator override$/)).toHaveLength(4)
    expect(screen.getByRole('switch', { name: 'SLA alerts' })).toBeTruthy()
    expect(document.body.textContent).toContain('Needs SLA alerts turned on below.')
  })
})

describe('the Email tabs', () => {
  it.each([
    ['/admin/settings/email', 'Notifications', 'Templates'],
    ['/admin/settings/email/templates', 'Templates', 'Notifications'],
  ])('%s shows h1 Email and both tab links, %s current', async (path, current, other) => {
    renderAt(path)
    await screen.findByRole('heading', { level: 1, name: 'Email' })
    const nav = screen.getByRole('navigation', { name: 'Email sections' })
    const links = within(nav).getAllByRole('link')
    expect(links.map((a) => a.textContent)).toEqual(['Notifications', 'Templates'])
    expect(within(nav).getByRole('link', { name: current }).getAttribute('aria-current')).toBe('page')
    expect(within(nav).getByRole('link', { name: other }).getAttribute('aria-current')).toBeNull()
  })

  it('Templates has no Save changes button', async () => {
    renderAt('/admin/settings/email/templates')
    await loaded(TEMPLATES)
    expect(screen.queryByRole('button', { name: /save changes/i })).toBeNull()
  })

  it('Notifications has one', async () => {
    renderAt('/admin/settings/email')
    await loaded(PAGES[3])
    expect(saveButton()).toBeTruthy()
  })
})

describe('each page saves only its own keys', () => {
  it('General sends exactly the edited key', async () => {
    renderAt('/admin/settings/general')
    await loaded(PAGES[0])
    await editPrefix('ABC')
    await userEvent.click(saveButton())
    await waitFor(() => expect(patchSpy).toHaveBeenCalledTimes(1))
    expect(patchSpy).toHaveBeenCalledWith('/admin/settings', { ticket_prefix: 'ABC' })
  })

  it('File Attachments sends a typed reputation key', async () => {
    renderAt('/admin/settings/attachments')
    await loaded(PAGES[4])
    await userEvent.type(screen.getAllByLabelText(/api key/i)[0], 'sekret')
    await userEvent.click(saveButton())
    await waitFor(() => expect(patchSpy).toHaveBeenCalledTimes(1))
    expect(patchSpy).toHaveBeenCalledWith('/admin/settings', { attachment_reputation_virustotal_key: 'sekret' })
  })
})

describe('a section that seeds at mount sees the stored value', () => {
  beforeEach(() => {
    dump = { ...DEFAULT_DUMP, attachment_allowed_types: ['application/pdf'] }
  })
  const box = () => screen.getByLabelText(/allowed file types/i) as HTMLTextAreaElement

  it.each([
    ['cold', false],
    ['with the dump already cached', true],
  ])('File Attachments shows the stored list, %s', async (_name, cached) => {
    renderAt('/admin/settings/attachments', { cached })
    await loaded(PAGES[4])
    expect(box().value).toBe('application/pdf')
  })

  it('saving another Uploads edit does not send the list', async () => {
    renderAt('/admin/settings/attachments')
    await loaded(PAGES[4])
    await userEvent.selectOptions(screen.getByLabelText('When a file’s content contradicts its name'), 'wrap')
    await userEvent.click(saveButton())
    await waitFor(() => expect(patchSpy).toHaveBeenCalledTimes(1))
    expect(patchSpy).toHaveBeenCalledWith('/admin/settings', { attachment_mismatch_handling: 'wrap' })
  })
})

describe('the leave guard', () => {
  it('prompts after an edit; Stay keeps the edit and the address', async () => {
    const { router } = renderAt('/admin/settings/general')
    await loaded(PAGES[0])
    await editPrefix('ABC')
    await userEvent.click(sidebarDashboard())
    expect(await screen.findByText('Leave without saving?')).toBeTruthy()
    expect(screen.getByText('Your changes on this page have not been saved. Leaving discards them.')).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: 'Stay' }))
    await waitFor(() => expect(dialogTitle()).toBeNull())
    expect(router.state.location.pathname).toBe('/admin/settings/general')
    expect((screen.getByLabelText('Tracking number prefix') as HTMLInputElement).value).toBe('ABC')
  })

  it('Leave navigates', async () => {
    const { router } = renderAt('/admin/settings/general')
    await loaded(PAGES[0])
    await editPrefix('ABC')
    await userEvent.click(sidebarDashboard())
    await userEvent.click(await screen.findByRole('button', { name: 'Leave without saving' }))
    expect(await screen.findByText('Dashboard stub')).toBeTruthy()
    expect(router.state.location.pathname).toBe('/dashboard')
    expect(patchSpy).not.toHaveBeenCalled()
  })

  it('Notifications to Templates prompts; after Leave, Notifications shows the stored value', async () => {
    renderAt('/admin/settings/email')
    await loaded(PAGES[3])
    const field = () => screen.getByLabelText('Tickets assigned to me: administrator override') as HTMLSelectElement
    await userEvent.selectOptions(field(), 'on')
    expect(field().value).toBe('on')
    await userEvent.click(within(screen.getByRole('navigation', { name: 'Email sections' })).getByRole('link', { name: 'Templates' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Leave without saving' }))
    await screen.findByRole('heading', { level: 2, name: 'Email templates' })
    await userEvent.click(within(screen.getByRole('navigation', { name: 'Email sections' })).getByRole('link', { name: 'Notifications' }))
    await waitFor(() => expect(field().value).toBe('user'))
    expect(dialogTitle()).toBeNull()
  })

  it('does not prompt with no edits', async () => {
    renderAt('/admin/settings/general')
    await loaded(PAGES[0])
    await userEvent.click(sidebarDashboard())
    expect(await screen.findByText('Dashboard stub')).toBeTruthy()
    expect(dialogTitle()).toBeNull()
  })

  it('does not prompt after a save', async () => {
    renderAt('/admin/settings/general')
    await loaded(PAGES[0])
    await editPrefix('ABC')
    await userEvent.click(saveButton())
    await screen.findByText('Saved.')
    await waitFor(() => expect(screen.queryByText('Saving…')).toBeNull())
    await userEvent.click(sidebarDashboard())
    expect(await screen.findByText('Dashboard stub')).toBeTruthy()
    expect(dialogTitle()).toBeNull()
  })

  it('does not prompt from Templates', async () => {
    renderAt('/admin/settings/email/templates')
    await loaded(TEMPLATES)
    await userEvent.click(within(screen.getByRole('navigation', { name: 'Email sections' })).getByRole('link', { name: 'Notifications' }))
    await screen.findByLabelText('Tickets assigned to me: administrator override')
    expect(dialogTitle()).toBeNull()
  })

  it.each(PAGES)('opening and leaving $h1 untouched never prompts', async (def) => {
    renderAt(def.path)
    await loaded(def)
    // Let every section's own queries land: a control that writes the draft on
    // mount would show up as an edit.
    await act(async () => { await new Promise((r) => setTimeout(r, 50)) })
    await userEvent.click(sidebarDashboard())
    expect(await screen.findByText('Dashboard stub')).toBeTruthy()
    expect(dialogTitle()).toBeNull()
  })
})

describe('Guest Access and File Attachments', () => {
  it('Guest Access shows the stored per-file limit as the slider top', async () => {
    dump = { ...DEFAULT_DUMP, attachment_max_mb: 10 }
    renderAt('/admin/settings/guest-access')
    await loaded(PAGES[5])
    expect((screen.getByRole('slider') as HTMLInputElement).max).toBe('10')
  })

  it('falls back to 25 when none is stored', async () => {
    renderAt('/admin/settings/guest-access')
    await loaded(PAGES[5])
    expect((screen.getByRole('slider') as HTMLInputElement).max).toBe('25')
  })

  it('after a save of 30 on File Attachments, Guest Access has a top of 30', async () => {
    dump = { ...DEFAULT_DUMP, attachment_max_mb: 10 }
    const { router } = renderAt('/admin/settings/attachments')
    await loaded(PAGES[4])
    const box = screen.getByLabelText('Largest file anyone may attach')
    await userEvent.clear(box)
    await userEvent.type(box, '30')
    await userEvent.click(saveButton())
    await waitFor(() => expect(patchSpy).toHaveBeenCalledWith('/admin/settings', { attachment_max_mb: 30 }))
    await waitFor(() => expect(screen.queryByText('Saving…')).toBeNull())
    act(() => router.history.push('/admin/settings/guest-access'))
    await loaded(PAGES[5])
    expect((screen.getByRole('slider') as HTMLInputElement).max).toBe('30')
  })

  it('File Attachments shows the stored guest value', async () => {
    dump = { ...DEFAULT_DUMP, attachment_max_mb: 25, guest_attachment_max_mb: 8 }
    renderAt('/admin/settings/attachments')
    await loaded(PAGES[4])
    expect(screen.getByTestId('guest-limit-line').textContent).toContain('Guests: up to 8 MB.')
  })
})
