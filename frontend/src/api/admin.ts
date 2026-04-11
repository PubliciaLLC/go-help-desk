import { api } from './client'
import type { User, Group, Category, TicketType, TicketItem, Status, APIKey, WebhookConfig } from './types'
import type { Role } from './types'

// ── Users ────────────────────────────────────────────────────────────────────

export async function listUsers(limit = 50, offset = 0): Promise<User[]> {
  const res = await api.get<User[]>('/admin/users', { params: { limit, offset } })
  return res.data
}

export async function createUser(input: {
  email: string
  display_name: string
  role: Role
  password?: string
}): Promise<User> {
  const res = await api.post<User>('/admin/users', input)
  return res.data
}

export async function updateUser(id: string, patch: Partial<Pick<User, 'email' | 'display_name' | 'role'>>): Promise<User> {
  const res = await api.patch<User>(`/admin/users/${id}`, patch)
  return res.data
}

export async function deleteUser(id: string): Promise<void> {
  await api.delete(`/admin/users/${id}`)
}

// ── Groups ───────────────────────────────────────────────────────────────────

export async function listGroups(): Promise<Group[]> {
  const res = await api.get<Group[]>('/admin/groups')
  return res.data
}

export async function createGroup(input: { name: string; description?: string }): Promise<Group> {
  const res = await api.post<Group>('/admin/groups', input)
  return res.data
}

export async function deleteGroup(id: string): Promise<void> {
  await api.delete(`/admin/groups/${id}`)
}

export async function addGroupMember(groupId: string, userId: string): Promise<void> {
  await api.post(`/admin/groups/${groupId}/members`, { user_id: userId })
}

export async function removeGroupMember(groupId: string, userId: string): Promise<void> {
  await api.delete(`/admin/groups/${groupId}/members/${userId}`)
}

// ── Categories ───────────────────────────────────────────────────────────────

export async function listCategories(): Promise<Category[]> {
  const res = await api.get<Category[]>('/admin/categories')
  return res.data
}

export async function createCategory(input: { name: string; sort_order?: number }): Promise<Category> {
  const res = await api.post<Category>('/admin/categories', input)
  return res.data
}

export async function deleteCategory(id: string): Promise<void> {
  await api.delete(`/admin/categories/${id}`)
}

export async function listTypes(categoryId: string): Promise<TicketType[]> {
  const res = await api.get<TicketType[]>(`/admin/categories/${categoryId}/types`)
  return res.data
}

export async function createType(categoryId: string, input: { name: string; sort_order?: number }): Promise<TicketType> {
  const res = await api.post<TicketType>(`/admin/categories/${categoryId}/types`, input)
  return res.data
}

export async function listItems(categoryId: string, typeId: string): Promise<TicketItem[]> {
  const res = await api.get<TicketItem[]>(`/admin/categories/${categoryId}/types/${typeId}/items`)
  return res.data
}

export async function createItem(
  categoryId: string,
  typeId: string,
  input: { name: string; sort_order?: number }
): Promise<TicketItem> {
  const res = await api.post<TicketItem>(
    `/admin/categories/${categoryId}/types/${typeId}/items`,
    input
  )
  return res.data
}

// ── Statuses ─────────────────────────────────────────────────────────────────

export async function listStatuses(): Promise<Status[]> {
  const res = await api.get<Status[]>('/admin/statuses')
  return res.data
}

// ── Settings ─────────────────────────────────────────────────────────────────

export async function getSettings(): Promise<Record<string, unknown>> {
  const res = await api.get<Record<string, unknown>>('/admin/settings')
  return res.data
}

export async function updateSettings(patch: Record<string, unknown>): Promise<void> {
  await api.patch('/admin/settings', patch)
}

// ── API Keys ─────────────────────────────────────────────────────────────────

export async function listAPIKeys(): Promise<APIKey[]> {
  const res = await api.get<APIKey[]>('/admin/api-keys')
  return res.data
}

export async function createAPIKey(input: {
  name: string
  scopes?: string[]
  expires_at?: string
}): Promise<APIKey & { raw_token: string }> {
  const res = await api.post<APIKey & { raw_token: string }>('/admin/api-keys', input)
  return res.data
}

export async function deleteAPIKey(id: string): Promise<void> {
  await api.delete(`/admin/api-keys/${id}`)
}

// ── Webhooks ─────────────────────────────────────────────────────────────────

export async function listWebhooks(): Promise<WebhookConfig[]> {
  const res = await api.get<WebhookConfig[]>('/admin/webhooks')
  return res.data
}

export async function createWebhook(input: {
  url: string
  events?: string[]
  secret?: string
}): Promise<WebhookConfig> {
  const res = await api.post<WebhookConfig>('/admin/webhooks', input)
  return res.data
}

export async function updateWebhook(
  id: string,
  patch: Partial<Pick<WebhookConfig, 'url' | 'events' | 'secret' | 'enabled'>>
): Promise<WebhookConfig> {
  const res = await api.patch<WebhookConfig>(`/admin/webhooks/${id}`, patch)
  return res.data
}

export async function deleteWebhook(id: string): Promise<void> {
  await api.delete(`/admin/webhooks/${id}`)
}
