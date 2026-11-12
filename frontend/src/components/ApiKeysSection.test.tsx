import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import { ApiKeysSection } from './ApiKeysSection'

// The page-level behaviour is in pages/account-api-keys.test.tsx; this is the
// component on its own, and the refusals a server can answer with.

function signedInAs(role: 'staff' | 'admin' | 'user', canCreate: boolean) {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 's@example.com', display_name: 'S', role, mfa_enabled: false,
      created_at: '', updated_at: '', can_create_api_keys: canCreate,
    },
  })
}

beforeEach(() => {
  vi.spyOn(api, 'get').mockImplementation(((url: string) =>
    Promise.resolve({
      data: url === '/me/api-keys/scopes' ? [{ scope: 'tickets:read', resource: 'tickets', action: 'read' }] : [],
    })) as never)
})
afterEach(() => vi.restoreAllMocks())

describe('ApiKeysSection', () => {
  it('shows the server’s reason when a key is refused, and keeps the form filled in', async () => {
    signedInAs('staff', true)
    const refusal = new AxiosError('x', '400', undefined, undefined, {
      status: 400, statusText: '', headers: {}, config: {} as never,
      data: { error: { code: 'invalid_scope', message: 'scope "users:read" is not available to a key owned by a staff account' } },
    })
    vi.spyOn(api, 'post').mockRejectedValue(refusal)
    renderWithQuery(<ApiKeysSection />)

    await userEvent.type(await screen.findByPlaceholderText(/key name/i), 'ci')
    await userEvent.click(await screen.findByRole('checkbox', { name: 'tickets:read' }))
    await userEvent.click(screen.getByRole('button', { name: 'Create key' }))

    expect((await screen.findByRole('alert')).textContent).toMatch(/not available to a key owned by a staff account/)
    expect((screen.getByPlaceholderText(/key name/i) as HTMLInputElement).value).toBe('ci')
  })

  it('needs a name and at least one permission before it will create', async () => {
    signedInAs('staff', true)
    renderWithQuery(<ApiKeysSection />)
    const create = await screen.findByRole('button', { name: 'Create key' })
    expect((create as HTMLButtonElement).disabled).toBe(true)
    await userEvent.type(screen.getByPlaceholderText(/key name/i), 'ci')
    expect((create as HTMLButtonElement).disabled).toBe(true)
    await userEvent.click(await screen.findByRole('checkbox', { name: 'tickets:read' }))
    expect((create as HTMLButtonElement).disabled).toBe(false)
  })

  it('renders nothing for a requester', () => {
    signedInAs('user', false)
    const { container } = renderWithQuery(<ApiKeysSection />)
    expect(container.textContent).toBe('')
  })
})
