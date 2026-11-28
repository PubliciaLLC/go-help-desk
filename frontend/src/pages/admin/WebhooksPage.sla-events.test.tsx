import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The SLA events (#670). sla.breached and sla.at_risk are ordinary events: a
// hook gets them under "All events" and can list them by name. The event list
// on this page must match the OpenAPI WebhookEvent enum, which the server's own
// test keeps equal to notify.WebhookEvents.

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

// npm test runs from frontend/, so the spec is one level up.
const OPENAPI = resolve(process.cwd(), '..', 'docs', 'api', 'openapi.yaml')

// The enum's entries, in the order the spec lists them, "*" included.
function openapiWebhookEvents(): string[] {
  const yaml = readFileSync(OPENAPI, 'utf8')
  const block = yaml.slice(yaml.indexOf('    WebhookEvent:'))
  const list = block.match(/enum: \[(.*)\]/)?.[1] ?? ''
  return list
    .split(',')
    .map((s) => s.trim().replace(/^'(.*)'$/, '$1'))
    .filter((s) => s !== '')
}

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

async function renderPage() {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/webhooks') return Promise.resolve({ data: [] })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {} } as any)
  renderWithQuery(<WebhooksPage />)
  await screen.findByRole('heading', { level: 1, name: 'Webhooks' })
  return { post }
}

const createForm = () => within(screen.getByRole('form', { name: 'New webhook' }))

// The event checkboxes: the fieldset that holds "All events".
function eventBoxes(form: ReturnType<typeof createForm>) {
  const fieldset = form.getByRole('checkbox', { name: 'All events' }).closest('fieldset') as HTMLElement
  return within(fieldset).getAllByRole('checkbox')
}

describe('the webhook event list', () => {
  it('lists exactly the events the OpenAPI enum names, under "*"', async () => {
    await renderPage()
    const form = createForm()
    const shown = eventBoxes(form)
      .map((box) => box.closest('label')?.textContent?.trim() ?? '')
      .filter((name) => name !== 'All events')

    const want = openapiWebhookEvents().filter((e) => e !== '*')
    expect(want).toContain('sla.breached')
    expect(want).toContain('sla.at_risk')
    expect([...shown].sort()).toEqual([...want].sort())
  })

  it('offers sla.breached and sla.at_risk, which All events covers', async () => {
    await renderPage()
    const form = createForm()
    await userEvent.click(form.getByRole('checkbox', { name: 'All events' }))
    for (const name of ['sla.breached', 'sla.at_risk']) {
      const box = form.getByRole('checkbox', { name }) as HTMLInputElement
      expect(box.checked).toBe(true)
      expect(box.disabled).toBe(true)
    }
  })

  it('sends them by name when chosen', async () => {
    const { post } = await renderPage()
    const form = createForm()
    await userEvent.type(form.getByLabelText('Webhook URL'), 'https://example.com/hook')
    await userEvent.click(form.getByRole('checkbox', { name: 'sla.at_risk' }))
    await userEvent.click(form.getByRole('checkbox', { name: 'sla.breached' }))
    await userEvent.click(form.getByRole('button', { name: /create/i }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0][1]).toMatchObject({ events: ['sla.breached', 'sla.at_risk'] })
  })
})
