export type Role = 'admin' | 'staff' | 'user'
export type Priority = 'critical' | 'high' | 'medium' | 'low'
export type LinkType = 'related_to' | 'parent_child' | 'caused_by' | 'duplicate_of'

export interface User {
  id: string
  email: string
  display_name: string
  role: Role
  mfa_enabled: boolean
  created_at: string
  updated_at: string
  /** Staff-email switches (#441). Optional so a profile from before they
   *  existed reads as on, which is also what the server defaults them to. */
  email_notify_assigned?: boolean
  email_notify_activity?: boolean
  email_notify_group?: boolean
  /** What the switches amount to for this person, with whether an administrator
   *  forced each one (#441). Only on /me and the preferences answer, and only
   *  for staff and administrators. */
  email_notifications?: {
    assigned: EmailSwitchState
    activity: EmailSwitchState
    group: EmailSwitchState
  }
}

export interface EmailSwitchState {
  enabled: boolean
  forced: boolean
}

export type EmailPreferencesPatch = Partial<
  Pick<User, 'email_notify_assigned' | 'email_notify_activity' | 'email_notify_group'>
>

// How an account signs in, from the credentials it holds. 'both' is local plus
// SAML; combinations that include OIDC are joined with '+'. 'service' is an
// OAuth client's service user (#420), which never signs in.
export type AuthType =
  | 'service'
  | 'local'
  | 'saml'
  | 'oidc'
  | 'both'
  | 'local+oidc'
  | 'saml+oidc'
  | 'local+saml+oidc'

export interface AdminUser {
  id: string
  email: string
  display_name: string
  role: Role
  disabled: boolean
  auth_type: AuthType
  // The identity an OAuth client acts as (#420). Never signs in, so it has no
  // password, second factor or admin role. Optional so older fixtures read as
  // an ordinary account.
  service_account?: boolean
  has_password: boolean
  mfa_enabled: boolean
  // Registered passkeys. Sent on the detail response only; mfa_enabled is the
  // TOTP flag alone, so a passkey-only account is told apart by this.
  passkey_count?: number
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

// #422: what the ids in a diff's before/after name now, keyed by the id as it
// appears there. An id with no entry no longer resolves (the row is gone) and
// is shown as the id. `masked` is true when name is the "Requester" mask.
export type AuditNames = Record<string, { name: string; masked?: boolean }>

// The admin-wide audit view (#129) — see handleListAdminAudit. Same shape as
// TicketAuditEntry plus the entity a per-ticket feed doesn't need to say.
export interface AdminAuditEntry {
  id: string
  entity_type: string
  entity_id: string
  action: string
  actor_id: string | null
  actor_name?: string
  // #366: true when actor_name is the "Requester" mask rather than a real
  // display name. Absent when false.
  actor_masked?: boolean
  created_at: string
  before?: Record<string, unknown> | null
  after?: Record<string, unknown> | null
  names?: AuditNames
}

export interface AdminAuditListResponse {
  entries: AdminAuditEntry[]
  /**
   * How many entries match the filter and the viewer may see, counted up to a
   * cap (see `total_capped`). For staff under
   * ticket scope that is the count of entries on tickets in their scope —
   * scope is part of the count's own query, so it says nothing about the
   * entries they cannot see.
   */
  total: number
  /**
   * True when more entries match than the server counts: `total` is then the
   * server's cap and means "at least this many". Show it as "n+".
   */
  total_capped: boolean
  /** Whether another page exists. The pager runs on this; `total` is for display. */
  has_more: boolean
}

// Someone following a ticket (#441), as GET /tickets/{id}/followers lists them.
export interface TicketFollower {
  user_id: string
  display_name: string
  created_at: string
}

export interface TicketAuditEntry {
  id: string
  action: string
  actor_id: string | null
  // omitempty on the backend: absent (not just empty) for a system actor or
  // one whose account no longer exists.
  actor_name?: string
  // #366: true when actor_name is the "Requester" mask rather than a real
  // display name. Absent when false.
  actor_masked?: boolean
  created_at: string
  // omitempty on the backend: the server decides whether this viewer sees
  // the field-level diff at all (admin always, staff only with the setting
  // on, reporters never) — see handleListTicketAudit. Each side is omitted
  // when empty, so a create entry shown with its diff has `after` and no
  // `before` key at all. Both absent can mean "not shown to you" or "nothing
  // recorded"; this type cannot tell them apart.
  before?: Record<string, unknown> | null
  after?: Record<string, unknown> | null
  names?: AuditNames
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
  closed_at?: string // staff and admin only: a requester does not receive it
  created_at: string
  updated_at?: string // staff and admin only: a requester does not receive it
  // The ticket's live SLA read, computed by the server on every GET /tickets
  // and GET /tickets/{id} response. null means no matching policy or SLA
  // tracking is off for this instance — render nothing, not green. The color
  // is the server's decision, not re-derived here: a second copy of the
  // 80/100 rule drifts (same reasoning as Attachment.reputation_url).
  // Absent on any response other than a get/list (e.g. after a PATCH), which
  // is why TicketDetailPage re-fetches rather than trusting a mutation's body.
  sla?: TicketSLA | null
  // Whether the CURRENT viewer may force-reopen this ticket: it is Closed and
  // the instance's closed_reopen_policy lets their role (#349). A courtesy for
  // the Reopen button; the server decides again when it is clicked. Absent on
  // a response that does not carry it, which reads as no.
  can_reopen?: boolean
  // How many times the ticket has been moved from Resolved or Closed back to an
  // open status (#464). Never reset. Staff and administrators only: absent for a requester, and when 0. Absent on a response that does not carry
  // it, which reads as never.
  reopen_count?: number
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
  // Where a person can look this hash up, built by the server.
  //
  // The URL arrives finished rather than the provider's name, because the
  // provider is a session-gated admin setting that staff cannot read — and
  // because a frontend that assembled the URL itself would need each
  // provider's format duplicated here, where it would drift.
  //
  // null on any row this instance found nothing on: the link goes only where
  // the scanner named the file or the content contradicts the name, because a
  // link plus a line of explanation under every holiday-request PDF is the
  // noise that teaches people to stop reading the rows that matter. null too
  // when there is no hash at all. WHICH rows get one is the server's decision
  // and is deliberately not re-derived here — a second copy of that rule is a
  // copy that drifts, silently, because both halves look right on their own.
  // The hash itself is shown wherever it was recorded, link or no link.
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
 * What one service said about a file's hash.
 *
 * The five verdicts are not five shades of the same thing, and collapsing any
 * of them into "clean" is the mistake this type exists to make visible:
 *
 *   detected   at least one engine flagged it.
 *   clean      the provider analysed it and no engine flagged it. Only ever
 *              meaningful with the denominator: "0 of 78" is a result, "0 of
 *              0" is a missing lookup wearing one's clothes.
 *   unseen     the provider has never encountered this file. Not a verdict,
 *              and arguably more interesting than a clean one.
 *   unscanned  the provider knows the hash and holds no verdict for it. Also
 *              not a verdict.
 *   known      a named catalogue has this exact hash on file. Nothing was
 *              scanned — the answer comes straight from the hash match — so
 *              it carries no counts at all, and what it carries instead is
 *              known_feeds. The one state here that may read as reassurance,
 *              and only because a named feed made a positive claim.
 *
 * And one non-verdict:
 *
 *   unavailable  the lookup was attempted and did not finish — a spent
 *                allowance, a provider that is down, a key that was rejected.
 *                It is NOT in the severity ordering, so it never displaces a
 *                real answer: it reaches the summary only when nothing
 *                answered at all, and otherwise appears against its own
 *                provider in the expanded list, where an operator can see
 *                their key failing.
 *
 * Absent or null is a different fact again, and the one thing it must never
 * read as is either of the above: no provider is enabled, so nothing was
 * attempted and there is nothing to say.
 */
export type ReputationState =
  | 'unseen'
  | 'unscanned'
  | 'clean'
  | 'detected'
  | 'known'
  | 'unavailable'

/**
 * One file's reputation: the worst single answer, for the row, and every
 * enabled provider's own answer under it, for the expanded view.
 *
 * The top-level fields are a COPY of one entry of `providers` rather than an
 * aggregate across them, which is what keeps them honest — 62 of 81 engines is
 * a fact about VirusTotal's analysis, and averaging it with a catalogue hit
 * from CIRCL would produce a number no service ever said. `provider_key` names
 * the entry that was copied and the entry itself carries `inline: true`, so
 * the row's single sentence is never a claim from nowhere.
 */
export interface AttachmentReputation {
  state: ReputationState
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
  // The same service as a machine reads it: "virustotal", "metadefender",
  // "polyswarm", "circl". It names which entry of `providers` the fields above
  // were copied from, and it is what the re-check endpoint's ?provider= takes.
  provider_key?: string
  // When WE last asked, as against analysed_at, which is when the PROVIDER
  // last looked. Staff may ask again once every seven days, and this is what
  // that control is armed on.
  //
  // Optional, like provider: a verdict stored before either field existed
  // carries neither.
  fetched_at?: string | null
  // Which catalogues have this hash on file. Set only on `known`, and empty
  // on every other state.
  //
  // Not decoration and not optional to render. `known` says only that
  // somebody has the file on record; these say who, and who is what decides
  // how much that is worth. An entry in a vendor's signing feed is an
  // Authenticode assertion that the file is signed and trusted; an NSRL entry
  // means only that the file turned up in a known software distribution, and
  // NSRL catalogues hacking tools. A renderer that shows the state without
  // naming the feed makes a claim the data does not support.
  //
  // Sorted and de-duplicated by the server. The strings are the feed
  // identifiers as the provider spells them, which is NOT one spelling: the
  // API documents `Microsoft Windows` and earlier research recorded
  // `microsoft_windows`, so turning one into a phrase for a person — the
  // frontend's job — has to be insensitive to case and to the separator.
  known_feeds?: string[] | null
  // Every enabled provider's own answer, in the order the server sorted them,
  // one entry each — including the ones that failed.
  //
  // The list is the reason for having more than one provider: they answer
  // different questions, and a sample one calls `detected` while another calls
  // `known` is telling staff something either alone would hide. The row shows
  // the worst of them and one click shows all of them, attributed.
  //
  // Optional because a server that predates the second provider sends a
  // verdict and no list. One entry, or none, is a single-provider instance and
  // renders as it always did — there is nothing to expand into.
  providers?: AttachmentProviderVerdict[] | null
}

/**
 * One service's statement about one file at one time, for the expanded view.
 *
 * Each carries its own timestamps and its own eligibility for a re-check
 * because each is genuinely separate: the verdict cache is keyed by hash AND
 * provider, so every answer has its own expiry clock. A shared "last checked"
 * across four services would be wrong for at least three of them.
 */
export interface AttachmentProviderVerdict {
  // The name as a person reads it, and the name as a machine does. Rendering
  // resolves the key against this build's own table rather than printing
  // either string, for the same reason the summary does.
  provider: string
  provider_key: string
  // This provider's own verdict, and unlike the summary it may be
  // `unavailable`: the lookup was attempted and did not finish. Hiding that
  // behind a sibling's good answer is how a dead integration goes unnoticed
  // for a month.
  state: ReputationState
  detected: number | null
  total: number | null
  threat_name: string
  known_feeds?: string[] | null
  // When THIS provider analysed the file, and when we last asked THIS
  // provider. Both null when there is nothing to say.
  analysed_at: string | null
  fetched_at: string | null
  // This provider's own page for the hash, beside its own verdict, and null
  // for one that has no per-hash page — CIRCL, whose root serves a Swagger
  // document. Distinct from Attachment.reputation_url, which is the VirusTotal
  // link the server puts on the rows this instance itself found something on:
  // the scanner named the file, or the content contradicts the name. The
  // per-provider toggles do not govern that link either — what decides it is
  // which row it is on, and that is the server's call. See its own comment.
  link_url: string | null
  // Whether a re-check of THIS provider would be attempted rather than
  // refused: the verdict can still change and the seven-day floor has passed.
  // It deliberately does not consult the daily budget, which can be spent
  // between the render and the click.
  recheckable: boolean
  // Marks the entry the summary above was copied from, so the row's single
  // line can be traced to the service that said it. Exactly one entry carries
  // it when anything answered, and none when nothing did.
  inline: boolean
}

export interface Reply {
  id: string
  ticket_id: string
  author_id?: string
  // The author's display name, sent with the reply. Absent for a guest's
  // reply, which has no account behind it — that is the one case where
  // author_id is genuinely missing too.
  author_name?: string
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
  // Whose key it is. A signed-in administrator is sent every key, so the list
  // has to say.
  owner_name?: string
  owner_email?: string
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
  // The client's service user (#420) and the groups it is in, which are the
  // client's ticket scope. Optional so a server that predates them reads as
  // "unknown" rather than "none".
  user_id?: string
  groups?: { id: string; name: string }[]
}

// payload_format reshapes the same lifecycle event for a chat/ITSM service's
// incoming-webhook endpoint before it is POSTed. 'raw' (the default) is
// ids-only JSON; the others are Slack, Teams, Discord and JIRA
// Automation shapes. See docs/DESIGN.md "Notifications" and #187.
export type WebhookPayloadFormat = 'raw' | 'slack' | 'teams' | 'discord' | 'jira'

// A class, never a message: the server stores no error text (#157).
export type WebhookDeliveryError =
  | '' | 'http_status' | 'timeout' | 'dns' | 'tls' | 'blocked_address' | 'connection' | 'other'

export interface WebhookDelivery {
  at: string
  status: number // 0 when no response came back, or one outside 100..999 did (error tells which)
  error: WebhookDeliveryError
}

// A webhook's ticket filter (#413). Empty lists are no condition. All
// conditions must match. invert sends for the tickets that do not.
export interface WebhookFilter {
  category_ids: string[]
  priorities: Priority[]
  group_ids: string[]
  invert: boolean
}

// One row of a hook's history (#448): an outcome, never content. result is 'ok'
// or a class; there is no error text.
export interface WebhookDeliveryRecord {
  id: string
  event: string
  started_at: string
  last_attempt_at: string
  status: number
  result: 'ok' | 'dropped' | Exclude<WebhookDeliveryError, ''>
  attempts: number
  retrying: boolean
  // Why a queued retry was dropped (#499); only when result is 'dropped'.
  drop_reason?: WebhookDropReason
}

export type WebhookDropReason = 'disabled' | 'unsubscribed' | 'url_changed' | 'filter' | 'unbuildable'

export interface WebhookConfig {
  id: string
  url: string
  events: string[]
  // Optional only because fixtures written before #413 omit it; the server
  // always sends it.
  filter?: WebhookFilter
  // No `secret`: it is write-only. The API accepts one on create and update
  // and never returns it, so a client has nothing to show or prefill.
  enabled: boolean
  created_at: string
  payload_format: WebhookPayloadFormat
  last_delivery?: WebhookDelivery | null
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

export type SLAColor = 'green' | 'amber' | 'red'

// One target's (response or resolution) live read, embedded on a ticket. Once
// met_at is set the numbers are frozen as of that instant and stop moving —
// color is then the target's final color, so a late response stays red and an
// on-time one stays green forever after.
export interface SLATargetStatus {
  color: SLAColor
  target_min: number
  elapsed_min: number
  remaining_min: number // target_min - elapsed_min; negative when over
  met_at: string | null // set: the numbers above are frozen and color is final
}

// The SLA record a restart replaced (#443): the clock restarted when the ticket
// was reopened, and this is the record it replaced. Never sent after a staff or
// admin priority change, which keeps the clock. One-ticket read only.
export interface SLAEarlierRecord {
  policy_id: string
  policy_name: string
  started_at: string
  superseded_at: string
  outcome: 'met' | 'breached'
}

export interface TicketSLA {
  policy_id: string
  policy_name: string
  response: SLATargetStatus
  resolution: SLATargetStatus
  earlier?: SLAEarlierRecord
}
