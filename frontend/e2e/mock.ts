import type { Page } from '@playwright/test'

// The same shape the vitest component tests give api.get: a table of
// path -> response, falling back to an empty array/object rather than a
// network error for anything not named. These tests are about layout, not
// data, so the fixtures are the minimum each page needs to reach its
// authenticated, populated state.

export const ME_STAFF = {
  id: 'u-staff',
  email: 'sam.staff@example.com',
  display_name: 'Sam Staff',
  role: 'staff',
  mfa_enabled: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

export const ME_REPORTER = {
  id: 'u-reporter',
  email: 'rita.reporter@example.com',
  display_name: 'Rita Reporter',
  role: 'user',
  mfa_enabled: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

export const ME_ADMIN = {
  id: 'u-admin',
  email: 'ada.admin@example.com',
  display_name: 'Ada Admin',
  role: 'admin',
  mfa_enabled: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

export const STATUSES = [
  { id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: '#3b82f6', active: true, ticket_count: 3 },
  { id: 'st-resolved', name: 'Resolved', kind: 'system', sort_order: 2, color: '#22c55e', active: true, ticket_count: 0 },
  { id: 'st-closed', name: 'Closed', kind: 'system', sort_order: 3, color: '#6b7280', active: true, ticket_count: 0 },
]

export const CATEGORIES = [
  { id: 'cat-1', name: 'Hardware', active: true },
  { id: 'cat-2', name: 'Software', active: true },
]

// A single unbroken token long enough to overflow a 390px-wide box unless the
// element it sits in actually wraps it — the exact shape of bug the
// vacuous document.documentElement check (see mobile-layout.spec.ts) could
// never have caught, since nothing on the document itself ever overflowed.
const LONG_TOKEN =
  'https://example.com/a/very/long/path/segment/that/keeps/going/and/going/without/any/spaces/or/hyphens/to/break/on/1234567890'

// #426: ordinary prose long enough to wrap over several lines at 390px, with
// no hyphens (a hyphen is a legitimate break point, which would muddy the
// "no word split mid-word" check), and an unbroken 300-character URL that
// must still wrap rather than widen the page.
const longUrl = (path: string) => `https://example.com/${path}/`.padEnd(300, '0123456789abcdef')
export const DESCRIPTION_PROSE =
  'My laptop will not connect to the docking station since this morning. ' +
  'The display panel is dark and the keyboard does not respond at all. ' +
  'I need it working before the board meeting at three this afternoon.'
export const DESCRIPTION_URL = longUrl('vendor')
export const REPLY_PROSE =
  'Facilities logged the same fault last month on the third floor. Replacing the docking ' +
  'station fixed it then, so I have ordered a new one for you this morning.'
export const REPLY_URL = longUrl('courier')

export const TICKETS = [
  {
    id: 'tk-1',
    tracking_number: 'GHD-2026-000001',
    subject: 'Printer on fire, second floor',
    description: 'Smoke coming from the tray.',
    description_format: 'plain',
    category_id: 'cat-1',
    priority: 'critical',
    status_id: 'st-new',
    assignee_user_id: 'u-staff',
    created_at: '2026-09-20T10:00:00Z',
    updated_at: '2026-09-20T10:00:00Z',
    sla: null,
  },
  {
    id: 'tk-2',
    tracking_number: 'GHD-2026-000002',
    subject: 'VPN drops every hour on the hour',
    description: 'Started after the router firmware update.',
    description_format: 'plain',
    category_id: 'cat-2',
    priority: 'medium',
    status_id: 'st-new',
    created_at: '2026-09-21T09:00:00Z',
    updated_at: '2026-09-21T09:00:00Z',
    sla: null,
  },
  {
    id: 'tk-3',
    tracking_number: 'GHD-2026-000003',
    subject: `Deployment failing on ${LONG_TOKEN}`,
    description: 'Build pipeline points at the wrong artifact URL.',
    description_format: 'plain',
    category_id: 'cat-2',
    priority: 'high',
    status_id: 'st-new',
    created_at: '2026-09-22T09:00:00Z',
    updated_at: '2026-09-22T09:00:00Z',
    sla: null,
  },
]

// The other end of tk-1's one link — not in TICKETS above, since it does not
// need to appear in the ticket list itself, only be resolvable by id for
// LinkedTicketsPanel's own lookup.
export const LINKED_TICKET = {
  id: 'tk-4',
  tracking_number: 'GHD-2026-000004',
  subject: 'Duplicate printer smoke report from the third floor as well',
  description: 'Reported separately, same building.',
  description_format: 'plain',
  category_id: 'cat-1',
  priority: 'medium',
  status_id: 'st-new',
  created_at: '2026-09-20T11:00:00Z',
  updated_at: '2026-09-20T11:00:00Z',
  sla: null,
}

// The ticket the detail-page tests open. Deliberately not `{...TICKETS[0]}`:
// this fixture exists to populate every panel TicketDetailPage mounts (an
// assignee, a tag, a link, a custom field, an attachment pair) and to carry
// the long unbroken tokens that prove the page's text actually wraps, none of
// which belongs on the short, plain tickets the list-page tests check against.
export const TICKET_DETAIL = {
  id: 'tk-1',
  tracking_number: 'GHD-2026-000001',
  subject: `Printer on fire, second floor — see ${LONG_TOKEN}`,
  description: `${DESCRIPTION_PROSE}\n\nVendor thread: ${DESCRIPTION_URL}`,
  description_format: 'plain',
  category_id: 'cat-1',
  priority: 'critical',
  status_id: 'st-new',
  assignee_user_id: 'u-staff',
  created_at: '2026-09-20T10:00:00Z',
  updated_at: '2026-09-20T10:00:00Z',
  sla: null,
}

export const ASSIGNABLE_STAFF = [
  { id: 'u-staff', display_name: 'Sam Staff', assignable: true },
]

export const TAGS = [
  { id: 'tag-1', name: 'hardware', created_at: '2026-09-20T10:05:00Z' },
]

export const LINKS = [
  { source_id: TICKET_DETAIL.id, target_id: LINKED_TICKET.id, link_type: 'related_to' as const },
]

export const CUSTOM_FIELDS = [
  {
    ticket_id: TICKET_DETAIL.id,
    field_def_id: 'fld-1',
    field_name: 'Asset tag',
    field_type: 'text' as const,
    value: 'AST-00412',
    updated_at: '2026-09-20T10:05:00Z',
  },
]

export const REPLIES = [
  {
    id: 'rep-1',
    ticket_id: TICKET_DETAIL.id,
    author_id: 'u-staff',
    author_name: 'Sam Staff',
    body: `${REPLY_PROSE} Tracking: ${REPLY_URL}`,
    body_format: 'plain',
    internal: false,
    notify_customer: true,
    created_at: '2026-09-20T11:00:00Z',
  },
]

// One ordinary attachment and one quarantined attachment carrying two
// provider verdicts, so the tap-target pass actually exercises HashLine,
// ReputationLine's "Show all N services" toggle and both providers'
// "Open … report" links — none of which mount at all with an empty list.
export const ATTACHMENTS = [
  {
    id: 'att-1',
    ticket_id: TICKET_DETAIL.id,
    filename: 'tray-smoke.jpg',
    mime_type: 'image/jpeg',
    size_bytes: 204800,
    created_at: '2026-09-20T10:10:00Z',
    detected_mime: 'image/jpeg',
    sha256: 'a1b2c3d4e5f60718293a4b5c6d7e8f90112233445566778899aabbccddeeff0',
    virus_name: null,
    mismatch: false,
    reputation_url: null,
    reputation: null,
  },
  {
    id: 'att-2',
    ticket_id: TICKET_DETAIL.id,
    filename: 'firmware_update.exe',
    mime_type: 'application/zip',
    size_bytes: 51200,
    created_at: '2026-09-20T10:12:00Z',
    detected_mime: 'application/vnd.microsoft.portable-executable',
    sha256: 'ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100',
    virus_name: 'Trojan.Generic.Test',
    mismatch: true,
    reputation_url: 'https://www.virustotal.com/gui/file/ffeeddccbbaa99887766554433221100',
    reputation: {
      state: 'detected',
      detected: 60,
      total: 72,
      threat_name: 'Trojan.Generic.Test',
      analysed_at: '2026-09-19T00:00:00Z',
      provider: 'VirusTotal',
      provider_key: 'virustotal',
      fetched_at: '2026-09-20T10:12:00Z',
      known_feeds: null,
      providers: [
        {
          provider: 'VirusTotal',
          provider_key: 'virustotal',
          state: 'detected',
          detected: 60,
          total: 72,
          threat_name: 'Trojan.Generic.Test',
          known_feeds: null,
          analysed_at: '2026-09-19T00:00:00Z',
          fetched_at: '2026-09-20T10:12:00Z',
          link_url: 'https://www.virustotal.com/gui/file/ffeeddccbbaa99887766554433221100',
          recheckable: false,
          inline: true,
        },
        {
          provider: 'MetaDefender',
          provider_key: 'metadefender',
          state: 'detected',
          detected: 40,
          total: 44,
          threat_name: 'Trojan.Generic.Test',
          known_feeds: null,
          analysed_at: '2026-09-19T00:00:00Z',
          fetched_at: '2026-09-20T10:12:00Z',
          link_url: 'https://metadefender.opswat.com/results/file/ffeeddccbbaa99887766554433221100',
          recheckable: false,
          inline: false,
        },
      ],
    },
  },
]

/**
 * Routes every /api/v1/* call the four in-scope pages make to a fixture,
 * falling back to `{ data: [] }` for anything not named — the same
 * fallback the existing vitest component-test mocks use.
 */
// #443: the same ticket with Markdown text: a 300-character link, a wide table
// and a long line of code, the three things that could push the page sideways.
export const MARKDOWN_DETAIL = {
  ...TICKET_DETAIL,
  description:
    `Intro with **bold** and a [long link](${DESCRIPTION_URL}).\n\n` +
    `| ${Array.from({ length: 12 }, (_, i) => `column ${i} heading`).join(' | ')} |\n` +
    `|${' --- |'.repeat(12)}\n` +
    `| ${Array.from({ length: 12 }, (_, i) => `cell number ${i} value`).join(' | ')} |\n\n` +
    '```bash\n' + 'echo ' + 'x'.repeat(200) + '\n```\n',
  description_format: 'markdown',
}
export const MARKDOWN_REPLIES = [
  { ...REPLIES[0], body: `A [reply link](${REPLY_URL}) and\n\n- one\n- two\n\n> quoted ${REPLY_URL}`, body_format: 'markdown' },
]

export async function mockApi(
  page: Page,
  opts: { me?: object; ticket?: object; replies?: object[] } = {},
) {
  const me = opts.me ?? ME_STAFF
  const byPath: Record<string, unknown> = {
    '/api/v1/me': me,
    '/api/v1/site': { name: 'Go Help Desk', logo_url: '', version: '1.3.0' },
    '/api/v1/statuses': STATUSES,
    '/api/v1/tickets': TICKETS,
    '/api/v1/categories': CATEGORIES,
    [`/api/v1/tickets/${TICKET_DETAIL.id}`]: opts.ticket ?? TICKET_DETAIL,
    [`/api/v1/tickets/${LINKED_TICKET.id}`]: LINKED_TICKET,
    [`/api/v1/tickets/${TICKET_DETAIL.id}/attachments`]: ATTACHMENTS,
    [`/api/v1/tickets/${TICKET_DETAIL.id}/replies`]: opts.replies ?? REPLIES,
    [`/api/v1/tickets/${TICKET_DETAIL.id}/history`]: [],
    [`/api/v1/tickets/${TICKET_DETAIL.id}/links`]: LINKS,
    [`/api/v1/tickets/${TICKET_DETAIL.id}/tags`]: TAGS,
    [`/api/v1/tickets/${TICKET_DETAIL.id}/custom-fields`]: CUSTOM_FIELDS,
    [`/api/v1/tickets/${TICKET_DETAIL.id}/canned-responses`]: [],
    '/api/v1/staff': ASSIGNABLE_STAFF,
    '/api/v1/groups': [],
    '/api/v1/admin/users': [],
    '/api/v1/tickets/fields': [],
  }

  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname
    if (path in byPath) {
      await route.fulfill({ json: byPath[path] })
      return
    }
    // /categories/:id/types and /types/:id/items — no CTI fixtures needed
    // for a layout check, so every one of these is "none configured".
    await route.fulfill({ json: [] })
  })
}

// #522: admin pages. 300 characters with no break point, in the shapes admins
// actually type: a name, a description, a URL. One token per field so a page
// that fails to wrap it widens by ~2000px rather than by a hair.
export const LONG_NAME = 'N'.repeat(300)
export const LONG_DESC = 'D'.repeat(300)
export const LONG_ADMIN_URL = 'https://hooks.example.com/' + 'u'.repeat(274)
export const LONG_EMAIL = 'e'.repeat(60) + '.' + 'f'.repeat(60) + '@' + 'g'.repeat(60) + '.example.com'

export const SAML_CERT = '-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUQ0hlYXBlc3RDZXJ0\n-----END CERTIFICATE-----\n'

const LONG_GROUP = { id: 'grp-1', name: LONG_NAME, description: LONG_DESC }

export const LONG_USER = {
  id: 'u-long',
  email: LONG_EMAIL,
  display_name: LONG_NAME,
  role: 'staff',
  disabled: false,
  auth_type: 'local',
  has_password: true,
  mfa_enabled: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  groups: [LONG_GROUP],
}

// The same page, short data: Users scrolled sideways with nothing long in it.
export const SHORT_USER = { ...LONG_USER, id: 'u-short', email: 'a@b.co', display_name: 'Al', groups: [] }

/**
 * Layers long-data fixtures for every admin endpoint over mockApi. Registered
 * after it, so these win (Playwright matches the most recent route first).
 */
export async function mockAdminApi(page: Page, opts: { short?: boolean } = {}) {
  await mockApi(page, { me: ME_ADMIN })
  const n = opts.short ? 'Short' : LONG_NAME
  const d = opts.short ? 'Short' : LONG_DESC
  const u = opts.short ? 'https://x.example.com/h' : LONG_ADMIN_URL
  const group = { id: 'grp-1', name: n, description: d }
  const byPath: Record<string, unknown> = {
    '/api/v1/admin/users': opts.short ? [SHORT_USER] : [LONG_USER],
    [`/api/v1/admin/users/${LONG_USER.id}`]: LONG_USER,
    '/api/v1/admin/groups': [group, { ...group, id: 'grp-2' }],
    '/api/v1/admin/groups/grp-1/members': [LONG_USER],
    '/api/v1/admin/groups/grp-1/scopes': [{ group_id: 'grp-1', category_id: 'cat-1' }],
    '/api/v1/admin/categories': [{ id: 'cat-1', name: n, sort_order: 1, active: true }],
    '/api/v1/admin/categories/cat-1/types': [{ id: 'ty-1', category_id: 'cat-1', name: n, sort_order: 1, active: true }],
    '/api/v1/admin/categories/cat-1/types/ty-1/items': [{ id: 'it-1', type_id: 'ty-1', name: n, sort_order: 1, active: true }],
    '/api/v1/admin/categories/cat-1/groups': [group],
    '/api/v1/admin/categories/cat-1/types/ty-1/groups': [group],
    '/api/v1/statuses': [{ ...STATUSES[0], id: 'st-long', name: n, kind: 'custom' }, ...STATUSES],
    '/api/v1/admin/tags': [{ id: 'tag-long', name: n, created_at: '2026-09-20T10:05:00Z' }],
    '/api/v1/admin/canned-responses': [
      { id: 'cr-1', name: n, body: d, sort_order: 1, created_at: '2026-09-20T10:05:00Z' },
    ],
    '/api/v1/admin/custom-fields': [
      { id: 'fld-long', name: n, field_type: 'select', options: [d], sort_order: 1, active: true, created_at: '2026-09-20T10:05:00Z' },
    ],
    '/api/v1/admin/api-keys': [
      {
        id: 'key-1', name: n, user_id: LONG_USER.id, owner_name: n, owner_email: opts.short ? 'a@b.co' : LONG_EMAIL,
        scopes: ['tickets:read'], created_at: '2026-09-20T10:05:00Z',
      },
    ],
    '/api/v1/admin/oauth-clients': [
      { id: 'oc-1', client_id: d, name: n, scopes: ['tickets:read'], created_at: '2026-09-20T10:05:00Z', groups: [{ id: 'grp-1', name: n }] },
    ],
    '/api/v1/admin/webhooks': [
      {
        id: 'wh-1', url: u, events: ['ticket.created'], enabled: true, created_at: '2026-09-20T10:05:00Z',
        payload_format: 'raw', filter: { category_ids: ['cat-1'], priorities: ['high'], group_ids: ['grp-1'], invert: false },
        last_delivery: { at: '2026-09-20T10:05:00Z', status: 500, error: 'http_status' },
      },
    ],
    '/api/v1/admin/sla/policies': [
      { id: 'sla-1', name: n, priority: 'high', response_target_min: 30, resolution_target_min: 240 },
    ],
    '/api/v1/admin/scopes': [{ scope: 'tickets:read', resource: 'tickets', action: 'read' }],
    '/api/v1/admin/settings': { sla_enabled: true, saml_enabled: true, setup_complete: true },
    '/api/v1/admin/saml': { configured: true, metadata_url: u, metadata_xml: '', cert_pem: SAML_CERT, sp_metadata_url: u },
    '/api/v1/admin/oidc': { enabled: true, issuer_url: u, client_id: d, client_secret: '', redirect_url: u, configured: true },
    '/api/v1/admin/audit': {
      entries: [
        {
          id: 'au-1', entity_type: 'group', entity_id: 'grp-1', action: 'update', actor_id: 'u-admin',
          actor_name: n, created_at: '2026-09-20T10:05:00Z',
          before: { name: n }, after: { name: d }, names: {},
        },
      ],
      total: 1, total_capped: false, has_more: false,
    },
    '/api/v1/staff': [{ id: LONG_USER.id, display_name: n, email: opts.short ? 'a@b.co' : LONG_EMAIL }],
    '/api/v1/groups': [group],
  }
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    if (route.request().method() === 'GET' && path in byPath) {
      await route.fulfill({ json: byPath[path] })
      return
    }
    await route.fallback()
  })
}
