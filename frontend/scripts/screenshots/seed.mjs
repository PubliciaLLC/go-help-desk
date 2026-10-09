// Usage: SHOTS_BASE_URL=http://localhost:18080 DATABASE_URL=postgres://... [SHOTS_PASSWORD SHOTS_OUT SHOTS_REPUTATION=0] node frontend/scripts/screenshots/seed.mjs
import { request } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
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

const WEBHOOKS = [
  { url: 'https://example.com/hooks/helpdesk', events: ['ticket.created', 'ticket.status_changed'], secret: 'screenshot-run-signing-secret', payload_format: 'slack', enabled: true },
  { url: 'https://www.example.org/teams-webhook', events: ['ticket.resolved', 'ticket.closed'], payload_format: 'teams', enabled: true },
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

  const features = { webhooks: true, webhookDelivered: false, reputation: REPUTATION }
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

main().catch((e) => {
  console.error(`seed failed: ${e.message}`)
  process.exitCode = 1
})
