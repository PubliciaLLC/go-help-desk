import { api } from './client'
import type { Ticket, Reply, TicketLink, LinkType, Tag, Attachment, Category, TicketType } from './types'

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
}

export async function listTickets(params?: { assignee_group_id?: string }): Promise<Ticket[]> {
  const res = await api.get<Ticket[]>('/tickets', { params })
  return res.data
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
  patch: { status_id?: string; assignee_user_id?: string; assignee_group_id?: string }
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

export async function listReplies(ticketId: string): Promise<Reply[]> {
  const res = await api.get<Reply[]>(`/tickets/${ticketId}/replies`)
  return res.data
}

export async function addReply(ticketId: string, body: string, internal = false): Promise<Reply> {
  const res = await api.post<Reply>(`/tickets/${ticketId}/replies`, { body, internal })
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

export function attachmentDownloadUrl(ticketId: string, attachmentId: string): string {
  return `/api/v1/tickets/${ticketId}/attachments/${attachmentId}`
}
