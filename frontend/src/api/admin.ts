import { api } from './client'
import type { User, AdminUser, Group, Category, TicketType, TicketItem, Status, APIKey, WebhookConfig, Tag, FieldDef, Assignment, ScopeType } from './types'
import type { Role } from './types'

// ── Site config (public) ──────────────────────────────────────────────────────

export interface SiteConfig {
  name: string
  logo_url: string
  version: string
}

export async function getSiteConfig(): Promise<SiteConfig> {
  const res = await api.get<SiteConfig>('/site')
  return res.data
}

// ── Users ────────────────────────────────────────────────────────────────────

export async function listUsers(limit = 200, offset = 0): Promise<AdminUser[]> {
  const res = await api.get<AdminUser[]>('/admin/users', { params: { limit, offset } })
  return res.data
}

export async function getUser(id: string): Promise<AdminUser> {
  const res = await api.get<AdminUser>(`/admin/users/${id}`)
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

export async function updateUser(id: string, patch: {
  display_name?: string
  email?: string
  role?: Role
  disabled?: boolean
  reset_mfa?: boolean
}): Promise<AdminUser> {
  const res = await api.patch<AdminUser>(`/admin/users/${id}`, patch)
  return res.data
}

export async function adminResetPassword(id: string, newPassword: string): Promise<void> {
  await api.post(`/admin/users/${id}/password`, { new_password: newPassword })
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

export async function updateGroup(id: string, patch: Partial<Pick<Group, 'name' | 'description'>>): Promise<Group> {
  const res = await api.patch<Group>(`/admin/groups/${id}`, patch)
  return res.data
}

export async function deleteGroup(id: string): Promise<void> {
  await api.delete(`/admin/groups/${id}`)
}

export async function listGroupMembers(groupId: string): Promise<User[]> {
  const res = await api.get<User[]>(`/admin/groups/${groupId}/members`)
  return res.data
}

export async function addGroupMember(groupId: string, userId: string): Promise<void> {
  await api.post(`/admin/groups/${groupId}/members`, { user_id: userId })
}

export async function removeGroupMember(groupId: string, userId: string): Promise<void> {
  await api.delete(`/admin/groups/${groupId}/members/${userId}`)
}

export interface GroupScope {
  group_id: string
  category_id: string
  type_id?: string
}

export async function listGroupScopes(groupId: string): Promise<GroupScope[]> {
  const res = await api.get<GroupScope[]>(`/admin/groups/${groupId}/scopes`)
  return res.data
}

export async function addGroupScope(groupId: string, input: { category_id: string; type_id?: string }): Promise<void> {
  await api.post(`/admin/groups/${groupId}/scopes`, input)
}

export async function removeGroupScope(groupId: string, input: { category_id: string; type_id?: string }): Promise<void> {
  await api.delete(`/admin/groups/${groupId}/scopes`, { data: input })
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

export async function updateCategory(id: string, patch: { name?: string; sort_order?: number; active?: boolean }): Promise<Category> {
  const res = await api.patch<Category>(`/admin/categories/${id}`, patch)
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

export async function updateType(categoryId: string, typeId: string, patch: { name?: string; sort_order?: number }): Promise<TicketType> {
  const res = await api.patch<TicketType>(`/admin/categories/${categoryId}/types/${typeId}`, patch)
  return res.data
}

export async function deleteType(categoryId: string, typeId: string): Promise<void> {
  await api.delete(`/admin/categories/${categoryId}/types/${typeId}`)
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

export async function updateItem(categoryId: string, typeId: string, itemId: string, patch: { name?: string; sort_order?: number }): Promise<TicketItem> {
  const res = await api.patch<TicketItem>(`/admin/categories/${categoryId}/types/${typeId}/items/${itemId}`, patch)
  return res.data
}

export async function deleteItem(categoryId: string, typeId: string, itemId: string): Promise<void> {
  await api.delete(`/admin/categories/${categoryId}/types/${typeId}/items/${itemId}`)
}

// ── Statuses ─────────────────────────────────────────────────────────────────

export async function listStatuses(): Promise<Status[]> {
  const res = await api.get<Status[]>('/admin/statuses')
  return res.data
}

export async function createStatus(input: { name: string; sort_order?: number; color?: string }): Promise<Status> {
  const res = await api.post<Status>('/admin/statuses', input)
  return res.data
}

export async function updateStatus(id: string, patch: { name?: string; sort_order?: number; color?: string; active?: boolean }): Promise<Status> {
  const res = await api.patch<Status>(`/admin/statuses/${id}`, patch)
  return res.data
}

export async function deleteStatus(id: string): Promise<void> {
  await api.delete(`/admin/statuses/${id}`)
}

// ── SAML ─────────────────────────────────────────────────────────────────────

export interface SAMLConfig {
  configured: boolean
  metadata_url: string
  cert_pem: string
  sp_metadata_url: string
}

export async function getSAMLConfig(): Promise<SAMLConfig> {
  const res = await api.get<SAMLConfig>('/admin/saml')
  return res.data
}

export async function saveSAMLConfig(input: {
  metadata_url: string
  cert_pem: string
  key_pem: string
}): Promise<{ warning?: string }> {
  const res = await api.put<{ warning?: string }>('/admin/saml', input)
  return res.data ?? {}
}

// ── Logo ─────────────────────────────────────────────────────────────────────

export async function uploadLogo(file: File): Promise<{ url: string }> {
  const form = new FormData()
  form.append('logo', file)
  const res = await api.post<{ url: string }>('/admin/settings/logo', form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
  return res.data
}

export async function deleteLogo(): Promise<void> {
  await api.delete('/admin/settings/logo')
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

// ── Tags (admin) ──────────────────────────────────────────────────────────────

export async function listAllTags(): Promise<Tag[]> {
  const res = await api.get<Tag[]>('/admin/tags')
  return res.data
}

export async function createTag(name: string): Promise<Tag> {
  const res = await api.post<Tag>('/admin/tags', { name })
  return res.data
}

export async function deleteTag(id: string): Promise<void> {
  await api.delete(`/admin/tags/${id}`)
}

export async function restoreTag(id: string): Promise<void> {
  await api.post(`/admin/tags/${id}/restore`)
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

// ── Custom field definitions ──────────────────────────────────────────────────

export async function listFieldDefs(): Promise<FieldDef[]> {
  const res = await api.get<FieldDef[]>('/admin/custom-fields')
  return res.data
}

export async function createFieldDef(input: {
  name: string
  field_type: string
  options?: string[]
  sort_order?: number
}): Promise<FieldDef> {
  const res = await api.post<FieldDef>('/admin/custom-fields', input)
  return res.data
}

export async function updateFieldDef(
  id: string,
  patch: Partial<Pick<FieldDef, 'name' | 'field_type' | 'options' | 'sort_order' | 'active'>>
): Promise<FieldDef> {
  const res = await api.patch<FieldDef>(`/admin/custom-fields/${id}`, patch)
  return res.data
}

// ── Custom field assignments (CTI-level) ──────────────────────────────────────

export async function listAssignments(scopeType: ScopeType, scopeId: string): Promise<Assignment[]> {
  const path = scopeType === 'category'
    ? `/admin/categories/${scopeId}/fields`
    : scopeType === 'type'
    ? `/admin/categories/_/types/${scopeId}/fields`
    : `/admin/categories/_/types/_/items/${scopeId}/fields`
  const res = await api.get<Assignment[]>(path)
  return res.data
}

// Scoped assignment helpers that use the full CTI path (preferred over listAssignments for types/items)
export async function listCategoryAssignments(categoryId: string): Promise<Assignment[]> {
  const res = await api.get<Assignment[]>(`/admin/categories/${categoryId}/fields`)
  return res.data
}

export async function listTypeAssignments(categoryId: string, typeId: string): Promise<Assignment[]> {
  const res = await api.get<Assignment[]>(`/admin/categories/${categoryId}/types/${typeId}/fields`)
  return res.data
}

export async function listItemAssignments(categoryId: string, typeId: string, itemId: string): Promise<Assignment[]> {
  const res = await api.get<Assignment[]>(`/admin/categories/${categoryId}/types/${typeId}/items/${itemId}/fields`)
  return res.data
}

export async function createCategoryAssignment(
  categoryId: string,
  input: { field_def_id: string; sort_order?: number; visible_on_new?: boolean; required_on_new?: boolean }
): Promise<Assignment> {
  const res = await api.post<Assignment>(`/admin/categories/${categoryId}/fields`, input)
  return res.data
}

export async function createTypeAssignment(
  categoryId: string,
  typeId: string,
  input: { field_def_id: string; sort_order?: number; visible_on_new?: boolean; required_on_new?: boolean }
): Promise<Assignment> {
  const res = await api.post<Assignment>(`/admin/categories/${categoryId}/types/${typeId}/fields`, input)
  return res.data
}

export async function createItemAssignment(
  categoryId: string,
  typeId: string,
  itemId: string,
  input: { field_def_id: string; sort_order?: number; visible_on_new?: boolean; required_on_new?: boolean }
): Promise<Assignment> {
  const res = await api.post<Assignment>(`/admin/categories/${categoryId}/types/${typeId}/items/${itemId}/fields`, input)
  return res.data
}

export async function updateCategoryAssignment(
  categoryId: string,
  assignmentId: string,
  patch: { sort_order?: number; visible_on_new?: boolean; required_on_new?: boolean }
): Promise<Assignment> {
  const res = await api.patch<Assignment>(`/admin/categories/${categoryId}/fields/${assignmentId}`, patch)
  return res.data
}

export async function updateTypeAssignment(
  categoryId: string,
  typeId: string,
  assignmentId: string,
  patch: { sort_order?: number; visible_on_new?: boolean; required_on_new?: boolean }
): Promise<Assignment> {
  const res = await api.patch<Assignment>(`/admin/categories/${categoryId}/types/${typeId}/fields/${assignmentId}`, patch)
  return res.data
}

export async function updateItemAssignment(
  categoryId: string,
  typeId: string,
  itemId: string,
  assignmentId: string,
  patch: { sort_order?: number; visible_on_new?: boolean; required_on_new?: boolean }
): Promise<Assignment> {
  const res = await api.patch<Assignment>(`/admin/categories/${categoryId}/types/${typeId}/items/${itemId}/fields/${assignmentId}`, patch)
  return res.data
}

export async function deleteCategoryAssignment(categoryId: string, assignmentId: string): Promise<void> {
  await api.delete(`/admin/categories/${categoryId}/fields/${assignmentId}`)
}

export async function deleteTypeAssignment(categoryId: string, typeId: string, assignmentId: string): Promise<void> {
  await api.delete(`/admin/categories/${categoryId}/types/${typeId}/fields/${assignmentId}`)
}

export async function deleteItemAssignment(categoryId: string, typeId: string, itemId: string, assignmentId: string): Promise<void> {
  await api.delete(`/admin/categories/${categoryId}/types/${typeId}/items/${itemId}/fields/${assignmentId}`)
}

// ── CTI group scope (read from CTI perspective) ───────────────────────────────

export async function listGroupsForCategory(categoryId: string): Promise<Group[]> {
  const res = await api.get<Group[]>(`/admin/categories/${categoryId}/groups`)
  return res.data
}

export async function listGroupsForType(categoryId: string, typeId: string): Promise<Group[]> {
  const res = await api.get<Group[]>(`/admin/categories/${categoryId}/types/${typeId}/groups`)
  return res.data
}
