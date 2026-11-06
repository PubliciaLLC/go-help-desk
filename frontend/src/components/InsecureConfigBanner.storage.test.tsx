import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { InsecureConfigBanner } from './InsecureConfigBanner'

// #458: when the server cannot write to ATTACHMENT_DIR, uploads are refused and
// administrators are told why, in words that say what to fix.

const scanning = {
  policy: 'required',
  configured: true,
  reachable: true,
  effect: 'Attachments are scanned before being accepted.',
  policy_explicit: true,
  stale_off: false,
}

function mockWarnings(attachment_storage: unknown) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/security-warnings') {
      return Promise.resolve({
        data: { insecure_secrets: [], attachment_scanning: scanning, attachment_storage },
      })
    }
    return Promise.resolve({ data: {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

describe('the attachment storage warning', () => {
  it('names the problem and the fix when the directory is not writable', async () => {
    mockWarnings({ writable: false, problem: 'permission denied' })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const alert = await screen.findByRole('region', { name: 'Server notices' })
    const text = alert.textContent ?? ''
    expect(text).toMatch(/attachment uploads are disabled/i)
    expect(text).toContain('permission denied')
    expect(text).toContain('ATTACHMENT_DIR')
    expect(text).toMatch(/still be downloaded/i)
  })

  it('says nothing when the directory is writable', async () => {
    mockWarnings({ writable: true, problem: '' })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()
  })

  it('says nothing to a non-admin', () => {
    mockWarnings({ writable: false, problem: 'permission denied' })
    renderWithQuery(<InsecureConfigBanner isAdmin={false} />)

    expect(api.get).not.toHaveBeenCalled()
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()
  })
})
