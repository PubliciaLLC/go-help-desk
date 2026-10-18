import { api } from './client'
import type { MyAPIKey, ScopeInfo } from './types'

// The caller's own API keys (#450). Its own module: these are /me routes for
// staff and administrators, not the admin page's /admin/api-keys.

export async function listMyAPIKeys(): Promise<MyAPIKey[]> {
  const res = await api.get<MyAPIKey[]>('/me/api-keys')
  return res.data
}

/** The scopes the caller's role can put on a key. */
export async function listMyAPIKeyScopes(): Promise<ScopeInfo[]> {
  const res = await api.get<ScopeInfo[]>('/me/api-keys/scopes')
  return res.data
}

export async function createMyAPIKey(input: {
  name: string
  scopes: string[]
  expires_at?: string
}): Promise<{ id: string; name: string; token: string }> {
  const res = await api.post<{ id: string; name: string; token: string }>('/me/api-keys', input)
  return res.data
}

export async function deleteMyAPIKey(id: string): Promise<void> {
  await api.delete(`/me/api-keys/${id}`)
}
