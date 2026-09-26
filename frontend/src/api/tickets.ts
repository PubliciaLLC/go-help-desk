import { api } from './client'
import type { Ticket, Reply, TicketLink, LinkType, Tag, Attachment, Category, TicketType, TicketItem, StatusHistoryEntry, Assignment, TicketFieldValue, CannedResponse } from './types'

export interface CreateTicketInput {
  subject: string
  description: string
  category_id: string
  type_id?: string
  item_id?: string
  priority?: string
  guest_email?: string
  guest_name?: string
  guest_phone?: string
  custom_fields?: Record<string, string>
}

export type TicketScope = 'mine' | 'unassigned' | 'all'

export async function listTickets(params?: {
  assignee_group_id?: string
  q?: string
  scope?: TicketScope
  reporter_id?: string
  limit?: number
  offset?: number
}): Promise<Ticket[]> {
  const res = await api.get<Ticket[]>('/tickets', { params })
  return res.data ?? []
}

export async function createTicket(input: CreateTicketInput): Promise<Ticket> {
  const res = await api.post<Ticket>('/tickets', input)
  return res.data
}

export async function getTicket(id: string): Promise<Ticket> {
  const res = await api.get<Ticket>(`/tickets/${id}`)
  return res.data
}

export async function updateTicket(
  id: string,
  patch: {
    status_id?: string
    assignee_user_id?: string
    assignee_group_id?: string
    // Unassigns the ticket. Needed because the two fields above cannot express
    // "nobody": an explicit null and an omitted key are indistinguishable to
    // the server, which decodes both into a nil *uuid.UUID.
    clear_assignee?: boolean
    category_id?: string
    type_id?: string | null
    item_id?: string | null
  }
): Promise<Ticket> {
  const res = await api.patch<Ticket>(`/tickets/${id}`, patch)
  return res.data
}

export async function resolveTicket(id: string, notes?: string): Promise<Ticket> {
  const res = await api.post<Ticket>(`/tickets/${id}/resolve`, { notes })
  return res.data
}

export async function reopenTicket(id: string): Promise<Ticket> {
  const res = await api.post<Ticket>(`/tickets/${id}/reopen`, {})
  return res.data
}

export async function closeTicket(id: string): Promise<Ticket> {
  const res = await api.post<Ticket>(`/tickets/${id}/close`, {})
  return res.data
}

export async function listReplies(ticketId: string): Promise<Reply[]> {
  const res = await api.get<Reply[]>(`/tickets/${ticketId}/replies`)
  return res.data
}

export async function listStatusHistory(ticketId: string): Promise<StatusHistoryEntry[]> {
  const res = await api.get<StatusHistoryEntry[]>(`/tickets/${ticketId}/history`)
  return res.data
}

export async function addReply(
  ticketId: string,
  body: string,
  internal = false,
  notifyCustomer = true
): Promise<Reply> {
  const res = await api.post<Reply>(`/tickets/${ticketId}/replies`, {
    body,
    internal,
    notify_customer: notifyCustomer,
  })
  return res.data
}

export async function listLinks(ticketId: string): Promise<TicketLink[]> {
  const res = await api.get<TicketLink[]>(`/tickets/${ticketId}/links`)
  return res.data
}

export async function addLink(
  ticketId: string,
  targetId: string,
  linkType: LinkType
): Promise<void> {
  await api.post(`/tickets/${ticketId}/links`, { target_id: targetId, link_type: linkType })
}

export async function removeLink(
  ticketId: string,
  targetId: string,
  linkType: LinkType
): Promise<void> {
  await api.delete(`/tickets/${ticketId}/links/${targetId}/${linkType}`)
}

export function duplicateResolutionNotes(targetTrackingNumber: string): string {
  return `Duplicate of ${targetTrackingNumber}`
}

export async function addDuplicateLinkAndResolve(
  ticketId: string,
  targetId: string,
  resolutionNotes: string
): Promise<Ticket> {
  const res = await api.post<Ticket>(`/tickets/${ticketId}/links`, {
    target_id: targetId,
    link_type: 'duplicate_of',
    resolve_as_duplicate: true,
    resolution_notes: resolutionNotes,
  })
  return res.data
}

// ── Tags ──────────────────────────────────────────────────────────────────────

export async function searchTags(q: string): Promise<Tag[]> {
  const res = await api.get<Tag[]>('/tags', { params: { q } })
  return res.data
}

export async function listTicketTags(ticketId: string): Promise<Tag[]> {
  const res = await api.get<Tag[]>(`/tickets/${ticketId}/tags`)
  return res.data
}

export async function addTicketTag(ticketId: string, name: string): Promise<Tag> {
  const res = await api.post<Tag>(`/tickets/${ticketId}/tags`, { name })
  return res.data
}

export async function removeTicketTag(ticketId: string, tagId: string): Promise<void> {
  await api.delete(`/tickets/${ticketId}/tags/${tagId}`)
}

// ── Public categories/types (no admin auth, active only) ──────────────────────

export async function listPublicCategories(): Promise<Category[]> {
  const res = await api.get<Category[]>('/categories')
  return res.data
}

export async function listPublicTypes(categoryId: string): Promise<TicketType[]> {
  const res = await api.get<TicketType[]>(`/categories/${categoryId}/types`)
  return res.data
}

export async function listPublicItems(categoryId: string, typeId: string): Promise<TicketItem[]> {
  const res = await api.get<TicketItem[]>(`/categories/${categoryId}/types/${typeId}/items`)
  return res.data
}

// ── Attachments ───────────────────────────────────────────────────────────────

export async function listAttachments(ticketId: string): Promise<Attachment[]> {
  const res = await api.get<Attachment[]>(`/tickets/${ticketId}/attachments`)
  return res.data
}

export async function uploadAttachment(ticketId: string, file: File): Promise<Attachment> {
  const form = new FormData()
  form.append('file', file)
  const res = await api.post<Attachment>(`/tickets/${ticketId}/attachments`, form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
  return res.data
}

/**
 * Asks the configured reputation service about this attachment again.
 *
 * Returns the attachment with its refreshed verdict. Refused with 409 on a
 * detection (engines do not un-flag a file), 429 when the same hash was
 * checked inside the last seven days, and 503 when there is no lookup
 * configured or the day's allowance is spent — each carrying a message that
 * says which.
 */
// `provider` re-checks one service rather than every enabled one. The expanded
// attachment row gives each service its own control, because each verdict has
// its own expiry clock; omitting it keeps the original behaviour, which the
// row's own merged control still wants.
export async function recheckAttachmentReputation(
  ticketId: string,
  attachmentId: string,
  provider?: string
): Promise<Attachment> {
  const path = `/tickets/${ticketId}/attachments/${attachmentId}/reputation`
  const res = await api.post<Attachment>(
    provider ? `${path}?provider=${encodeURIComponent(provider)}` : path,
    {}
  )
  return res.data
}

export function attachmentDownloadUrl(ticketId: string, attachmentId: string): string {
  return `/api/v1/tickets/${ticketId}/attachments/${attachmentId}`
}

// ── Custom fields ─────────────────────────────────────────────────────────────

export async function resolveFieldsForCTI(params: {
  category_id: string
  type_id?: string
  item_id?: string
}): Promise<Assignment[]> {
  const res = await api.get<Assignment[]>('/tickets/fields', { params })
  return res.data
}

export async function listTicketCustomFields(ticketId: string): Promise<TicketFieldValue[]> {
  const res = await api.get<TicketFieldValue[]>(`/tickets/${ticketId}/custom-fields`)
  return res.data
}

export async function putTicketCustomFields(
  ticketId: string,
  values: Record<string, string>
): Promise<void> {
  await api.put(`/tickets/${ticketId}/custom-fields`, values)
}

// ── Canned responses ──────────────────────────────────────────────────────────

export async function listTicketCannedResponses(ticketId: string): Promise<CannedResponse[]> {
  const res = await api.get<CannedResponse[]>(`/tickets/${ticketId}/canned-responses`)
  return res.data
}
