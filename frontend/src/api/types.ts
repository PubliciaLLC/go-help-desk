export type Role = 'admin' | 'staff' | 'user'
export type Priority = 'critical' | 'high' | 'medium' | 'low'
export type LinkType = 'related_to' | 'parent_of' | 'child_of' | 'caused_by' | 'duplicate_of'

export interface User {
  id: string
  email: string
  display_name: string
  role: Role
  mfa_enabled: boolean
  created_at: string
  updated_at: string
}

export type AuthType = 'local' | 'saml' | 'both'

export interface AdminUser {
  id: string
  email: string
  display_name: string
  role: Role
  disabled: boolean
  auth_type: AuthType
  has_password: boolean
  mfa_enabled: boolean
  created_at: string
  updated_at: string
  groups: Group[]
}

export interface Category {
  id: string
  name: string
  sort_order: number
  active: boolean
}

export interface TicketType {
  id: string
  category_id: string
  name: string
  sort_order: number
  active: boolean
}

export interface TicketItem {
  id: string
  type_id: string
  name: string
  sort_order: number
  active: boolean
}

export interface Status {
  id: string
  name: string
  kind: 'system' | 'custom'
  sort_order: number
  color: string
}

export interface Ticket {
  id: string
  tracking_number: string
  subject: string
  description: string
  category_id: string
  type_id?: string
  item_id?: string
  priority: Priority
  status_id: string
  assignee_user_id?: string
  assignee_group_id?: string
  reporter_user_id?: string
  guest_email?: string
  resolution_notes?: string
  resolved_at?: string
  closed_at?: string
  created_at: string
  updated_at: string
}

export interface Reply {
  id: string
  ticket_id: string
  author_id?: string
  body: string
  internal: boolean
  created_at: string
}

export interface TicketLink {
  source_id: string
  target_id: string
  link_type: LinkType
}

export interface Group {
  id: string
  name: string
  description: string
}

export interface Tag {
  id: string
  name: string
  created_at: string
  deleted_at?: string
}

export interface APIKey {
  id: string
  name: string
  user_id: string
  scopes: string[]
  last_used_at?: string
  expires_at?: string
  created_at: string
}

export interface WebhookConfig {
  id: string
  url: string
  events: string[]
  secret: string
  enabled: boolean
  created_at: string
}

export interface Settings {
  [key: string]: unknown
}

export interface ApiError {
  error: { code: string; message: string }
}
