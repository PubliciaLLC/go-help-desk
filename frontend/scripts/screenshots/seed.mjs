// Usage: SHOTS_BASE_URL=http://localhost:18080 DATABASE_URL=postgres://... [SHOTS_PASSWORD SHOTS_OUT SHOTS_REPUTATION=0] node frontend/scripts/screenshots/seed.mjs
//        ... SHOTS_MAIL_DIR=<fake-smtp.mjs's FAKE_SMTP_DIR> node frontend/scripts/screenshots/seed.mjs --extras
//
// The first form seeds the instance for shots 01 to 28. --extras adds what the
// 1.3.0 shots from 29 on need and must run after those have been captured: it
// files a ticket, archives a category and changes settings, any of which would
// alter the older shots (the ticket lists, the audit log, the dashboard counts).
// It reads and rewrites seed.json, adding an "extras" key. It also writes the
// descriptions on Hardware and Hardware > Laptop and the two followers of T7
// (shots 53 to 55), last, after the history that puts Sam in IT Support.
//
// --extras also seeds the history the dashboard (02) and Reports (48) need: SLA
// tracking and policies, a group with three members, about seventy tickets filed
// over two months, a reopened one, an unassigned pair and a breached and an
// at-risk ticket. The API cannot backdate, so every ticket in that history
// is filed "now" and described in $SHOTS_OUT/history.json, which backdate.sql
// reads (scripts/screenshots.sh runs it between the extras seed and the extras
// capture).
import { request } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const env = (k, d) => process.env[k] || d
const BASE = env('SHOTS_BASE_URL', 'http://localhost:18080')
const PASSWORD = env('SHOTS_PASSWORD', 'Screenshots-2026!')
const DATABASE_URL = process.env.DATABASE_URL
const OUT = env('SHOTS_OUT', join(tmpdir(), 'ghd-shots'))
const REPUTATION = env('SHOTS_REPUTATION', '1') !== '0'
// Decoded at runtime so the repo never contains the EICAR string itself.
const EICAR = Buffer.from('WDVPIVAlQEFQWzRcUFpYNTQoUF4pN0NDKTd9JEVJQ0FSLVNUQU5EQVJELUFOVElWSVJVUy1URVNULUZJTEUhJEgrSCo=', 'base64')

const P = {
  admin: { name: 'Alex Rivera', email: 'alex.rivera@example.com', role: 'admin' },
  jordan: { name: 'Jordan Blake', email: 'jordan.blake@example.com', role: 'staff' },
  sam: { name: 'Sam Okafor', email: 'sam.okafor@example.com', role: 'staff' },
  morgan: { name: 'Morgan Diaz', email: 'morgan.diaz@example.com', role: 'user' },
  casey: { name: 'Casey Nguyen', email: 'casey.nguyen@example.com', role: 'user' },
  taylor: { name: 'Taylor Brooks', email: 'taylor.brooks@example.com', role: 'user' },
  drew: { name: 'Drew Patel', email: 'drew.patel@example.com', role: 'user' },
}
const RILEY = 'riley.chen@example.com'
const PAT = { name: 'Pat Lee', email: 'pat.lee@example.com' }

const ok = (status, want) => (want ? [want].flat().includes(status) : status >= 200 && status < 300)
const clean = (o) => Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined))

const contexts = []
async function newContext() {
  const c = await request.newContext({ baseURL: BASE })
  contexts.push(c)
  return c
}

// Returns { status, body } without throwing, for the calls that branch on a status.
async function raw(ctx, method, path, data) {
  const res = await ctx.fetch(path, { method, data })
  const text = await res.text()
  let body = text
  try { body = text ? JSON.parse(text) : null } catch { /* plain text body */ }
  return { status: res.status(), body }
}

// Throws on an unexpected status. Shows the path and the server's reply, never the request body.
async function call(ctx, method, path, data, want) {
  const r = await raw(ctx, method, path, data)
  if (!ok(r.status, want)) {
    const text = typeof r.body === 'string' ? r.body : JSON.stringify(r.body)
    throw new Error(`${method} ${path} -> ${r.status}: ${text.slice(0, 300)}`)
  }
  return r.body
}

async function upload(ctx, path, name, buffer) {
  const res = await ctx.fetch(path, { method: 'POST', multipart: { file: { name, mimeType: 'text/plain', buffer } } })
  const text = await res.text()
  if (res.status() !== 201) throw new Error(`POST ${path} (upload ${name}) -> ${res.status()}: ${text.slice(0, 300)}`)
  return JSON.parse(text)
}

async function login(email) {
  const c = await newContext()
  const r = await call(c, 'POST', '/api/v1/auth/local/login', { email, password: PASSWORD }, 200)
  if (r?.mfa_needed) throw new Error(`${email}: MFA is enabled; the seed expects it off`)
  return c
}

const SETTINGS = () => clean({
  ticket_prefix: 'GHD',
  guest_submission_enabled: true,
  self_signup_enabled: true,
  allowed_email_domains: ['example.com'],
  reopen_target_status_name: 'In Progress',
  ticket_scope_enforced: true,
  staff_can_view_ticket_change_history: true,
  attachment_scan_policy: 'required',
  attachment_infected_handling: 'quarantine',
  attachment_reputation_circl_enabled: REPUTATION,
  attachment_reputation_virustotal_enabled: REPUTATION,
  attachment_reputation_virustotal_key: REPUTATION ? 'screenshot-run-no-real-key' : undefined,
})

// The server refuses to deliver to loopback and private addresses (internal/safehttp),
// so a local stand-in cannot answer a hook. The hooks go to the example hosts, which
// answer a POST with 405, and the page shows "Failing · 405". To show a delivery that
// worked, SHOTS_WEBHOOK_OK_URL names a public endpoint that answers a POST with 2xx:
// the Slack and Teams hooks then both post there and show "Delivered" (the disabled Jira
// hook still has no deliveries). A hook posts Slack, Teams or Jira JSON built from the
// seeded (made-up) tickets.
const WEBHOOK_OK_URL = process.env.SHOTS_WEBHOOK_OK_URL
const WEBHOOKS = [
  // With an endpoint chosen, each of the first two hooks fires once (one ticket is closed), not
  // for every ticket filed: a shared test service answers a burst of POSTs with 429, and the
  // server obeys its Retry-After, so the retry would land long after the seed has looked.
  { url: WEBHOOK_OK_URL || 'https://example.com/hooks/helpdesk', events: WEBHOOK_OK_URL ? ['ticket.closed'] : ['ticket.created', 'ticket.status_changed'], secret: 'screenshot-run-signing-secret', payload_format: 'slack', enabled: true },
  { url: WEBHOOK_OK_URL || 'https://www.example.org/teams-webhook', events: WEBHOOK_OK_URL ? ['ticket.closed'] : ['ticket.resolved', 'ticket.closed'], payload_format: 'teams', enabled: true },
  { url: 'https://www.example.net/jira-automation', events: ['*'], payload_format: 'jira', enabled: false },
]

const BODY = {
  reply_canned: 'We have ordered a replacement screen. A technician will contact you within two business days to arrange the swap.',
  reply_vpn: 'Thanks for the detail. We are reviewing the gateway logs for your account and will update you shortly.',
  reply_password: 'Thanks. We are checking the portal account and will confirm once the reset is done.',
  reply_monitor: 'Thanks for the report. We are sending a replacement dock cable to your desk today.',
  device_info: 'Model: Contoso Book 14 (example)\nOS build: 24H2\nDisplay: internal panel cracked, external monitor works\n',
  email_headers: 'Supplier invoice 2026-0417: header lines copied from the mail for the payment run.\n\nReturn-Path: <accounts@example.com>\nFrom: Accounts <accounts@example.com>\nTo: Casey Nguyen <casey.nguyen@example.com>\nSubject: Invoice 2026-0417\nDate: Tue, 14 Apr 2026 09:12:44 +0000\nMessage-ID: <0417.invoice@example.com>\n',
}

async function main() {
  if (!DATABASE_URL) throw new Error('DATABASE_URL is required (psql reads the signup token)')
  mkdirSync(OUT, { recursive: true })
  const anon = await newContext()
  const setup = await call(anon, 'GET', '/api/v1/setup/status', null, 200)
  if (!setup.needed) throw new Error('instance already has users: the seed needs a fresh database')

  // A. Instance: setup creates the admin and the first category.
  await call(anon, 'POST', '/api/v1/setup', { email: P.admin.email, display_name: P.admin.name, password: PASSWORD, category: 'Hardware' }, 201)
  const admin = await login(P.admin.email)
  await call(admin, 'PATCH', '/api/v1/admin/settings', SETTINGS(), 204)
  console.log('A. instance configured')

  // B. Catalogue.
  const cats = {}
  const types = {}
  for (const c of await call(admin, 'GET', '/api/v1/admin/categories', null, 200)) cats[c.name] = c.id
  if (!cats.Hardware) throw new Error('setup did not create the Hardware category')
  for (const [name, sort] of [['Software', 1], ['Network', 2], ['Accounts & Access', 3]]) {
    cats[name] = (await call(admin, 'POST', '/api/v1/admin/categories', { name, sort_order: sort }, 201)).id
  }
  const CATALOGUE = {
    Hardware: ['Laptop', 'Printer', 'Monitor'],
    Software: ['Email', 'Office suite'],
    Network: ['VPN', 'Wi-Fi'],
    'Accounts & Access': ['Password reset', 'New starter'],
  }
  const items = {}
  for (const [cat, names] of Object.entries(CATALOGUE)) {
    types[cat] = {}
    for (const [i, name] of names.entries()) {
      const tp = await call(admin, 'POST', `/api/v1/admin/categories/${cats[cat]}/types`, { name, sort_order: i }, 201)
      types[cat][name] = tp.id
    }
  }
  for (const [name, sort] of [['Screen', 0], ['Battery', 1], ['Keyboard', 2]]) {
    items[name] = (await call(admin, 'POST', `/api/v1/admin/categories/${cats.Hardware}/types/${types.Hardware.Laptop}/items`, { name, sort_order: sort }, 201)).id
  }
  // SHOTS_HW_FIELD=1: a field on Hardware, so shot 06 shows the Fields section with one in it (see screenshots.sh).
  if (process.env.SHOTS_HW_FIELD === '1') {
    const f = await call(admin, 'POST', '/api/v1/admin/custom-fields', { name: 'Serial number', field_type: 'text' }, 201)
    await call(admin, 'POST', `/api/v1/admin/categories/${cats.Hardware}/fields`, { field_def_id: f.id, sort_order: 1, visible_on_new: true, required_on_new: false }, 201)
  }
  console.log('B. catalogue created')

  // C. People: users, then the disabled account, then groups with scopes (scopes before tickets, or auto-assign does nothing).
  const U = {}
  for (const key of ['jordan', 'sam', 'morgan', 'casey', 'taylor', 'drew']) {
    const p = P[key]
    U[key] = (await call(admin, 'POST', '/api/v1/admin/users', { email: p.email, display_name: p.name, role: p.role, password: PASSWORD }, 201)).id
  }
  U.admin = (await call(admin, 'GET', '/api/v1/admin/users', null, 200)).find((u) => u.email === P.admin.email)?.id
  if (!U.admin) throw new Error('admin user not found in the user list')
  await call(admin, 'PATCH', `/api/v1/admin/users/${U.drew}`, { disabled: true }, 200)

  const G = {}
  const group = async (name, description, members, scopes) => {
    G[name] = (await call(admin, 'POST', '/api/v1/admin/groups', { name, description }, 201)).id
    for (const m of members) await call(admin, 'POST', `/api/v1/admin/groups/${G[name]}/members`, { user_id: U[m] }, 204)
    for (const s of scopes) await call(admin, 'POST', `/api/v1/admin/groups/${G[name]}/scopes`, { category_id: cats[s] }, 204)
  }
  await group('IT Support', 'Desk-side, software and account support', ['jordan'], ['Hardware', 'Software', 'Accounts & Access'])
  await group('Network Team', 'Connectivity, VPN and Wi-Fi', ['sam'], ['Network'])
  console.log('C. people and groups created')

  // D. Content: canned responses, then webhooks (before tickets, so the events fire).
  const canned = [
    { name: 'Acknowledgement', body: 'Thanks for getting in touch. We have your request and someone from the team will pick it up shortly.', sort_order: 1 },
    { name: 'Laptop screen replacement scheduled', body: BODY.reply_canned, category_id: cats.Hardware, type_id: types.Hardware.Laptop, sort_order: 1 },
    { name: 'Password reset instructions', body: "Use 'Forgot password' on the sign-in page, then follow the link in the email. The link expires after one hour.", category_id: cats['Accounts & Access'], type_id: types['Accounts & Access']['Password reset'], sort_order: 1 },
    { name: 'VPN troubleshooting steps', body: 'Please disconnect, quit the VPN client, reconnect to Wi-Fi and sign in again. If it still drops, reply with the time it happened.', category_id: cats.Network, type_id: types.Network.VPN, sort_order: 2 },
  ]
  for (const cr of canned) await call(admin, 'POST', '/api/v1/admin/canned-responses', clean(cr), 201)

  const features = { webhooks: true, webhookDelivered: false, webhookOk: false, reputation: REPUTATION }
  const hookIDs = []
  for (const wh of WEBHOOKS) {
    const r = await raw(admin, 'POST', '/api/v1/admin/webhooks', wh)
    if (r.status === 400 && JSON.stringify(r.body).includes('invalid_url')) {
      // No DNS for the example hosts: drop what was made and skip the webhook shot.
      for (const id of hookIDs) await call(admin, 'DELETE', `/api/v1/admin/webhooks/${id}`, null, [200, 204])
      hookIDs.length = 0
      features.webhooks = false
      console.log('D. webhook URL did not resolve; webhooks skipped')
      break
    }
    if (r.status !== 201) throw new Error(`POST /api/v1/admin/webhooks -> ${r.status}: ${JSON.stringify(r.body).slice(0, 300)}`)
    hookIDs.push(r.body.id)
  }
  if (features.webhooks) console.log('D. canned responses and webhooks created')
  else console.log('D. canned responses created')

  // E. Tickets. Filing order fixes the tracking numbers; T8 is filed in step F.
  const S = {}
  for (const s of await call(admin, 'GET', '/api/v1/statuses', null, 200)) S[s.name] = s.id
  for (const n of ['New', 'In Progress', 'Pending', 'Resolved', 'Closed']) if (!S[n]) throw new Error(`status ${n} missing`)
  const T = {}
  const CAT = (name) => cats[name]
  const file = async (key, actor, body) => {
    const t = await call(actor, 'POST', '/api/v1/tickets', clean(body), 201)
    T[key] = { id: t.id, tracking: t.tracking_number }
    return t
  }
  const patch = (actor, key, body) => call(actor, 'PATCH', `/api/v1/tickets/${T[key].id}`, body, 200)
  const reply = (actor, key, body, internal = false) =>
    call(actor, 'POST', `/api/v1/tickets/${T[key].id}/replies`, { body, internal, notify_customer: false }, 201)
  const tag = (actor, key, name) => call(actor, 'POST', `/api/v1/tickets/${T[key].id}/tags`, { name }, 201)
  const link = (actor, key, target, link_type) => call(actor, 'POST', `/api/v1/tickets/${T[key].id}/links`, { target_id: T[target].id, link_type }, [200, 201, 204])
  const attach = (actor, key, name, buffer) => upload(actor, `/api/v1/tickets/${T[key].id}/attachments`, name, buffer)

  const as = {}
  const who = async (key) => (as[key] ??= await login(P[key].email))

  // Temp files for the uploads. The EICAR copy is removed in the finally block below.
  const tmp = mkdtempSync(join(tmpdir(), 'ghd-seed-'))
  try {
    const files = {
      'device-info.txt': BODY.device_info,
      'email-headers.txt': BODY.email_headers,
      'invoice-2026-0417.txt': EICAR,
    }
    for (const [name, data] of Object.entries(files)) writeFileSync(join(tmp, name), data)
    const buf = (name) => readFileSync(join(tmp, name))

    // T1: Morgan reports; the category auto-assigns it to IT Support.
    await file('T1', await who('morgan'), { subject: 'Laptop screen is cracked', description: 'The lid hinge gave way and the internal panel is cracked across the bottom corner. The laptop still boots and the external monitor works.', category_id: CAT('Hardware'), type_id: types.Hardware.Laptop, item_id: items.Screen })
    const jordan = await who('jordan')
    await patch(jordan, 'T1', { assignee_user_id: U.jordan })
    await patch(jordan, 'T1', { status_id: S['In Progress'] })
    await reply(jordan, 'T1', BODY.reply_canned)
    await reply(jordan, 'T1', 'Asset tag LT-0412, still under warranty.', true)
    await tag(jordan, 'T1', 'hardware-swap')
    await attach(await who('morgan'), 'T1', 'device-info.txt', buf('device-info.txt'))

    // T2: Sam reports on the Network Team queue.
    await file('T2', await who('sam'), { subject: 'VPN drops every few minutes', description: "The VPN client disconnects every few minutes on the office Wi-Fi and reconnects only after a restart. It started after Tuesday's update.", category_id: CAT('Network'), type_id: types.Network.VPN, priority: 'high' })
    const sam = await who('sam')
    await patch(sam, 'T2', { status_id: S['In Progress'] })
    await reply(sam, 'T2', BODY.reply_vpn)

    // T3: Casey uploads a clean file and the EICAR sample, which is quarantined.
    await file('T3', await who('casey'), { subject: 'Supplier invoice attachment will not open', description: 'The supplier sent an invoice by email, but the attachment will not open on my laptop. I need it for the month-end payment run.', category_id: CAT('Software'), type_id: types.Software.Email })
    const casey = await who('casey')
    await attach(casey, 'T3', 'email-headers.txt', buf('email-headers.txt'))
    await attach(casey, 'T3', 'invoice-2026-0417.txt', buf('invoice-2026-0417.txt'))

    // T4: Taylor reports; Jordan tags it.
    await file('T4', await who('taylor'), { subject: 'Printer keeps jamming on floor 3', description: 'The floor 3 printer jams on every third page in the tray. Clearing the jam does not help for long.', category_id: CAT('Hardware'), type_id: types.Hardware.Printer })
    await tag(jordan, 'T4', 'waiting-on-vendor')

    // T5: Morgan reports and it stays New.
    await file('T5', await who('morgan'), { subject: "Outlook won't sync new email", description: 'New messages arrive on my phone but not in Outlook on my laptop. Sending works fine.', category_id: CAT('Software'), type_id: types.Software.Email })

    // T6: Casey, password reset. Jordan replies here; the resolve is in step F.
    await file('T6', casey, { subject: 'Need a password reset for the portal', description: 'I am locked out of the supplier portal after too many attempts.', category_id: CAT('Accounts & Access'), type_id: types['Accounts & Access']['Password reset'] })
    await reply(jordan, 'T6', BODY.reply_password)

    // T7: admin files a critical new-starter ticket, assigns the group, links it to T1.
    await file('T7', admin, { subject: 'New starter needs a laptop and accounts', description: 'Starts Monday. Needs a laptop, email, and portal access set up before the first day.', category_id: CAT('Accounts & Access'), type_id: types['Accounts & Access']['New starter'], priority: 'critical' })
    await patch(admin, 'T7', { assignee_group_id: G['IT Support'] })
    await patch(admin, 'T7', { status_id: S['In Progress'] })
    await link(admin, 'T7', 'T1', 'related_to')

    // T9: Taylor reports; Jordan replies and resolves. The admin closes it in step F.
    await file('T9', await who('taylor'), { subject: 'Monitor flickers after docking', description: 'The monitor flickers every time I dock the laptop, then settles after a few seconds.', category_id: CAT('Hardware'), type_id: types.Hardware.Monitor })
    await reply(jordan, 'T9', BODY.reply_monitor)
    await call(jordan, 'POST', `/api/v1/tickets/${T.T9.id}/resolve`, { notes: 'Replaced the dock cable.' }, 200)

    // T10: a guest, no session. The tracking number comes back in the create response.
    const guest = await raw(anon, 'POST', '/api/v1/guest/tickets', { subject: 'Cannot join the visitor Wi-Fi', description: 'The visitor Wi-Fi asks for a code I was never given. I am here for the 10:00 meeting.', category_id: CAT('Network'), guest_email: PAT.email, guest_name: PAT.name })
    if (guest.status !== 201) throw new Error(`POST /api/v1/guest/tickets -> ${guest.status}: ${JSON.stringify(guest.body).slice(0, 300)}`)
    const all = await call(admin, 'GET', '/api/v1/tickets?scope=all&limit=200', null, 200)
    const found = all.find((t) => t.tracking_number === guest.body.tracking_number)
    if (!found) throw new Error(`guest ticket ${guest.body.tracking_number} not visible to the admin`)
    T.T10 = { id: found.id, tracking: found.tracking_number }
    console.log('E. tickets filed')

    // F. Audit tail: the newest entries, in this order.
    await call(admin, 'POST', `/api/v1/tickets/${T.T9.id}/close`, null, 200)
    await call(jordan, 'POST', `/api/v1/tickets/${T.T6.id}/resolve`, { notes: 'Reset the portal password and confirmed sign-in with the requester.' }, 200)
    await call(admin, 'POST', `/api/v1/admin/users/${U.casey}/password`, { new_password: PASSWORD }, 204)
    await patch(sam, 'T2', { assignee_user_id: U.sam })
    await patch(jordan, 'T4', { status_id: S.Pending })
    await file('T8', await who('taylor'), { subject: 'Wi-Fi drops in conference room B', description: 'The Wi-Fi in conference room B drops for a minute or two during every meeting.', category_id: CAT('Network'), type_id: types.Network['Wi-Fi'] })
    await link(sam, 'T8', 'T2', 'related_to')
    console.log('F. audit tail created')
  } finally {
    rmSync(tmp, { recursive: true, force: true })
  }

  // H. Webhook deliveries are asynchronous; poll up to 30 s for every enabled hook to record one.
  // Runs after section F, so every enabled hook's events (Teams: resolved and closed) have fired by now.
  if (features.webhooks) {
    const deadline = Date.now() + 30_000
    let delivered = false
    while (Date.now() < deadline) {
      const hooks = await call(admin, 'GET', '/api/v1/admin/webhooks', null, 200)
      delivered = hooks.filter((h) => h.enabled).every((h) => h.last_delivery)
      if (delivered) break
      await new Promise((r) => setTimeout(r, 1000))
    }
    features.webhookDelivered = delivered
    if (!delivered) console.warn('H. warning: not every enabled webhook recorded a delivery within 30 s')
    else console.log('H. webhook deliveries recorded')
    if (WEBHOOK_OK_URL) {
      // The shot waits for two "Delivered", so say here whether the endpoint answered both hooks.
      const hooks = await call(admin, 'GET', '/api/v1/admin/webhooks', null, 200)
      const ours = hooks.filter((h) => h.url === WEBHOOK_OK_URL)
      features.webhookOk = ours.length === 2 && ours.every((h) => h.last_delivery?.error === '')
      if (!features.webhookOk) {
        for (const h of ours) console.warn(`H. last delivery to SHOTS_WEBHOOK_OK_URL (${h.payload_format}): ${JSON.stringify(h.last_delivery ?? null)}`)
        // Shot 18 is gated on webhookDelivered, which a failed delivery also sets, so clear it:
        // without this the shot would still be taken, with a hook Failing.
        features.webhookDelivered = false
        console.warn('H. warning: SHOTS_WEBHOOK_OK_URL did not answer both hooks with 2xx; shot 18 will not be captured')
      }
    }
  }

  // Reputation lookups run after the upload returns; poll up to 30 s for the quarantined file's result.
  if (features.reputation) {
    const deadline = Date.now() + 30_000
    let looked = false
    while (Date.now() < deadline) {
      const atts = await call(admin, 'GET', `/api/v1/tickets/${T.T3.id}/attachments`, null, 200)
      looked = atts.some((a) => a.filename.endsWith('.zip') && a.reputation)
      if (looked) break
      await new Promise((r) => setTimeout(r, 1000))
    }
    if (!looked) console.warn('H. warning: the quarantined attachment has no reputation result within 30 s')
    else console.log('H. attachment reputation recorded')
  }

  // Signup: the address only, then the token from the database (no mail server here).
  await call(anon, 'POST', '/api/v1/auth/signup', { email: RILEY }, 202)
  // The password goes in the child's environment (PGPASSWORD), not in argv, where `ps` would show it.
  const db = new URL(DATABASE_URL)
  const pgenv = {
    ...process.env,
    PGHOST: db.hostname,
    PGPORT: db.port || '5432',
    PGUSER: decodeURIComponent(db.username),
    PGPASSWORD: decodeURIComponent(db.password),
    PGDATABASE: decodeURIComponent(db.pathname.slice(1)),
    PGSSLMODE: db.searchParams.get('sslmode') || 'prefer',
  }
  const token = execFileSync('psql', ['-Atc', `select token from pending_registrations where lower(email)='${RILEY}'`], { encoding: 'utf8', env: pgenv }).trim()
  if (!token) throw new Error('no pending registration found for the signup address')
  const verify = await call(anon, 'GET', `/api/v1/auth/verify-email?token=${encodeURIComponent(token)}`, null, 200)
  if (verify.email !== RILEY) throw new Error('verify-email returned an unexpected address')

  const tickets = Object.fromEntries(['T1', 'T2', 'T3', 'T4', 'T5', 'T6', 'T7', 'T8', 'T9', 'T10'].map((k) => [k, T[k]]))
  const seed = {
    baseURL: BASE,
    password: PASSWORD,
    users: { admin: P.admin.email, jordan: P.jordan.email, sam: P.sam.email, morgan: P.morgan.email, casey: P.casey.email, taylor: P.taylor.email },
    tickets,
    signupToken: token,
    features,
  }
  // Owner-only: the file holds the throwaway admin password and the signup token.
  const seedPath = join(OUT, 'seed.json')
  writeFileSync(seedPath, JSON.stringify(seed, null, 2) + '\n', { mode: 0o600 })
  chmodSync(seedPath, 0o600)
  console.log(`wrote ${seedPath} (mode 0600)`)
  for (const c of contexts) await c.dispose()
}

// ── --extras: data for shots 29 and later ─────────────────────────────────────

// The guest link is not stored anywhere but in the mail the guest was sent: the
// server keeps only the token's hash. fake-smtp.mjs saved that mail. The newest
// one to the guest holds the live token, because every event on an open ticket
// rotates it.
const guestPathFromMail = async (mailDir, to) => {
  const deadline = Date.now() + 30_000
  while (Date.now() < deadline) {
    const names = existsSync(mailDir) ? readdirSync(mailDir).filter((n) => n.endsWith('.eml')) : []
    names.sort((a, b) => parseInt(b, 10) - parseInt(a, 10))
    for (const name of names) {
      // The body is quoted-printable: undo the soft line breaks before matching.
      const text = readFileSync(join(mailDir, name), 'latin1').replace(/=\r?\n/g, '')
      // Plain string match on the To: line: no regex, so nothing in the address needs escaping.
      const addressed = text
        .split(/\r?\n/)
        .some((l) => /^to:/i.test(l) && l.toLowerCase().includes(to.toLowerCase()))
      if (!addressed) continue
      const m = /\/g#([A-Za-z0-9_-]+)/.exec(text)
      if (m) return `/g#${m[1]}`
    }
    await new Promise((r) => setTimeout(r, 500))
  }
  throw new Error(`no mail to ${to} with a ticket link in ${mailDir} within 30 s`)
}


// ── --extras: the history behind the dashboard (02) and Reports (48) ──────────
//
// A help desk that has run for two months, not for two minutes: SLA tracking
// with policies, a trend over many days, a comparison period, an unassigned
// pair, a reopened ticket, one breached and one at-risk ticket, and an
// administrator who is in a group and holds tickets. Every ticket is filed
// through the API, which stamps it "now". Each one is also written to
// $SHOTS_OUT/history.json with the age it should have, and backdate.sql moves
// it there (scripts/screenshots.sh runs that between this seed and the extras
// capture).
//
// Times are minutes. In a plan: c is the age of the ticket when the shots are
// taken, fr the minutes from filing to the first staff reply, res the minutes
// from filing to the resolution, clo the minutes from the resolution to the
// close, upd how long ago the ticket last changed (worked out when it is left
// out) and ro how long ago the ticket was reopened.
const day = (n) => Math.round(n * 1440)

const WORK = {
  Hardware: 'Thanks for the report. A technician has taken a look and will confirm the next step by email.',
  Software: 'Thanks for the details. We are checking the account and the licence and will update this ticket shortly.',
  Network: 'Thanks. The problem shows in the gateway logs and we are working on a fix.',
  'Accounts & Access': 'Thanks. Your identity is confirmed and the account is being updated now.',
}

// The ten tickets of the main seed are moved to these ages, so that the
// older ones count in the previous period and the trend has no spike on the day
// of the run. T6 was resolved and then reopened by the requester (history()).
const LEGACY = {
  T1: { c: day(9), upd: 120 },
  T2: { c: day(21), upd: day(2) },
  T3: { c: day(5), upd: day(1) },
  T4: { c: day(33), upd: day(4) },
  T5: { c: day(2), upd: day(2) },
  T6: { c: day(40), res: day(3), ro: 120, upd: 120 },
  T7: { c: day(14), upd: day(1) },
  T8: { c: day(3), upd: day(3) },
  T9: { c: day(18), res: 180, clo: day(2) },
  T10: { c: day(6), upd: day(6) },
}

// req: the requester. by: the staff member who holds it. cat and type are
// names. state: closed, resolved, open (In Progress), pending (waiting on the
// requester or a vendor: the SLA clock stops there, so an old one is not
// breached) or new. self: filed by the requester, not for them.
const PLAN = [
  // The month before last (days 58 to 32 ago): fewer tickets, slower, more late.
  { req: 'morgan', cat: 'Network', type: 'Wi-Fi', pri: 'medium', by: 'sam', state: 'closed', c: day(58), fr: 50, res: 1300, clo: day(1), subject: 'Cannot connect to the guest Wi-Fi from the lobby', desc: 'The guest network asks for a code at the front desk and none of the codes on the card work.' },
  { req: 'morgan', cat: 'Software', type: 'Office suite', pri: 'medium', by: 'jordan', state: 'closed', c: day(57), fr: 90, res: 1200, clo: day(1), subject: 'Cannot install the PDF editor', desc: 'The installer stops at 40 percent with an access error on my laptop.' },
  { req: 'taylor', cat: 'Hardware', type: 'Laptop', pri: 'medium', by: 'jordan', state: 'closed', c: day(55), fr: 70, res: 1700, clo: day(1), subject: 'Laptop screen flickers on battery', desc: 'The screen flickers whenever the laptop is unplugged and stops when I plug the charger in.' },
  { req: 'casey', cat: 'Accounts & Access', type: 'Password reset', pri: 'high', by: 'jordan', state: 'closed', c: day(53), fr: 30, res: 300, clo: day(2), subject: 'Password reset for the contractor portal', desc: 'The contractor portal rejects my password after the quarterly change and I cannot submit hours.' },
  { req: 'taylor', cat: 'Hardware', type: 'Laptop', pri: 'high', by: 'jordan', state: 'closed', c: day(52), fr: 110, res: 5000, clo: day(1), subject: 'Laptop charger gets hot and cuts out', desc: 'The charger is too hot to touch after an hour and the laptop switches to battery on its own.' },
  { req: 'morgan', cat: 'Hardware', type: 'Printer', pri: 'low', by: 'jordan', state: 'closed', c: day(51), fr: 300, res: 4500, clo: day(1), subject: 'Printer on floor 1 shows offline', desc: 'Every laptop on floor 1 lists the printer as offline since Monday, but it prints from the panel.' },
  { req: 'taylor', cat: 'Network', type: 'VPN', pri: 'medium', by: 'sam', state: 'closed', c: day(49), fr: 120, res: 1500, clo: day(1), subject: 'VPN client will not install on the new laptop', desc: 'The installer says a newer version is already present, but there is no VPN client in the app list.' },
  { req: 'casey', cat: 'Hardware', type: 'Monitor', pri: 'low', by: 'jordan', state: 'closed', c: day(47), fr: 400, res: 3900, clo: day(2), subject: 'Request for a second monitor', desc: 'Finance would like a second monitor at the shared desk on floor 2.' },
  { req: 'morgan', cat: 'Software', type: 'Email', pri: 'high', by: 'admin', state: 'closed', c: day(45), fr: 40, res: 900, clo: day(1), subject: 'Shared mailbox missing from Outlook', desc: 'The accounts mailbox disappeared from Outlook this morning. Webmail still shows it.' },
  { req: 'casey', cat: 'Network', type: 'Wi-Fi', pri: 'medium', by: 'sam', state: 'closed', c: day(44), fr: 100, res: 1100, clo: day(1), subject: 'No Wi-Fi in meeting room 4', desc: 'Laptops see the network in meeting room 4 but cannot get an address.' },
  { req: 'taylor', cat: 'Accounts & Access', type: 'New starter', pri: 'critical', by: 'admin', state: 'closed', c: day(43), fr: 12, res: 230, clo: day(1), subject: 'New starter: accounts and laptop for Monday', desc: 'A new analyst starts on Monday and needs a laptop, email and portal access.' },
  { req: 'casey', cat: 'Network', type: 'Wi-Fi', pri: 'medium', by: 'sam', state: 'resolved', c: day(41), fr: 200, res: 1500, subject: 'Teams meetings freeze on video calls', desc: 'Video freezes for about ten seconds every few minutes on the office Wi-Fi. Audio is fine.' },
  { req: 'morgan', cat: 'Accounts & Access', type: 'Password reset', pri: 'low', by: 'jordan', state: 'closed', c: day(40), fr: 150, res: 700, clo: day(1), subject: 'Expired password warning keeps appearing', desc: 'Windows says my password expires in 0 days every time I sign in, even after changing it.' },
  { req: 'morgan', cat: 'Software', type: 'Office suite', pri: 'low', by: 'jordan', state: 'closed', c: day(39), fr: 350, res: 2800, clo: day(1), subject: 'Excel macro security warning on the budget file', desc: 'The budget workbook opens with a red security bar and the macros are switched off.' },
  { req: 'taylor', cat: 'Hardware', type: 'Monitor', pri: 'medium', by: 'jordan', state: 'closed', c: day(37), fr: 60, res: 1100, clo: day(1), subject: 'Monitor goes black after a few minutes', desc: 'The monitor on my desk goes black every few minutes and comes back when I move the mouse.' },
  { req: 'casey', cat: 'Software', type: 'Email', pri: 'low', by: 'jordan', state: 'pending', c: day(36), fr: 200, subject: 'Archive the mailboxes of departed staff', desc: 'Please archive the mailboxes of the four people who left in September. Finance still needs read access.', reply: 'Thanks. Please send the four names and who should keep read access, and we will schedule the archive.' },
  { req: 'casey', cat: 'Accounts & Access', type: 'Password reset', pri: 'high', by: 'jordan', state: 'closed', c: day(35), fr: 45, res: 600, clo: day(2), subject: 'Cannot sign in after the MFA reset', desc: 'After the MFA reset the portal says the code is invalid. The authenticator shows a new code every 30 seconds.' },
  { req: 'morgan', cat: 'Network', type: 'VPN', pri: 'critical', by: 'sam', state: 'closed', c: day(34), fr: 8, res: 260, clo: day(1), subject: 'VPN gateway rejects every login from the east office', desc: 'Nobody in the east office can connect to the VPN since 08:00. The credentials work on the web portal.' },

  // The last 30 days: busier, quicker, mostly on time.
  { req: 'morgan', cat: 'Hardware', type: 'Laptop', pri: 'medium', by: 'jordan', state: 'closed', c: day(29), fr: 40, res: 700, clo: day(2), subject: 'Laptop battery drains within an hour', desc: 'A fully charged battery is empty after about an hour of light work. It is two years old.' },
  { req: 'casey', cat: 'Accounts & Access', type: 'Password reset', pri: 'high', by: 'jordan', state: 'resolved', c: day(27), fr: 25, res: 300, subject: 'Locked out of the finance portal', desc: 'The finance portal locked my account after three attempts and I have a payment run at noon.' },
  { req: 'morgan', cat: 'Network', type: 'VPN', pri: 'high', by: 'sam', state: 'closed', c: day(26), fr: 50, res: 700, clo: day(2), subject: 'VPN will not connect from home', desc: 'The VPN says the gateway is unreachable from my home connection. Other sites load normally.' },
  { req: 'taylor', cat: 'Hardware', type: 'Laptop', pri: 'high', by: 'jordan', state: 'resolved', c: day(24), fr: 90, res: day(2), subject: 'Keys stick on my laptop keyboard', desc: 'The E and R keys stick and repeat. I cannot type a password without retyping it.' },
  { req: 'casey', cat: 'Software', type: 'Email', pri: 'medium', by: 'admin', state: 'closed', c: day(23), fr: 60, res: 600, clo: day(1), subject: 'Outlook keeps asking for my password', desc: 'Outlook shows a sign-in box every few minutes. Webmail works without asking.' },
  { req: 'taylor', cat: 'Hardware', type: 'Monitor', pri: 'low', by: 'jordan', state: 'closed', c: day(21), fr: 200, res: 2000, clo: day(2), subject: 'Monitor arm for the new standing desk', desc: 'The new standing desk has no room for the monitor foot. Could we fit an arm?' },
  { req: 'morgan', cat: 'Network', type: 'Wi-Fi', pri: 'medium', by: 'sam', state: 'resolved', c: day(20), fr: 30, res: 1500, subject: 'Wi-Fi drops in the east wing', desc: 'The Wi-Fi drops for a minute in the east wing about every hour. The wired desks are fine.' },
  { req: 'casey', cat: 'Accounts & Access', type: 'New starter', pri: 'critical', by: 'admin', state: 'closed', c: day(19), fr: 10, res: 200, clo: day(1), subject: 'New starter needs a laptop, email and portal access', desc: 'A new analyst starts tomorrow. Please prepare a laptop, a mailbox and portal access.' },
  { req: 'morgan', cat: 'Software', type: 'Office suite', pri: 'medium', by: 'jordan', state: 'closed', c: day(17), fr: 90, res: 800, clo: day(1), subject: 'Excel crashes when opening the large budget workbook', desc: 'Excel closes without a message when I open the 60 MB budget workbook.' },
  { req: 'taylor', cat: 'Hardware', type: 'Printer', pri: 'low', by: 'jordan', state: 'closed', c: day(16), fr: 120, res: 900, clo: day(1), subject: 'Printer on floor 2 is out of toner', desc: 'The printer on floor 2 shows a toner empty message and prints pale pages.' },
  { req: 'casey', cat: 'Network', type: 'Wi-Fi', pri: 'medium', by: 'sam', state: 'resolved', c: day(14), fr: 45, res: 400, subject: 'Video calls drop on the conference room Wi-Fi', desc: 'Calls from the big conference room drop twice an hour. The Wi-Fi bars are full.' },
  { req: 'morgan', cat: 'Accounts & Access', type: 'Password reset', pri: 'high', by: 'admin', state: 'closed', c: day(13), fr: 20, res: 120, clo: day(1), subject: 'Locked out after a password change', desc: 'I changed my password on my phone and now my laptop locks the account every few minutes.' },
  { req: 'taylor', cat: 'Hardware', type: 'Laptop', pri: 'high', by: 'jordan', state: 'resolved', c: day(12), fr: 80, res: 3000, subject: 'Docking station does not charge the laptop', desc: 'The docking station connects the screens but the laptop does not charge on it.' },
  { req: 'morgan', cat: 'Network', type: 'Wi-Fi', pri: 'low', by: 'sam', state: 'closed', c: day(10), fr: 150, res: 1800, clo: day(2), subject: 'Wi-Fi access for a visiting auditor', desc: 'An auditor is on site for two weeks and needs Wi-Fi access for a laptop that is not ours.' },
  { req: 'taylor', cat: 'Software', type: 'Email', pri: 'critical', by: 'sam', state: 'resolved', c: day(9), fr: 6, res: 180, subject: 'Executive mailbox is not receiving external email', desc: 'The finance director has received nothing from outside the company since 07:30.' },
  { req: 'casey', cat: 'Hardware', type: 'Laptop', pri: 'high', by: 'jordan', state: 'closed', c: day(8), fr: 80, res: 2500, clo: day(1), subject: 'Laptop fan runs at full speed all day', desc: 'The fan never slows down, even when nothing is open, and the case is hot.' },
  { req: 'morgan', cat: 'Accounts & Access', type: 'Password reset', pri: 'medium', by: 'jordan', state: 'resolved', c: day(6), fr: 30, res: 200, subject: 'Reset my portal password', desc: 'I forgot the portal password and the reset mail does not arrive.' },
  { req: 'taylor', cat: 'Network', type: 'VPN', pri: 'medium', by: 'sam', state: 'resolved', c: day(5), fr: 35, res: 480, subject: 'VPN disconnects when switching Wi-Fi networks', desc: 'The VPN drops every time I move from the office Wi-Fi to the phone hotspot.' },
  { req: 'casey', cat: 'Software', type: 'Office suite', pri: 'low', by: 'admin', state: 'resolved', c: day(4), fr: 100, res: 600, subject: 'Word template missing from the shared library', desc: 'The letter template is gone from the shared template library.' },
  { req: 'morgan', cat: 'Hardware', type: 'Monitor', pri: 'medium', by: 'jordan', state: 'resolved', c: day(3), fr: 20, res: 240, subject: 'Second monitor not detected after docking', desc: 'After docking the second monitor stays black until I unplug and replug the cable.' },
  { req: 'taylor', cat: 'Hardware', type: 'Laptop', pri: 'medium', by: 'jordan', state: 'resolved', c: day(2), fr: 25, res: 300, subject: 'Trackpad does not click', desc: 'The trackpad moves the pointer but a press does not register as a click.' },
  { req: 'casey', cat: 'Accounts & Access', type: 'Password reset', pri: 'high', by: 'admin', state: 'resolved', c: day(1), fr: 15, res: 90, subject: 'No code arrives when I reset my password', desc: 'The reset page says a code was sent, but nothing reaches my mailbox.' },

  // Open now. The printer tickets fill the Printer filter of the ticket list
  // shots. The word "fuser" is in the reply on the first and in an internal
  // note on the second, and nowhere else.
  { req: 'taylor', cat: 'Hardware', type: 'Printer', pri: 'high', by: 'jordan', state: 'open', c: day(3), fr: 55, subject: 'Printer on the 3rd floor jams on tray 2', desc: 'The 3rd floor printer jams on every third page from tray 2. Clearing the jam does not help for long.', reply: 'Thanks for the detail. The paper is creasing at the fuser, so I have ordered a new fuser kit and will fit it Thursday.' },
  { req: 'casey', cat: 'Hardware', type: 'Printer', pri: 'high', by: 'jordan', state: 'pending', c: day(6), fr: 80, subject: 'Toner warning stays on after replacement', desc: 'The printer on floor 4 still shows the toner warning after I fitted the new cartridge.', reply: 'Thanks. We are checking the service counter with the vendor and will come back to you.', note: 'Spoke to the vendor: the counter resets with the fuser, not the toner. Check the service menu before ordering a second cartridge.' },
  { req: 'morgan', cat: 'Hardware', type: 'Printer', pri: 'high', by: 'sam', state: 'open', c: day(2), fr: 60, subject: 'Duplex printing smudges the back of each page', desc: 'Double-sided prints come out with a grey smear along one edge of the back of every page.' },
  { req: 'taylor', cat: 'Hardware', type: 'Printer', pri: 'high', by: 'jordan', state: 'pending', c: day(9), fr: 70, subject: 'Reception badge printer ignores queued jobs', desc: 'Visitor badges stay in the queue at reception and the printer light stays green.' },
  { req: 'casey', cat: 'Hardware', type: 'Printer', pri: 'high', by: 'sam', state: 'open', c: day(1), fr: 30, subject: 'Plotter prints with a blue cast', desc: 'Everything from the plotter in the design room comes out with a blue tint.' },
  { req: 'taylor', cat: 'Hardware', type: 'Laptop', pri: 'medium', by: 'sam', state: 'open', c: 360, fr: 40, subject: 'Laptop will not boot after the overnight update', desc: 'The laptop shows the logo and then a black screen after last night’s update.' },
  { req: 'taylor', cat: 'Hardware', type: 'Laptop', pri: 'medium', by: 'jordan', state: 'pending', c: day(8), fr: 120, subject: 'Warranty claim for the cracked docking station', desc: 'The docking station cracked when it fell off the desk. It is still in warranty.', reply: 'Thanks. We have sent the claim to the supplier and are waiting for their reply.' },
  { req: 'morgan', cat: 'Software', type: 'Office suite', pri: 'low', by: 'jordan', state: 'pending', c: day(15), fr: 200, subject: 'Software licence request for the design tools', desc: 'The design team needs three more licences for the layout tool.', reply: 'Thanks. The purchase request is with finance and we will install the licences when it is approved.' },
  { req: 'casey', cat: 'Network', type: 'Wi-Fi', pri: 'low', by: 'sam', state: 'pending', c: day(24), fr: 90, subject: 'Wi-Fi coverage survey for building C', desc: 'Building C has weak coverage on the second floor. Could someone survey it?', reply: 'Thanks. The survey is booked for when the contractor returns, and we will share the heat map.' },

  // What the dashboard's queue strip and attention list are built on. Alex
  // holds the first two. The first is breached on both targets: nobody has
  // answered it. The second is at risk: its resolution has used 90 percent of
  // the day the policy allows, and the requester has just written back.
  { req: 'casey', cat: 'Software', type: 'Office suite', pri: 'high', by: 'admin', state: 'open', c: 840, subject: 'Month-end workbook is locked and its owner is out', desc: 'The month-end workbook is locked by a colleague on leave. Finance cannot post the accruals.' },
  { req: 'morgan', cat: 'Software', type: 'Email', pri: 'medium', by: 'admin', state: 'open', c: 1296, fr: 35, upd: 20, follow: 'Thanks, I have tried that and the calendar still does not sync with my phone.', subject: 'Outlook calendar will not sync with my phone', desc: 'Meetings I accept on my laptop never reach my phone calendar.', reply: 'Thanks. I have reset the sync profile on the server. Please remove and add the account on your phone and tell me how it goes.' },
  // Filed by the requesters themselves in a category no group covers: they land
  // in Available, with no group and no person.
  { req: 'taylor', cat: 'Facilities', type: 'Building access', self: true, state: 'new', c: 150, subject: 'Badge reader at the loading dock is not working', desc: 'The badge reader at the loading dock does not beep or open the door, so deliveries wait outside.' },
  { req: 'casey', cat: 'Facilities', type: 'Desk moves', self: true, state: 'new', c: 60, subject: 'Request to move my desk to the second floor', desc: 'I would like to move to the second floor with the rest of the finance team.' },
]

// The targets of each priority, in minutes, and the longer ones Hardware gets at high priority.
const POLICIES = [
  { name: 'Critical', priority: 'critical', response_target_min: 15, resolution_target_min: 240 },
  { name: 'High', priority: 'high', response_target_min: 60, resolution_target_min: 480 },
  { name: 'Medium', priority: 'medium', response_target_min: 240, resolution_target_min: 1440 },
  { name: 'Low', priority: 'low', response_target_min: 480, resolution_target_min: 4320 },
]
const HARDWARE_HIGH = { name: 'High, hardware', priority: 'high', response_target_min: 120, resolution_target_min: 10080 }

// Returns what backdate.sql needs: one entry per ticket to move in time.
async function history(admin, seed, U) {
  const as = {}
  const who = async (key) => (as[key] ??= await login(P[key].email))
  const hist = []

  const S = {}
  for (const s of await call(admin, 'GET', '/api/v1/statuses', null, 200)) S[s.name] = s.id
  const groups = await call(admin, 'GET', '/api/v1/admin/groups', null, 200)
  const it = groups.find((g) => g.name === 'IT Support')?.id
  const net = groups.find((g) => g.name === 'Network Team')?.id
  if (!it || !net) throw new Error('IT Support or Network Team group not found')

  // Two more people in IT Support, so "assigned to others" and the workload
  // card have somebody to show. Alex is an administrator and holds tickets.
  for (const m of ['admin', 'sam']) await call(admin, 'POST', `/api/v1/admin/groups/${it}/members`, { user_id: U[m] }, 204)

  // Facilities: a category no group covers, so its tickets have no group.
  const facilities = await call(admin, 'POST', '/api/v1/admin/categories', { name: 'Facilities', sort_order: 5 }, 201)
  const cats = {}
  const types = {}
  for (const c of await call(admin, 'GET', '/api/v1/admin/categories', null, 200)) {
    cats[c.name] = c.id
    types[c.name] = {}
    if (c.name === 'Desk phones') continue
    for (const t of await call(admin, 'GET', `/api/v1/admin/categories/${c.id}/types`, null, 200)) types[c.name][t.name] = t.id
  }
  for (const [i, name] of ['Building access', 'Desk moves'].entries()) {
    types.Facilities[name] = (await call(admin, 'POST', `/api/v1/admin/categories/${facilities.id}/types`, { name, sort_order: i }, 201)).id
  }
  const groupOf = { Hardware: it, Software: it, 'Accounts & Access': it, Network: net }

  const reply = (ctx, id, body, internal = false) =>
    call(ctx, 'POST', `/api/v1/tickets/${id}/replies`, { body, internal, notify_customer: false }, 201)

  // ── Legacy phase: SLA is still off. Morgan's reply on T1 makes it wait for
  // Jordan, and Casey's reply on T6, which was resolved, reopens it.
  await reply(await who('morgan'), seed.tickets.T1.id, 'Thanks. The new screen arrived, but the lid still does not close all the way. Can someone look at the hinge as well?')
  await reply(await who('casey'), seed.tickets.T6.id, 'The reset worked, but the portal now asks me to change the password again as soon as I sign in.')
  // The main seed assigned T1 and T2 to a person alone, which left them in no
  // group; put each back in its group so the group views count them.
  await call(admin, 'PATCH', `/api/v1/tickets/${seed.tickets.T1.id}`, { assignee_user_id: U.jordan, assignee_group_id: it }, 200)
  await call(admin, 'PATCH', `/api/v1/tickets/${seed.tickets.T2.id}`, { assignee_user_id: U.sam, assignee_group_id: net }, 200)
  for (const [k, spec] of Object.entries(LEGACY)) hist.push({ id: seed.tickets[k].id, ...spec })

  // Two tickets that were resolved, reopened by the requester and resolved
  // again: what makes the reopen rate more than zero. Filed while SLA is off, so
  // the reopen does not start a second SLA record that would also need moving.
  const reopened = [
    { req: 'taylor', cat: 'Software', type: 'Email', pri: 'low', by: 'jordan', subject: 'Email signature is missing on the new laptop', desc: 'My signature is blank in Outlook on the new laptop. It was fine on the old one.', c: day(19), fr: 90, res: day(2) + 300, ro: day(17) },
    { req: 'casey', cat: 'Hardware', type: 'Printer', pri: 'medium', by: 'jordan', subject: 'Badge printer prints blank cards', desc: 'The badge printer feeds a card and ejects it blank.', c: day(12), fr: 60, res: 4200, ro: day(12) - 3600 },
  ]
  for (const p of reopened) {
    const t = await call(admin, 'POST', '/api/v1/tickets', clean({ subject: p.subject, description: p.desc, category_id: cats[p.cat], type_id: types[p.cat][p.type], priority: p.pri, reporter_user_id: U[p.req] }), 201)
    await call(admin, 'PATCH', `/api/v1/tickets/${t.id}`, { assignee_group_id: it, assignee_user_id: U[p.by], status_id: S['In Progress'] }, 200)
    const holder = await who(p.by)
    await reply(holder, t.id, WORK[p.cat])
    await call(holder, 'POST', `/api/v1/tickets/${t.id}/resolve`, { notes: 'Fixed and confirmed.' }, 200)
    await reply(await who(p.req), t.id, 'It has come back: the same thing happens again this morning.')
    await call(holder, 'POST', `/api/v1/tickets/${t.id}/resolve`, { notes: 'Found the cause this time and fixed it for good.' }, 200)
    // The ticket was reopened after it was filed and before the resolution that stands.
    if (!(p.ro <= p.c && p.ro > p.c - p.res)) throw new Error(`history: the reopen of "${p.subject}" is not between filing and resolution: ${JSON.stringify(p)}`)
    hist.push({ id: t.id, c: p.c, fr: p.fr, res: p.res, ro: p.ro })
  }

  // ── SLA phase.
  await call(admin, 'PATCH', '/api/v1/admin/settings', {
    sla_enabled: true,
    // The auto-close sweep (every five minutes in the server) closes a Resolved
    // ticket whose resolved_at is older than this many days (default 7).
    // backdate.sql moves resolved tickets back up to 41 days, so with the default
    // each of them would flip to Closed whenever a sweep tick landed between the
    // backdate and the capture, and the status cards and columns would differ from
    // run to run. 90 days is longer than any history filed here.
    reopen_window_days: 90,
    // Off: the dashboard's health card then has its one amber item (tracking on, alerts off).
    sla_alerts_enabled: false,
    // Staff see Available too, so Jordan's sidebar lists it.
    available_queue_roles: ['admin', 'staff'],
  }, 204)
  for (const pol of [...POLICIES, { ...HARDWARE_HIGH, category_id: cats.Hardware }]) {
    await call(admin, 'POST', '/api/v1/admin/sla/policies', pol, 201)
  }

  for (const p of PLAN) {
    const filer = p.self ? await who(p.req) : admin
    const body = p.self
      ? { subject: p.subject, description: p.desc, category_id: cats[p.cat], type_id: types[p.cat][p.type] }
      : { subject: p.subject, description: p.desc, category_id: cats[p.cat], type_id: types[p.cat][p.type], priority: p.pri, reporter_user_id: U[p.req] }
    const t = await call(filer, 'POST', '/api/v1/tickets', clean(body), 201)
    // An assignment replaces both the person and the group, so a PATCH that names
    // only the person leaves the ticket in no group: the group goes with it.
    const move = {}
    if (groupOf[p.cat]) move.assignee_group_id = groupOf[p.cat]
    if (p.by) Object.assign(move, { assignee_user_id: U[p.by], status_id: S['In Progress'] })
    if (Object.keys(move).length) await call(admin, 'PATCH', `/api/v1/tickets/${t.id}`, move, 200)

    const holder = p.by ? await who(p.by) : null
    if (p.fr != null) await reply(holder, t.id, p.reply ?? WORK[p.cat])
    if (p.follow) await reply(await who(p.req), t.id, p.follow)
    if (p.note) await reply(holder, t.id, p.note, true)
    if (p.state === 'pending') await call(holder, 'PATCH', `/api/v1/tickets/${t.id}`, { status_id: S.Pending }, 200)
    if (p.state === 'resolved' || p.state === 'closed') {
      await call(holder, 'POST', `/api/v1/tickets/${t.id}/resolve`, { notes: 'Fixed, and confirmed with the requester.' }, 200)
    }
    if (p.state === 'closed') await call(admin, 'POST', `/api/v1/tickets/${t.id}/close`, null, 200)
    hist.push({ id: t.id, c: p.c, fr: p.fr, res: p.res, clo: p.clo, upd: p.upd })
  }

  // Every plan says when things happened, so check that it can have: a ticket
  // is filed, answered, resolved and closed in that order, and not in the future.
  for (const h of hist) {
    if (!(h.c > (h.res ?? 0) + (h.clo ?? 0)) || (h.fr != null && h.res != null && h.fr > h.res)) {
      throw new Error(`history: impossible times for ${h.id}: ${JSON.stringify(h)}`)
    }
    // Where upd is left out: the last thing that happened to the ticket.
    // A reopen cannot be older than the ticket.
    if (h.ro != null && h.ro > h.c) throw new Error(`history: reopened before it was filed: ${JSON.stringify(h)}`)
    h.upd ??= h.clo != null ? h.c - h.res - h.clo : h.res != null ? h.c - h.res : h.fr != null ? h.c - h.fr : h.c
  }
  return hist
}

// The text of Hardware > Laptop (shots 54, 55). Shot 55 shows its length in the
// counter and its alt text on the site says the number, so change both together.
const LAPTOP_DESCRIPTION = 'A company laptop that is damaged, slow or will not start. Include the asset tag from the sticker underneath.'

async function extras() {
  const mailDir = process.env.SHOTS_MAIL_DIR
  if (!mailDir) throw new Error('SHOTS_MAIL_DIR is required with --extras (the directory fake-smtp.mjs writes to)')
  const seedPath = join(OUT, 'seed.json')
  const seed = JSON.parse(readFileSync(seedPath, 'utf8'))

  const admin = await login(P.admin.email)
  const jordan = await login(P.jordan.email)
  const users = await call(admin, 'GET', '/api/v1/admin/users', null, 200)
  const casey = users.find((u) => u.email === P.casey.email)?.id
  if (!casey) throw new Error('Casey Nguyen not found in the user list')

  // Staff may create their own API keys (shot 36), and guests may attach files (shot 40).
  await call(admin, 'PATCH', '/api/v1/admin/settings', { staff_can_create_api_keys: true, guest_attachments_enabled: true }, 204)

  // A category that was retired: archived, so the page shows its Restore button (shot 33).
  const cats = await call(admin, 'GET', '/api/v1/admin/categories', null, 200)
  const software = cats.find((c) => c.name === 'Software')?.id
  const office = (await call(admin, 'GET', `/api/v1/admin/categories/${software}/types`, null, 200)).find((t) => t.name === 'Office suite')?.id
  if (!software || !office) throw new Error('Software / Office suite not found: run the main seed first')
  const legacy = await call(admin, 'POST', '/api/v1/admin/categories', { name: 'Desk phones', sort_order: 4 }, 201)
  await call(admin, 'PATCH', `/api/v1/admin/categories/${legacy.id}`, { active: false }, 200)

  // T11: Jordan took a call from Casey and filed the ticket for them. The
  // description was typed as plain text that happens to use Markdown marks (the
  // API's default format), so it reads as typed until "Show formatted" is
  // pressed; the reply is Markdown and shows formatted (shots 30, 35).
  const t11 = await call(jordan, 'POST', '/api/v1/tickets', {
    subject: 'Budget workbook keeps asking me to sign in',
    description:
      'Caller: Casey Nguyen, finance, desk ext. 4417.\n\n' +
      '**What Casey sees**\n' +
      '- Opens the budget workbook from the shared drive\n' +
      '- A "Sign in to continue" box comes back every few minutes\n' +
      '- Only on the laptop; the phone is fine\n\n' +
      'Already tried: signing out and back in. No change.',
    category_id: software,
    type_id: office,
    reporter_user_id: casey,
    source_channel: 'phone',
  }, 201)
  await call(jordan, 'POST', `/api/v1/tickets/${t11.id}/replies`, {
    body:
      'Thanks, Casey. Two things to try while we look at your account:\n\n' +
      '1. Close the workbook and quit **Excel** completely.\n' +
      '2. Open it again from `Recent` and sign in once.\n\n' +
      'If the box comes back, reply with the **time** it appeared.',
    format: 'markdown',
    internal: false,
    notify_customer: false,
  }, 201)

  // Custom fields (shot 41): a select field with held values, an option at no
  // tickets, a retired option and two places. T1 (Hardware / Laptop) and T4
  // (Hardware / Printer) hold values, so those two options cannot be deleted.
  const hw = cats.find((c) => c.name === 'Hardware')?.id
  const hwTypes = await call(admin, 'GET', `/api/v1/admin/categories/${hw}/types`, null, 200)
  const laptop = hwTypes.find((t) => t.name === 'Laptop')?.id
  const printer = hwTypes.find((t) => t.name === 'Printer')?.id
  if (!hw || !laptop || !printer) throw new Error('Hardware / Laptop / Printer not found: run the main seed first')
  const field = (body) => call(admin, 'POST', '/api/v1/admin/custom-fields', body, 201)
  const place = (def, typeId, sort) =>
    call(admin, 'POST', `/api/v1/admin/categories/${hw}/types/${typeId}/fields`, { field_def_id: def.id, sort_order: sort, visible_on_new: true, required_on_new: false }, 201)
  const assetTag = await field({ name: 'Asset tag', field_type: 'text', sort_order: 1 })
  const warranty = await field({ name: 'Warranty (months)', field_type: 'number', sort_order: 2 })
  const condition = await field({ name: 'Condition on arrival', field_type: 'select', options: ['Like new', 'Good', 'Worn', 'Damaged', 'Refurbished'], sort_order: 3 })
  await place(assetTag, laptop, 1)
  await place(warranty, laptop, 2)
  await place(condition, laptop, 3)
  await place(condition, printer, 1)
  const setValue = (key, def, value) => call(admin, 'PUT', `/api/v1/tickets/${seed.tickets[key].id}/custom-fields`, { [def.id]: value }, 204)
  await setValue('T1', condition, 'Good')
  await setValue('T4', condition, 'Worn')
  await call(admin, 'PATCH', `/api/v1/admin/custom-fields/${condition.id}`, { options: ['Like new', 'Good', 'Worn', 'Damaged'], retired_options: ['Refurbished'] }, 200)

  const guestPath = await guestPathFromMail(mailDir, PAT.email)

  const ids = Object.fromEntries(users.map((u) => [u.email, u.id]))
  const hist = await history(admin, seed, {
    admin: ids[P.admin.email], jordan: ids[P.jordan.email], sam: ids[P.sam.email],
    morgan: ids[P.morgan.email], casey: ids[P.casey.email], taylor: ids[P.taylor.email],
  })
  // backdate.sql reads this (scripts/screenshots.sh hands it over). Not a secret.
  writeFileSync(join(OUT, 'history.json'), JSON.stringify(hist) + '\n')
  console.log(`X. history seeded: ${hist.length} tickets to move in time`)

  // Descriptions (shots 54, 55): on Hardware and Hardware > Laptop only. Shot 34
  // picks Software > Office suite and shot 04 and 06 are taken before this seed,
  // so none of them gains a line of text. Laptop > Screen has none, which is what
  // shot 54 shows: the item has no text of its own, so the type's text is the
  // one the form shows.
  await call(admin, 'PATCH', `/api/v1/admin/categories/${hw}`, { description: 'Laptops, printers, monitors and other equipment at your desk.' }, [200, 204])
  await call(admin, 'PATCH', `/api/v1/admin/categories/${hw}/types/${laptop}`, { description: LAPTOP_DESCRIPTION }, [200, 204])

  // Followers (shot 53): Jordan follows T7 and adds Alex. After history(), where
  // Sam joins IT Support, the group T7 is assigned to: only then can Sam see the
  // ticket, so only then does the picker offer Sam.
  await call(jordan, 'PUT', `/api/v1/tickets/${seed.tickets.T7.id}/follow`, null, [200, 204])
  await call(jordan, 'PUT', `/api/v1/tickets/${seed.tickets.T7.id}/followers/${ids[P.admin.email]}`, null, [200, 204])

  seed.extras = { tickets: { T11: { id: t11.id, tracking: t11.tracking_number } }, guestPath, fields: { condition: condition.id } }
  writeFileSync(seedPath, JSON.stringify(seed, null, 2) + '\n', { mode: 0o600 })
  chmodSync(seedPath, 0o600)
  console.log('X. extras seeded')
  for (const c of contexts) await c.dispose()
}

(process.argv.includes('--extras') ? extras() : main()).catch((e) => {
  console.error(`seed failed: ${e.message}`)
  process.exitCode = 1
})
