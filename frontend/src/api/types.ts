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
  // What the content detector made of the uploaded bytes, the hash of those
  // bytes, and the scanner's verdict. All null on an attachment that predates
  // detection: null is "not recorded", never "nothing wrong".
  detected_mime: string | null
  sha256: string | null
  // Non-null means the scanner identified the file as malicious, and this is
  // its name for the detection.
  virus_name: string | null
  // The content contradicted the filename. Independent of virus_name: a
  // mismatch alone is not a detection.
  mismatch: boolean | null
  // Where a person can look this hash up, built by the server from whichever
  // reputation provider the instance is configured for.
  //
  // The URL arrives finished rather than the provider's name, because the
  // provider is a session-gated admin setting that staff cannot read — and
  // because a frontend that assembled the URL itself would need each
  // provider's format duplicated here, where it would drift. null when there
  // is no hash to look up.
  reputation_url: string | null
  // What the configured reputation service said about sha256, when it was
  // asked. Only ever filled in for a quarantined attachment — the server does
  // not spend a daily allowance on holiday-request PDFs.
  //
  // Optional as well as nullable: absent and null mean the same thing here,
  // and an attachment built before this field existed is not a verdict.
  reputation?: AttachmentReputation | null
}

/**
 * One provider's verdict on a file's hash.
 *
 * The four states are not four shades of the same thing, and collapsing any of
 * them into "clean" is the mistake this type exists to make visible:
 *
 *   detected   at least one engine flagged it.
 *   clean      the provider analysed it and no engine flagged it. Only ever
 *              meaningful with the denominator: "0 of 78" is a result, "0 of
 *              0" is a missing lookup wearing one's clothes.
 *   unseen     the provider has never encountered this file. Not a verdict,
 *              and arguably more interesting than a clean one.
 *   unscanned  the provider knows the hash and holds no verdict for it. Also
 *              not a verdict.
 *
 * "unavailable" never arrives: a lookup that failed leaves the whole object
 * null, so there is one absence to render rather than two.
 */
export interface AttachmentReputation {
  state: 'unseen' | 'unscanned' | 'clean' | 'detected'
  // Engines that flagged the file and engines that ran. null together, and
  // only ever non-null for a completed analysis — nil is a fact, because 0 of
  // 0 reads as "nothing found anything".
  detected: number | null
  total: number | null
  // The provider's own consensus name for what it found; often empty.
  // Attacker-influenced, like virus_name: render it as text, never as markup.
  threat_name: string
  // When the PROVIDER analysed the file, not when we asked. What tells a
  // reader whether a clean verdict predates the sample appearing in the wild.
  analysed_at: string | null
  // Which service gave this verdict, so the claim can be attributed:
  // "VirusTotal has never seen this file" has a source, "the reputation
  // service has never seen this file" is a claim from nowhere.
  //
  // The frontend must not work this out for itself — the provider is a
  // session-gated admin setting staff cannot read, which is why the server
  // also sends a finished reputation_url. Anything this build does not
  // recognise falls back to the generic wording rather than printing an
  // operator's raw setting value at a reader.
  provider?: string
  // When WE last asked, as against analysed_at, which is when the PROVIDER
  // last looked. Staff may ask again once every seven days, and this is what
  // that control is armed on.
  //
  // Optional, like provider: a verdict stored before either field existed
  // carries neither.
  fetched_at?: string | null
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

export interface CannedResponse {
  id: string
  name: string
  body: string
  category_id?: string
  type_id?: string
  sort_order: number
  created_at: string
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

export interface ScopeInfo {
  scope: string
  resource: string
  action: string
}

export interface OAuthClient {
  id: string
  client_id: string
  name: string
  scopes: string[]
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
  priority?: Priority // absent = applies to every priority
  category_id?: string
  response_target_min: number
  resolution_target_min: number
}
