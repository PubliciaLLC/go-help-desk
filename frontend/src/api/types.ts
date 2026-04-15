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

export interface StatusHistoryEntry {
  id: string
  ticket_id: string
  from_status_id: string | null
  from_status_name: string
  from_status_color: string
  to_status_id: string
  to_status_name: string
  to_status_color: string
  changed_by_user_id: string | null
  changed_by_name: string
  created_at: string
}

export interface Status {
  id: string
  name: string
  kind: 'system' | 'custom'
  sort_order: number
  color: string
  active: boolean
  ticket_count: number
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
  guest_name?: string
  guest_phone?: string
  resolution_notes?: string
  resolved_at?: string
  closed_at?: string
  created_at: string
  updated_at: string
}

export interface Attachment {
  id: string
  ticket_id: string
  filename: string
  mime_type: string
  size_bytes: number
  created_at: string
}

export interface Reply {
  id: string
  ticket_id: string
  author_id?: string
  body: string
  internal: boolean
  notify_customer: boolean
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

// ── Custom fields ─────────────────────────────────────────────────────────────

export type FieldType = 'text' | 'textarea' | 'number' | 'select'
export type ScopeType = 'category' | 'type' | 'item'

export interface FieldDef {
  id: string
  name: string
  field_type: FieldType
  options?: string[]
  sort_order: number
  active: boolean
  created_at: string
}

export interface Assignment {
  id: string
  field_def_id: string
  field_def?: FieldDef
  scope_type: ScopeType
  scope_id: string
  sort_order: number
  visible_on_new: boolean
  required_on_new: boolean
}

export interface TicketFieldValue {
  ticket_id: string
  field_def_id: string
  field_name: string
  field_type: FieldType
  options?: string[]
  value: string
  updated_at: string
}

// ── SLA ───────────────────────────────────────────────────────────────────────

export interface SLAPolicy {
  id: string
  name: string
  priority: Priority
  category_id?: string
  response_target_min: number
  resolution_target_min: number
}
