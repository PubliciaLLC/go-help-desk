import { describe, expect, it, vi, beforeEach } from 'vitest'
import { api } from './client'
import { createMyAPIKey, deleteMyAPIKey, listMyAPIKeys, listMyAPIKeyScopes } from './apikeys'

beforeEach(() => vi.restoreAllMocks())

describe('the caller’s own API keys', () => {
  it('lists from /me/api-keys, not the admin route', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({ data: [{ id: 'k-1' }] })
    expect(await listMyAPIKeys()).toEqual([{ id: 'k-1' }])
    expect(get).toHaveBeenCalledWith('/me/api-keys')
  })

  it('asks for the scopes of its own role', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({ data: [] })
    await listMyAPIKeyScopes()
    expect(get).toHaveBeenCalledWith('/me/api-keys/scopes')
  })

  it('creates with exactly what it was given, and never a user_id', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: { id: 'k-2', name: 'ci', token: 'GHD_x' } })
    const out = await createMyAPIKey({ name: 'ci', scopes: ['tickets:read'] })
    expect(out.token).toBe('GHD_x')
    expect(post).toHaveBeenCalledWith('/me/api-keys', { name: 'ci', scopes: ['tickets:read'] })
  })

  it('revokes by id', async () => {
    const del = vi.spyOn(api, 'delete').mockResolvedValue({ data: undefined })
    await deleteMyAPIKey('k-1')
    expect(del).toHaveBeenCalledWith('/me/api-keys/k-1')
  })
})
