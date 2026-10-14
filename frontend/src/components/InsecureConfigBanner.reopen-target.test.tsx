import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { InsecureConfigBanner } from './InsecureConfigBanner'

// #419: a deactivated reopen target sends reopened tickets to New. The admin
// is told, and asked to pick an active status.

const scanning = {
  policy: 'required',
  configured: true,
  reachable: true,
  effect: 'Attachments are scanned before being accepted.',
  policy_explicit: true,
  stale_off: false,
}

function mockWarnings(reopen_target: unknown) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/security-warnings') {
      return Promise.resolve({
        data: {
          insecure_secrets: [],
          attachment_scanning: scanning,
          attachment_storage: { writable: true, problem: '' },
          reopen_target,
        },
      })
    }
    return Promise.resolve({ data: {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

describe('the reopen target warning', () => {
  it('names the status and asks for an active one', async () => {
    mockWarnings({ inactive: true, name: 'Awaiting parts' })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const alert = await screen.findByRole('alert')
    const text = alert.textContent ?? ''
    expect(text).toContain('Awaiting parts')
    expect(text).toMatch(/inactive/i)
    expect(text).toMatch(/New/)
    expect(text).toMatch(/active status/i)
  })

  it('says nothing when the target is active', async () => {
    mockWarnings({ inactive: false })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('says nothing to a non-admin', () => {
    mockWarnings({ inactive: true, name: 'Awaiting parts' })
    renderWithQuery(<InsecureConfigBanner isAdmin={false} />)

    expect(api.get).not.toHaveBeenCalled()
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
