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
  // The ticket's live SLA read, computed by the server on every GET /tickets
  // and GET /tickets/{id} response. null means no matching policy or SLA
  // tracking is off for this instance — render nothing, not green. The color
  // is the server's decision, not re-derived here: a second copy of the
  // 80/100 rule drifts (same reasoning as Attachment.reputation_url).
  // Absent on any response other than a get/list (e.g. after a PATCH), which
  // is why TicketDetailPage re-fetches rather than trusting a mutation's body.
  sla?: TicketSLA | null
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

// payload_format reshapes the same lifecycle event for a chat/ITSM service's
// incoming-webhook endpoint before it is POSTed. 'raw' (the default) is
// today's full event payload; the others are Slack, Teams, Discord and JIRA
// Automation shapes. See docs/DESIGN.md "Notifications" and #187.
export type WebhookPayloadFormat = 'raw' | 'slack' | 'teams' | 'discord' | 'jira'

export interface WebhookConfig {
  id: string
  url: string
  events: string[]
  secret: string
  enabled: boolean
  created_at: string
  payload_format: WebhookPayloadFormat
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

export interface TicketSLA {
  policy_id: string
  policy_name: string
  response: SLATargetStatus
  resolution: SLATargetStatus
}
