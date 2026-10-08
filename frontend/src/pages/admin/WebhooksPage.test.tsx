import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, AxiosHeaders } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The admin page for webhook subscriptions (#157). The REST surface and the
// typed client already existed; an administrator following the docs looked for
// Admin -> Webhooks and found nothing.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/webhooks' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { WebhooksPage } from './WebhooksPage'

// What GET /admin/webhooks returns. There is no `secret` key: the API never
// sends one back.
const HOOKS = [
  {
    id: 'wh-1',
    url: 'https://hooks.slack.com/services/T000/B000/abc',
    events: ['ticket.created', 'ticket.replied'],
    enabled: true,
    created_at: '2026-01-01T00:00:00Z',
    payload_format: 'slack',
  },
  {
    id: 'wh-2',
    url: 'https://example.com/ingest',
    events: ['*'],
    enabled: false,
    created_at: '2026-01-02T00:00:00Z',
    payload_format: 'raw',
  },
]

beforeEach(() => {
  vi.restoreAllMocks()
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
})

function refusal(code: string, message: string, status = 400) {
  const headers = new AxiosHeaders()
  return new AxiosError('Request failed', 'ERR_BAD_REQUEST', { headers } as never, null, {
    status,
    statusText: 'Bad Request',
    headers,
    config: { headers } as never,
    data: { error: { code, message } },
  } as never)
}

async function renderPage(hooks: unknown[] = HOOKS) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/webhooks') return Promise.resolve({ data: hooks })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {} } as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} } as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const del = vi.spyOn(api, 'delete').mockResolvedValue({ data: undefined } as any)

  renderWithQuery(<WebhooksPage />)
  await screen.findByRole('heading', { level: 1, name: 'Webhooks' })
  return { post, patch, del }
}

function createForm() {
  return screen.getByRole('form', { name: 'New webhook' })
}

async function row(url: string) {
  const cell = await screen.findByText(url)
  return cell.closest('tr') as HTMLElement
}

describe('the webhook list', () => {
  it('shows every subscription, enabled or not, with its format and events', async () => {
    await renderPage()

    const on = await row(HOOKS[0].url)
    expect(within(on).getByText('Slack')).toBeTruthy()
    expect(within(on).getByText('ticket.created')).toBeTruthy()
    expect(within(on).getByText('ticket.replied')).toBeTruthy()
    expect(within(on).getByText('Enabled')).toBeTruthy()

    const off = await row(HOOKS[1].url)
    expect(within(off).getByText('Raw JSON')).toBeTruthy()
    expect(within(off).getByText('All events')).toBeTruthy()
    expect(within(off).getByText('Disabled')).toBeTruthy()
  })

  it('reads a legacy empty event list as all events, which is how delivery treats it', async () => {
    await renderPage([{ ...HOOKS[1], events: [] }])
    const r = await row(HOOKS[1].url)
    expect(within(r).getByText('All events')).toBeTruthy()
  })

  it('says so when there are none', async () => {
    await renderPage([])
    expect(await screen.findByText(/no webhooks yet/i)).toBeTruthy()
  })
})

describe('creating a webhook', () => {
  it('sends the URL, secret, format and chosen events, then clears the secret', async () => {
    const { post } = await renderPage()
    const form = within(createForm())

    await userEvent.type(form.getByLabelText('Webhook URL'), 'https://hooks.slack.com/services/X')
    await userEvent.type(form.getByLabelText('Secret'), 's3cret')
    await userEvent.selectOptions(form.getByLabelText('Payload format'), 'slack')
    await userEvent.click(form.getByRole('checkbox', { name: 'ticket.replied' }))
    await userEvent.click(form.getByRole('checkbox', { name: 'ticket.created' }))
    await userEvent.click(form.getByRole('button', { name: /create/i }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith('/admin/webhooks', {
      url: 'https://hooks.slack.com/services/X',
      secret: 's3cret',
      payload_format: 'slack',
      // In the order the server lists them, not the order they were ticked.
      events: ['ticket.created', 'ticket.replied'],
    })

    // The secret does not linger in the DOM once it has been sent.
    await waitFor(() =>
      expect((within(createForm()).getByLabelText('Secret') as HTMLInputElement).value).toBe('')
    )
  })

  it('keeps the secret field masked and out of autofill', async () => {
    await renderPage()
    const secret = within(createForm()).getByLabelText('Secret') as HTMLInputElement
    expect(secret.type).toBe('password')
    expect(secret.autocomplete).toBe('new-password')
  })

  it('sends no secret key when none was typed, and defaults to raw', async () => {
    const { post } = await renderPage()
    const form = within(createForm())

    await userEvent.type(form.getByLabelText('Webhook URL'), 'https://example.com/hook')
    await userEvent.click(form.getByRole('checkbox', { name: 'All events' }))
    await userEvent.click(form.getByRole('button', { name: /create/i }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0][1]).toEqual({
      url: 'https://example.com/hook',
      payload_format: 'raw',
      events: ['*'],
    })
  })

  it('offers exactly the formats the server accepts', async () => {
    await renderPage()
    const select = within(createForm()).getByLabelText('Payload format') as HTMLSelectElement
    expect(Array.from(select.options).map((o) => o.value)).toEqual([
      'raw',
      'slack',
      'teams',
      'discord',
      'jira',
    ])
  })

  it('will not submit until there is a URL and at least one event, which the server would refuse', async () => {
    await renderPage()
    const form = within(createForm())
    const button = form.getByRole('button', { name: /create/i })
    expect((button as HTMLButtonElement).disabled).toBe(true)

    await userEvent.type(form.getByLabelText('Webhook URL'), 'https://example.com/hook')
    expect((button as HTMLButtonElement).disabled).toBe(true)

    await userEvent.click(form.getByRole('checkbox', { name: 'ticket.closed' }))
    expect((button as HTMLButtonElement).disabled).toBe(false)
  })

  it('names the address problem when the server refuses the URL, and keeps what was typed', async () => {
    const { post } = await renderPage()
    post.mockRejectedValue(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      refusal('invalid_url', 'refusing to connect to private address 10.0.0.5') as any
    )
    const form = within(createForm())

    await userEvent.type(form.getByLabelText('Webhook URL'), 'http://10.0.0.5/hook')
    await userEvent.click(form.getByRole('checkbox', { name: 'All events' }))
    await userEvent.click(form.getByRole('button', { name: /create/i }))

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toMatch(/url was refused/i)
    expect(alert.textContent).toContain('refusing to connect to private address 10.0.0.5')
    expect((form.getByLabelText('Webhook URL') as HTMLInputElement).value).toBe('http://10.0.0.5/hook')
  })

  it('shows any other refusal as the server worded it', async () => {
    const { post } = await renderPage()
    post.mockRejectedValue(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      refusal('invalid_event_name', 'unknown event "ticket.creatd"') as any
    )
    const form = within(createForm())

    await userEvent.type(form.getByLabelText('Webhook URL'), 'https://example.com/hook')
    await userEvent.click(form.getByRole('checkbox', { name: 'All events' }))
    await userEvent.click(form.getByRole('button', { name: /create/i }))

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('unknown event "ticket.creatd"')
    expect(alert.textContent).not.toMatch(/url was refused/i)
  })

  it('creates enabled by default', async () => {
    await renderPage()
    const form = within(createForm())
    const enabledCheckbox = form.getByRole('checkbox', { name: 'Enabled' }) as HTMLInputElement
    expect(enabledCheckbox.checked).toBe(true)
  })

  it('creates a disabled hook when Enabled is unticked', async () => {
    const { post } = await renderPage()
    const form = within(createForm())

    await userEvent.click(form.getByRole('checkbox', { name: 'Enabled' }))
    await userEvent.type(form.getByLabelText('Webhook URL'), 'https://example.com/hook')
    await userEvent.click(form.getByRole('checkbox', { name: 'All events' }))
    await userEvent.click(form.getByRole('button', { name: /create/i }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith('/admin/webhooks', {
      url: 'https://example.com/hook',
      payload_format: 'raw',
      events: ['*'],
      enabled: false,
    })
  })
})

describe('enabling and disabling', () => {
  it('turns an enabled webhook off without touching anything else', async () => {
    const { patch } = await renderPage()
    const r = await row(HOOKS[0].url)
    await userEvent.click(within(r).getByRole('button', { name: 'Disable' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/webhooks/wh-1', { enabled: false })
  })

  it('turns a disabled webhook back on', async () => {
    const { patch } = await renderPage()
    const r = await row(HOOKS[1].url)
    await userEvent.click(within(r).getByRole('button', { name: 'Enable' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/webhooks/wh-2', { enabled: true })
  })
})

describe('editing a webhook', () => {
  async function openEditor(url: string) {
    const r = await row(url)
    await userEvent.click(within(r).getByRole('button', { name: 'Edit' }))
    return within(screen.getByRole('form', { name: 'Edit webhook' }))
  }

  it('starts from the stored values, with the secret blank', async () => {
    await renderPage()
    const form = await openEditor(HOOKS[0].url)

    expect((form.getByLabelText('Webhook URL') as HTMLInputElement).value).toBe(HOOKS[0].url)
    expect((form.getByLabelText('Payload format') as HTMLSelectElement).value).toBe('slack')
    expect((form.getByRole('checkbox', { name: 'ticket.created' }) as HTMLInputElement).checked).toBe(true)
    expect((form.getByRole('checkbox', { name: 'ticket.replied' }) as HTMLInputElement).checked).toBe(true)
    expect((form.getByRole('checkbox', { name: 'ticket.closed' }) as HTMLInputElement).checked).toBe(false)
    expect((form.getByLabelText('Secret') as HTMLInputElement).value).toBe('')
  })

  it('leaves the stored secret alone when the field is left empty', async () => {
    const { patch } = await renderPage()
    const form = await openEditor(HOOKS[0].url)

    await userEvent.selectOptions(form.getByLabelText('Payload format'), 'discord')
    await userEvent.click(form.getByRole('checkbox', { name: 'ticket.closed' }))
    await userEvent.click(form.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    const [path, body] = patch.mock.calls[0]
    expect(path).toBe('/admin/webhooks/wh-1')
    expect(body).toEqual({
      url: HOOKS[0].url,
      payload_format: 'discord',
      events: ['ticket.created', 'ticket.replied', 'ticket.closed'],
    })
    expect(body).not.toHaveProperty('secret')
  })

  it('replaces the secret only when a new one is typed', async () => {
    const { patch } = await renderPage()
    const form = await openEditor(HOOKS[0].url)

    await userEvent.type(form.getByLabelText('Secret'), 'rotated')
    await userEvent.click(form.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch.mock.calls[0][1]).toMatchObject({ secret: 'rotated' })
  })

  it('opens a hook subscribed to everything with All events ticked, and saves it as "*"', async () => {
    const { patch } = await renderPage()
    const form = await openEditor(HOOKS[1].url)

    expect((form.getByRole('checkbox', { name: 'All events' }) as HTMLInputElement).checked).toBe(true)
    await userEvent.click(form.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch.mock.calls[0][1]).toMatchObject({ events: ['*'] })
  })

  it('opens a legacy hook with no events as All events, so saving does not send the empty list the server refuses', async () => {
    const { patch } = await renderPage([{ ...HOOKS[1], events: [] }])
    const form = await openEditor(HOOKS[1].url)

    expect((form.getByRole('checkbox', { name: 'All events' }) as HTMLInputElement).checked).toBe(true)
    await userEvent.click(form.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch.mock.calls[0][1]).toMatchObject({ events: ['*'] })
  })

  it('names the address problem when the new URL is refused', async () => {
    const { patch } = await renderPage()
    patch.mockRejectedValue(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      refusal('invalid_url', 'refusing to connect to loopback address 127.0.0.1') as any
    )
    const form = await openEditor(HOOKS[0].url)

    await userEvent.clear(form.getByLabelText('Webhook URL'))
    await userEvent.type(form.getByLabelText('Webhook URL'), 'http://localhost:9200/hook')
    await userEvent.click(form.getByRole('button', { name: 'Save' }))

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toMatch(/url was refused/i)
    expect(alert.textContent).toContain('refusing to connect to loopback address 127.0.0.1')
  })

  it('closes without saving on Cancel', async () => {
    const { patch } = await renderPage()
    const form = await openEditor(HOOKS[0].url)
    await userEvent.click(form.getByRole('button', { name: 'Cancel' }))

    expect(screen.queryByRole('form', { name: 'Edit webhook' })).toBeNull()
    expect(patch).not.toHaveBeenCalled()
  })

  it('the edit form has no Enabled checkbox', async () => {
    await renderPage()
    const form = await openEditor(HOOKS[0].url)
    expect(form.queryByRole('checkbox', { name: 'Enabled' })).toBeNull()
  })
})

describe('deleting a webhook', () => {
  it('asks first, then deletes', async () => {
    const { del } = await renderPage()
    const r = await row(HOOKS[0].url)
    await userEvent.click(within(r).getByRole('button', { name: 'Delete' }))

    expect(del).not.toHaveBeenCalled()
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(del).toHaveBeenCalledTimes(1))
    expect(del).toHaveBeenCalledWith('/admin/webhooks/wh-1')
  })

  it('does nothing when the confirmation is cancelled', async () => {
    const { del } = await renderPage()
    const r = await row(HOOKS[0].url)
    await userEvent.click(within(r).getByRole('button', { name: 'Delete' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))

    expect(del).not.toHaveBeenCalled()
  })
})
