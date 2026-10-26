import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, AxiosHeaders } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The email template editor (#158). Checked as you type: the draft goes to the
// preview endpoint 400 ms after the last keystroke, problems show beside the
// field with their line and column, and Save stays disabled while the draft is
// invalid or not yet checked. The server still validates every save; the test
// at the end proves the page shows its refusal too.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/email-templates' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { EmailTemplatesPage } from './EmailTemplatesPage'

const LIST = [
  { name: 'ticket_created', label: 'Ticket received', group: 'requester', shell: false, customised: false, broken: false, updated_at: null, updated_by_name: '' },
  { name: 'guest_link', label: 'New guest link', group: 'requester', shell: false, customised: true, broken: false, updated_at: '2026-10-25T10:00:00Z', updated_by_name: 'Ada Admin' },
  { name: 'staff_assigned', label: 'Assigned to you', group: 'staff', shell: false, customised: false, broken: false, updated_at: null, updated_by_name: '' },
  { name: 'password_reset', label: 'Reset your password', group: 'account', shell: false, customised: true, broken: true, updated_at: '2026-10-25T10:00:00Z', updated_by_name: 'Ada Admin' },
  { name: '_header', label: 'Header', group: 'shared', shell: true, customised: false, broken: false, updated_at: null, updated_by_name: '' },
  { name: '_footer', label: 'Footer', group: 'shared', shell: true, customised: false, broken: false, updated_at: null, updated_by_name: '' },
]

const DETAIL = {
  ...LIST[0],
  subject: 'We have received {{if .TrackingNumber}}[{{.TrackingNumber}}]{{else}}your ticket{{end}}',
  body: 'We have received your ticket.\n\nOpen it:\n{{.TicketURL}}\n',
  default_subject: 'We have received {{if .TrackingNumber}}[{{.TrackingNumber}}]{{else}}your ticket{{end}}',
  default_body: 'We have received your ticket.\n\nOpen it:\n{{.TicketURL}}\n',
  required: 'TicketURL',
  variables: [
    { name: 'TrackingNumber', description: 'The ticket tracking number.' },
    { name: 'TicketURL', description: 'The link to the ticket.' },
  ],
  max_body_bytes: 8192,
  max_subject_bytes: 200,
}

const SHELL = {
  ...LIST[4], subject: '', body: '', default_subject: '', default_body: '', required: '',
  variables: [{ name: 'SiteName', description: 'The help desk name.' }], max_body_bytes: 8192, max_subject_bytes: 200,
}

const BROKEN = {
  ...LIST[3], subject: 'Reset', body: 'no link here', default_subject: 'Reset your password', default_body: 'Click {{.ResetURL}}\n',
  required: 'ResetURL', variables: [{ name: 'ResetURL', description: 'The reset link.' }], max_body_bytes: 8192, max_subject_bytes: 200,
  broken_reason: 'body: must include {{.ResetURL}} outside any {{if}}',
}

const OK_PREVIEW = { valid: true, errors: [], subject: 'We have received [GHD-2026-000042]', body: 'We have received your ticket.\n\nOpen it:\nhttps://x/tickets/0\n' }

beforeEach(() => {
  vi.restoreAllMocks()
  window.history.replaceState(null, '', '/admin/email-templates')
  useAuthStore.setState({
    user: { id: 'u-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin', mfa_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
  })
})

afterEach(() => {
  window.history.replaceState(null, '', '/')
})

function refusal(code: string, message: string, extra: Record<string, unknown> = {}, status = 400) {
  const headers = new AxiosHeaders()
  return new AxiosError('Request failed', 'ERR_BAD_REQUEST', { headers } as never, null, {
    status, statusText: 'Bad Request', headers, config: { headers } as never,
    data: { error: { code, message, ...extra } },
  } as never)
}

type Preview =
  | typeof OK_PREVIEW
  | (typeof OK_PREVIEW & { html: string })
  | { valid: boolean; errors: { field: string; line: number; col: number; message: string }[] }

interface Wiring {
  detail?: unknown
  /** What the preview endpoint says for a draft. */
  preview?: (draft: { subject: string; body: string; html?: string }) => Preview
}

function wire(w: Wiring = {}) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/email-templates') return Promise.resolve({ data: LIST })
    if (url.startsWith('/admin/email-templates/')) return Promise.resolve({ data: w.detail ?? DETAIL })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  const previewCalls: { at: number; draft: { subject: string; body: string; html?: string } }[] = []
  const post = vi.spyOn(api, 'post').mockImplementation(((url: string, draft: { subject: string; body: string; html?: string }) => {
    if (url.endsWith('/preview')) {
      previewCalls.push({ at: Date.now(), draft })
      return Promise.resolve({ data: (w.preview ?? (() => OK_PREVIEW))(draft) })
    }
    if (url.endsWith('/test')) return Promise.resolve({ data: { sent: true } })
    return Promise.resolve({ data: {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const put = vi.spyOn(api, 'put').mockResolvedValue({ data: DETAIL } as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const del = vi.spyOn(api, 'delete').mockResolvedValue({ data: undefined } as any)
  return { post, put, del, previewCalls }
}

async function openEditor(name: string) {
  window.history.replaceState(null, '', `/admin/email-templates?t=${name}`)
  renderWithQuery(<EmailTemplatesPage />)
  return await screen.findByLabelText('Body')
}

const bodyField = () => screen.getByLabelText('Body') as HTMLTextAreaElement
const saveButton = () => screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement

function type(el: HTMLElement, value: string) {
  fireEvent.change(el, { target: { value } })
}

// ── the list ─────────────────────────────────────────────────────────────────

describe('the template list', () => {
  it('groups the templates and marks the edited and the broken ones', async () => {
    wire()
    renderWithQuery(<EmailTemplatesPage />)
    await screen.findByRole('heading', { level: 1, name: 'Email templates' })

    for (const g of ['Mail to requesters', 'Mail to staff', 'Account mail', 'Header and footer']) {
      expect(await screen.findByRole('heading', { name: g })).toBeTruthy()
    }
    const guest = screen.getByRole('button', { name: /New guest link/ })
    expect(within(guest).getByText('Customised')).toBeTruthy()
    const reset = screen.getByRole('button', { name: /Reset your password/ })
    expect(within(reset).getByText('Broken')).toBeTruthy()
    expect(within(screen.getByRole('button', { name: /Ticket received/ })).queryByText('Customised')).toBeNull()
  })

  it('says templates cannot include ticket text, and why', async () => {
    wire()
    renderWithQuery(<EmailTemplatesPage />)
    await screen.findByRole('heading', { level: 1, name: 'Email templates' })
    expect(screen.getByText(/cannot include ticket text, by design/i)).toBeTruthy()
    const link = screen.getByRole('link', { name: /GHSA-p2wx-wvfr-7h3p/ })
    expect((link).getAttribute('href')).toContain('GHSA-p2wx-wvfr-7h3p')
  })

  it('opens a template in the editor', async () => {
    wire()
    renderWithQuery(<EmailTemplatesPage />)
    await userEvent.click(await screen.findByRole('button', { name: /Ticket received/ }))
    expect(((await screen.findByLabelText('Body')) as HTMLTextAreaElement).value).toBe(DETAIL.body)
    expect((screen.getByLabelText('Subject') as HTMLInputElement).value).toBe(DETAIL.subject)
  })
})

// ── live validation ──────────────────────────────────────────────────────────

describe('checking the draft as you type', () => {
  it('asks the server 400 ms after the last keystroke, once, with the latest text', async () => {
    const { previewCalls } = wire()
    await openEditor('ticket_created')
    await waitFor(() => expect(previewCalls.length).toBeGreaterThan(0)) // the saved text is checked too
    const before = previewCalls.length

    const t0 = Date.now()
    type(bodyField(), '{{.TicketURL}} a')
    type(bodyField(), '{{.TicketURL}} ab')
    type(bodyField(), '{{.TicketURL}} abc')
    await new Promise((r) => setTimeout(r, 150))
    expect(previewCalls.length, 'no request while the person is still typing').toBe(before)

    await waitFor(() => expect(previewCalls.length).toBe(before + 1))
    const call = previewCalls[before]
    expect(call.draft.body).toBe('{{.TicketURL}} abc')
    expect(call.at - t0, 'debounced by about 400 ms').toBeGreaterThanOrEqual(380)
    expect(previewCalls.some((c) => c.draft.body.endsWith(' ab'))).toBe(false)
  })

  it('shows the problem next to the field with its line and column, and marks the field invalid', async () => {
    wire({
      preview: () => ({ valid: false, errors: [{ field: 'body', line: 2, col: 5, message: 'the function index is not allowed; templates have no functions' }] }),
    })
    await openEditor('ticket_created')

    const msg = await screen.findByText(/Line 2, column 5/)
    expect(msg.textContent).toContain('the function index is not allowed')
    const field = bodyField()
    expect((field).getAttribute('aria-invalid')).toBe('true')
    expect(field.getAttribute('aria-describedby')).toBe(msg.id)
    expect((screen.getByLabelText('Subject')).getAttribute('aria-invalid')).not.toBe('true')
  })

  it('marks each field that has a problem', async () => {
    wire({
      preview: () => ({
        valid: false,
        errors: [
          { field: 'subject', line: 1, col: 3, message: 'no such variable' },
          { field: 'body', line: 1, col: 1, message: 'must include the link' },
        ],
      }),
    })
    await openEditor('ticket_created')
    await screen.findByText(/Line 1, column 3/)
    expect((screen.getByLabelText('Subject')).getAttribute('aria-invalid')).toBe('true')
    expect((bodyField()).getAttribute('aria-invalid')).toBe('true')
  })

  it('shows a problem with no place (a length) without a position', async () => {
    wire({ preview: () => ({ valid: false, errors: [{ field: 'body', line: 0, col: 0, message: 'is 9000 bytes; the limit is 8192' }] }) })
    await openEditor('ticket_created')
    const msg = await screen.findByText(/is 9000 bytes/)
    expect(msg.textContent).not.toMatch(/Line/)
  })

  it('shows the rendered preview, and the sample data it was made with', async () => {
    wire()
    await openEditor('ticket_created')
    expect(await screen.findByText(/Open it:/, { selector: 'pre *, pre' })).toBeTruthy()
    expect(screen.getByText(/GHD-2026-000042/)).toBeTruthy()
  })
})

// ── Save is disabled while the draft is invalid ─────────────────────────────

describe('Save', () => {
  it('is off until something changed, then on once the draft has been checked and is valid', async () => {
    wire()
    await openEditor('ticket_created')
    await waitFor(() => expect((saveButton()).disabled).toBe(true)) // unchanged

    type(bodyField(), 'New wording. {{.TicketURL}}')
    expect((saveButton()).disabled, 'a draft that has not been checked yet cannot be saved').toBe(true)
    await waitFor(() => expect((saveButton()).disabled).toBe(false))
  })

  it('saves the checked draft', async () => {
    const { put } = wire()
    await openEditor('ticket_created')
    type(bodyField(), 'New wording. {{.TicketURL}}')
    await waitFor(() => expect((saveButton()).disabled).toBe(false))
    await userEvent.click(saveButton())
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put).toHaveBeenCalledWith('/admin/email-templates/ticket_created', {
      subject: DETAIL.subject, body: 'New wording. {{.TicketURL}}',
    })
  })

  it('cannot be used while the draft is invalid: no click, no Enter, no request', async () => {
    const { put } = wire({
      preview: (d) =>
        d.body.includes('{{.TicketURL}}')
          ? OK_PREVIEW
          : { valid: false, errors: [{ field: 'body', line: 1, col: 1, message: 'must include {{.TicketURL}}' }] },
    })
    await openEditor('ticket_created')
    type(bodyField(), 'no link at all')
    await screen.findByText(/must include/)
    expect((saveButton()).disabled).toBe(true)

    await userEvent.click(saveButton())
    fireEvent.submit(bodyField().closest('form') as HTMLFormElement)
    await new Promise((r) => setTimeout(r, 50))
    expect(put, 'an invalid draft must not reach the server from the UI').not.toHaveBeenCalled()

    // And it comes back as soon as the draft is fixed.
    type(bodyField(), 'with the link {{.TicketURL}}')
    await waitFor(() => expect((saveButton()).disabled).toBe(false))
  })

  it('stays off if the check itself could not be made', async () => {
    const { put } = wire()
    await openEditor('ticket_created')
    vi.spyOn(api, 'post').mockRejectedValue(refusal('internal_error', 'boom', {}, 500))
    type(bodyField(), 'x {{.TicketURL}}')
    await screen.findByText(/could not be checked/i)
    expect((saveButton()).disabled).toBe(true)
    expect(put).not.toHaveBeenCalled()
  })

  it('shows the server\'s refusal too, because the server is the authority', async () => {
    const { put } = wire()
    await openEditor('ticket_created')
    put.mockRejectedValue(
      refusal('invalid_template', 'body, line 3 column 1: nope', {
        field: 'body', line: 3, col: 1,
        errors: [{ field: 'body', line: 3, col: 1, message: 'a rule the page did not know' }],
      }),
    )
    type(bodyField(), 'looks fine to the page {{.TicketURL}}')
    await waitFor(() => expect((saveButton()).disabled).toBe(false))
    await userEvent.click(saveButton())

    const msg = await screen.findByText(/Line 3, column 1/)
    expect(msg.textContent).toContain('a rule the page did not know')
    expect((bodyField()).getAttribute('aria-invalid')).toBe('true')
  })
})

// ── variables, test send, revert ─────────────────────────────────────────────

describe('the editor tools', () => {
  it('inserts a variable where the cursor is', async () => {
    wire()
    await openEditor('ticket_created')
    const field = bodyField()
    field.focus()
    field.setSelectionRange(5, 5)
    await userEvent.click(screen.getByRole('button', { name: /\{\{\.TrackingNumber\}\}/ }))
    expect(bodyField().value.slice(0, 5 + '{{.TrackingNumber}}'.length)).toBe('We ha{{.TrackingNumber}}')
  })

  it('lists only the variables the template has, and says the link is required', async () => {
    wire()
    await openEditor('ticket_created')
    expect(screen.getByRole('button', { name: /\{\{\.TicketURL\}\}/ })).toBeTruthy()
    expect(screen.queryByRole('button', { name: /ResetURL/ })).toBeNull()
    expect(screen.getByText(/must include/i).textContent).toContain('{{.TicketURL}}')
  })

  it('sends a test of the draft and says where it went', async () => {
    const { post } = wire()
    await openEditor('ticket_created')
    type(bodyField(), 'Draft {{.TicketURL}}')
    await waitFor(() => expect((saveButton()).disabled).toBe(false))
    await userEvent.click(screen.getByRole('button', { name: 'Send test' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/admin/email-templates/ticket_created/test', { subject: DETAIL.subject, body: 'Draft {{.TicketURL}}' }),
    )
    expect(await screen.findByText(/sent to your own address/i)).toBeTruthy()
  })

  it('shows why a test could not be sent', async () => {
    wire()
    await openEditor('ticket_created')
    vi.spyOn(api, 'post').mockImplementation(((url: string) =>
      url.endsWith('/test')
        ? Promise.reject(refusal('send_failed', 'dialing SMTP server: connection refused', {}, 502))
        : Promise.resolve({ data: OK_PREVIEW })) as never)
    await waitFor(() => expect((screen.getByRole('button', { name: 'Send test' }) as HTMLButtonElement).disabled).toBe(false))
    await userEvent.click(screen.getByRole('button', { name: 'Send test' }))
    expect(await screen.findByText(/dialing SMTP server: connection refused/)).toBeTruthy()
  })

  it('asks before reverting, then deletes the override', async () => {
    const { del } = wire({ detail: { ...DETAIL, customised: true, body: 'Mine {{.TicketURL}}' } })
    await openEditor('ticket_created')
    await userEvent.click(screen.getByRole('button', { name: 'Revert to default' }))
    expect(del).not.toHaveBeenCalled()
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Revert' }))
    await waitFor(() => expect(del).toHaveBeenCalledWith('/admin/email-templates/ticket_created'))
  })

  it('offers no revert for a template that was never edited', async () => {
    wire()
    await openEditor('ticket_created')
    expect(screen.queryByRole('button', { name: 'Revert to default' })).toBeNull()
  })

  it('says plainly that a saved version is broken, and why', async () => {
    wire({ detail: BROKEN })
    await openEditor('password_reset')
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toMatch(/does not work with this release/i)
    expect(alert.textContent).toContain('must include {{.ResetURL}}')
    expect(screen.getByRole('button', { name: 'Revert to default' })).toBeTruthy()
  })
})

// Touch targets: 44 px on a coarse pointer, on this page only (the shared Button
// is unchanged). The real heights are measured in e2e/email-templates.spec.ts.
describe('touch targets', () => {
  it.each(['Save', 'Send test', 'All templates'])('%s is 44px on a touch screen', async (name) => {
    wire({ detail: { ...DETAIL, customised: true } })
    await openEditor('ticket_created')
    const cls = screen.getByRole('button', { name }).className
    expect(cls).toContain('pointer-coarse:min-h-11')
  })

  it('Revert to default and the variable chips are too', async () => {
    wire({ detail: { ...DETAIL, customised: true } })
    await openEditor('ticket_created')
    expect(screen.getByRole('button', { name: 'Revert to default' }).className).toContain('pointer-coarse:min-h-11')
    expect(screen.getByRole('button', { name: /\{\{\.TicketURL\}\}/ }).className).toContain('pointer-coarse:min-h-11')
  })

  it('does not change the desktop size', async () => {
    wire()
    await openEditor('ticket_created')
    expect(screen.getByRole('button', { name: 'Save' }).className).toContain('h-10')
  })
})

describe('the shared header and footer', () => {
  it('has a body and no subject', async () => {
    wire({ detail: SHELL })
    await openEditor('_header')
    expect(screen.queryByLabelText('Subject')).toBeNull()
    expect(screen.getByText(/shared by all mail/i)).toBeTruthy()
    expect(screen.getByRole('button', { name: /\{\{\.SiteName\}\}/ })).toBeTruthy()
    expect(screen.queryByText(/must include/i)).toBeNull()
  })

  it('saves with an empty subject', async () => {
    const { put } = wire({ detail: SHELL })
    await openEditor('_header')
    type(bodyField(), 'Acme Support\n')
    await waitFor(() => expect((saveButton()).disabled).toBe(false))
    await userEvent.click(saveButton())
    await waitFor(() => expect(put).toHaveBeenCalledWith('/admin/email-templates/_header', { subject: '', body: 'Acme Support\n' }))
  })
})


// ── the HTML part (#158, PR 2) ───────────────────────────────────────────────
//
// An optional HTML body beside the text one. Checked as you type like the rest,
// previewed in a sandboxed iframe, and sent only when there is some: a request
// without "html" is a request for no HTML part, which is what the server reads
// an absent key as.

const HTML_DETAIL = {
  ...DETAIL,
  customised: true,
  html: '<p>Hello</p><a href="{{.TicketURL}}">Open</a>',
  has_html: true,
  max_html_bytes: 65536,
}

const HTML_PREVIEW = { ...OK_PREVIEW, html: '<p>Hello</p><a href="https://x/tickets/0">Open</a>' }

const htmlField = () => screen.getByLabelText('HTML') as HTMLTextAreaElement

describe('the HTML part', () => {
  it('has its own field, empty for a template with no HTML', async () => {
    wire()
    await openEditor('ticket_created')
    expect(htmlField().value).toBe('')
    expect(screen.getByText(/Leave empty to send plain text only/i)).toBeTruthy()
  })

  it('shows the saved HTML', async () => {
    wire({ detail: HTML_DETAIL, preview: () => HTML_PREVIEW })
    await openEditor('ticket_created')
    expect(htmlField().value).toBe(HTML_DETAIL.html)
  })

  it('marks the templates that have HTML in the list', async () => {
    vi.spyOn(api, 'get').mockImplementation(((url: string) =>
      Promise.resolve({ data: url === '/admin/email-templates' ? LIST.map((t) => ({ ...t, has_html: t.name === 'guest_link' })) : [] })) as never)
    renderWithQuery(<EmailTemplatesPage />)
    const guest = await screen.findByRole('button', { name: /New guest link/ })
    expect(within(guest).getByText('HTML')).toBeTruthy()
    expect(within(screen.getByRole('button', { name: /Ticket received/ })).queryByText('HTML')).toBeNull()
  })

  it('sends the HTML with the draft to be checked, and leaves the key out when there is none', async () => {
    const { previewCalls } = wire()
    await openEditor('ticket_created')
    await waitFor(() => expect(previewCalls.length).toBeGreaterThan(0))
    expect(previewCalls[0].draft).not.toHaveProperty('html')

    type(htmlField(), '<p>{{.TicketURL}}</p>')
    await waitFor(() => expect(previewCalls.at(-1)?.draft.html).toBe('<p>{{.TicketURL}}</p>'))
  })

  it('saves the HTML with the text', async () => {
    const { put } = wire()
    await openEditor('ticket_created')
    type(htmlField(), '<p>{{.TicketURL}}</p>')
    await waitFor(() => expect(saveButton().disabled).toBe(false))
    await userEvent.click(saveButton())
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put).toHaveBeenCalledWith('/admin/email-templates/ticket_created', {
      subject: DETAIL.subject, body: DETAIL.body, html: '<p>{{.TicketURL}}</p>',
    })
  })

  it('removing the HTML is a save without the key, and counts as a change', async () => {
    const { put } = wire({ detail: HTML_DETAIL, preview: () => HTML_PREVIEW })
    await openEditor('ticket_created')
    await waitFor(() => expect(saveButton().disabled).toBe(true)) // unchanged
    type(htmlField(), '')
    await waitFor(() => expect(saveButton().disabled).toBe(false))
    await userEvent.click(saveButton())
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put.mock.calls[0][1]).toEqual({ subject: DETAIL.subject, body: DETAIL.body })
  })

  it('shows an HTML problem beside the HTML field with its place, and Save stays off', async () => {
    const { put } = wire({
      preview: (d) =>
        d.html?.includes('<script')
          ? { valid: false, errors: [{ field: 'html', line: 2, col: 1, message: '<script> is not allowed: mail clients remove scripts' }] }
          : OK_PREVIEW,
    })
    await openEditor('ticket_created')
    type(htmlField(), '<p>{{.TicketURL}}</p>\n<script>x</script>')
    const msg = await screen.findByText(/Line 2, column 1/)
    expect(msg.textContent).toContain('<script> is not allowed')
    expect(htmlField().getAttribute('aria-invalid')).toBe('true')
    expect(htmlField().getAttribute('aria-describedby')).toBe(msg.id)
    expect(bodyField().getAttribute('aria-invalid')).not.toBe('true')
    expect(saveButton().disabled).toBe(true)

    await userEvent.click(saveButton())
    fireEvent.submit(htmlField().closest('form') as HTMLFormElement)
    await new Promise((r) => setTimeout(r, 50))
    expect(put).not.toHaveBeenCalled()
  })

  it('shows the server\'s refusal of the HTML too', async () => {
    const { put } = wire()
    await openEditor('ticket_created')
    put.mockRejectedValue(
      refusal('invalid_template', 'html, line 1 column 1: a rule the page did not know', {
        field: 'html', line: 1, col: 1, errors: [{ field: 'html', line: 1, col: 1, message: 'a rule the page did not know' }],
      }),
    )
    type(htmlField(), '<p>{{.TicketURL}}</p>')
    await waitFor(() => expect(saveButton().disabled).toBe(false))
    await userEvent.click(saveButton())
    const msg = await screen.findByText(/Line 1, column 1/)
    expect(msg.textContent).toContain('a rule the page did not know')
    expect(htmlField().getAttribute('aria-invalid')).toBe('true')
  })

  it('sends the HTML in a test mail, and omits the key when there is none', async () => {
    const { post } = wire({ preview: () => HTML_PREVIEW })
    await openEditor('ticket_created')
    type(htmlField(), '<p>{{.TicketURL}}</p>')
    await waitFor(() => expect(saveButton().disabled).toBe(false))
    await userEvent.click(screen.getByRole('button', { name: 'Send test' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/admin/email-templates/ticket_created/test', {
        subject: DETAIL.subject, body: DETAIL.body, html: '<p>{{.TicketURL}}</p>',
      }),
    )
  })

  it('inserts a variable into whichever field was last used, the HTML included', async () => {
    wire()
    await openEditor('ticket_created')
    htmlField().focus()
    await userEvent.type(htmlField(), '<p></p>')
    htmlField().setSelectionRange(3, 3)
    await userEvent.click(screen.getByRole('button', { name: /\{\{\.TicketURL\}\}/ }))
    expect(htmlField().value).toBe('<p>{{.TicketURL}}</p>')
    expect(bodyField().value).toBe(DETAIL.body)
  })
})

describe('Start from a basic layout', () => {
  it('fills an empty HTML field with a starter that has the template\'s link, and saves nothing', async () => {
    const { put } = wire()
    await openEditor('ticket_created')
    await userEvent.click(screen.getByRole('button', { name: 'Start from a basic layout' }))
    expect(htmlField().value).toContain('{{.TicketURL}}')
    expect(htmlField().value).toContain('href="{{.TicketURL}}"')
    expect(put).not.toHaveBeenCalled()
    // It is a draft like any other: it has to be checked before it can be saved.
    expect(saveButton().disabled).toBe(true)
    await waitFor(() => expect(saveButton().disabled).toBe(false))
  })

  it('is for an empty field only: it never replaces what was written', async () => {
    wire({ detail: HTML_DETAIL, preview: () => HTML_PREVIEW })
    await openEditor('ticket_created')
    expect((screen.getByRole('button', { name: 'Start from a basic layout' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('gives the header and the footer the two halves of one layout', async () => {
    wire({ detail: SHELL })
    await openEditor('_header')
    await userEvent.click(screen.getByRole('button', { name: 'Start from a basic layout' }))
    const header = htmlField().value
    expect(header).toContain('{{.SiteName}}')
    expect(header).not.toMatch(/<\/table>/i)
    expect(header).not.toContain('{{.TicketURL}}')
  })

  it('keeps the starter free of anything the server refuses', async () => {
    wire()
    await openEditor('ticket_created')
    await userEvent.click(screen.getByRole('button', { name: 'Start from a basic layout' }))
    expect(htmlField().value).not.toMatch(/<script|<form|<iframe|\son\w+=|javascript:|<meta|<link/i)
  })
})

describe('the HTML preview', () => {
  const frame = () => screen.getByTitle('HTML preview') as HTMLIFrameElement

  it('renders in an iframe with an empty sandbox: no scripts, not the page\'s origin', async () => {
    wire({ detail: HTML_DETAIL, preview: () => HTML_PREVIEW })
    await openEditor('ticket_created')
    await screen.findByTitle('HTML preview')
    // sandbox="" and not sandbox="allow-something": every restriction on.
    expect(frame().getAttribute('sandbox')).toBe('')
    expect(frame().hasAttribute('src')).toBe(false)
    expect(frame().getAttribute('referrerpolicy')).toBe('no-referrer')
  })

  it('puts a Content-Security-Policy meta first in the document it shows', async () => {
    wire({ detail: HTML_DETAIL, preview: () => HTML_PREVIEW })
    await openEditor('ticket_created')
    await screen.findByTitle('HTML preview')
    const doc = frame().getAttribute('srcdoc') ?? ''
    expect(doc.startsWith(
      `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:">`,
    )).toBe(true)
    expect(doc).toContain(HTML_PREVIEW.html)
    // A click on a sample link goes nowhere.
    expect(doc).toContain('<base target="_blank">')
  })

  it('never lets what the server rendered into the page itself', async () => {
    const hostile = '<script id="hostile">window.__pwned = true</script><img src=x onerror="window.__pwned = true">'
    wire({ detail: HTML_DETAIL, preview: () => ({ ...OK_PREVIEW, html: hostile }) })
    await openEditor('ticket_created')
    await screen.findByTitle('HTML preview')
    expect(document.getElementById('hostile')).toBeNull()
    expect(document.querySelector('img[onerror]')).toBeNull()
    expect((window as unknown as { __pwned?: boolean }).__pwned).toBeUndefined()
    expect(frame().getAttribute('sandbox')).toBe('')
  })

  it('says remote images do not load here, and where to see the real thing', async () => {
    wire({ detail: HTML_DETAIL, preview: () => HTML_PREVIEW })
    await openEditor('ticket_created')
    expect((await screen.findByText(/Images from other sites do not load in this preview/i)).textContent).toMatch(/Send test/)
  })

  it('shows the text preview as before, beside it', async () => {
    wire({ detail: HTML_DETAIL, preview: () => HTML_PREVIEW })
    await openEditor('ticket_created')
    expect(await screen.findByText(/Open it:/, { selector: 'pre *, pre' })).toBeTruthy()
  })

  it('says the mail is plain text when there is no HTML, and shows no frame', async () => {
    wire()
    await openEditor('ticket_created')
    await screen.findByText(/Open it:/, { selector: 'pre *, pre' })
    expect(screen.queryByTitle('HTML preview')).toBeNull()
    expect(screen.getByText(/no HTML part: it is sent as plain text/i)).toBeTruthy()
  })
})

describe('touch targets for the HTML part', () => {
  it('Start from a basic layout is 44px on a touch screen', async () => {
    wire()
    await openEditor('ticket_created')
    expect(screen.getByRole('button', { name: 'Start from a basic layout' }).className).toContain('pointer-coarse:min-h-11')
  })
})
