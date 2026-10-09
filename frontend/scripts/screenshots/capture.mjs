// Captures every screenshot under screenshots/ for the 1.3.0 site.
//
//   node frontend/scripts/screenshots/capture.mjs --phase pre-setup
//   node frontend/scripts/screenshots/capture.mjs --phase main [--only 05,12]
//
// --phase pre-setup runs against the empty instance, before seed.mjs (shots 15,
// 15m). --phase main needs $SHOTS_OUT/seed.json written by seed.mjs. Output is
// $SHOTS_OUT/png/*.png (default ${TMPDIR:-/tmp}/ghd-shots), plus
// $SHOTS_OUT/manifest.txt listing exactly the PNG files this run wrote, one name
// per line. Env: SHOTS_BASE_URL (pre-setup only; main reads seed.json),
// SHOTS_CHROMIUM.
//
// Line kinds printed at the end, one per shot:
//   OK    file  WxH  KB       written, pixel size shown
//   SKIP  file  -  reason    not captured because the seed lacks the data (or an
//                            optional shot failed); the reason is printed
//   FAIL  file  -  reason    a required shot or its data check failed
// Exit code is non-zero if any FAIL line was printed.
//
// Known app issue, filed separately (not worked around here): ticket descriptions
// and subjects use `break-all`, so words split mid-word in shots 05, 12 and 24.
//
// Design: scratchpad design-shots.md sections 2 and 4.4. Shot order matters:
// the passkey shot (22) registers a credential that revokes Sam's sessions, so
// it always runs last.

import fs from 'node:fs'
import path from 'node:path'

const DESK = { width: 1440, height: 900 }
const MOBILE = { width: 390, height: 844 }
const PAD = 16
const PASSWORD_FALLBACK = 'Screenshots-2026!'
const RILEY_PASSWORD = 'Riley-Screenshot-2026!'
const DEFAULT_CHROMIUM = '/opt/pw-browsers/chromium-1194/chrome-linux/chrome'
const DATA_WAIT = 25000

const OUT = process.env.SHOTS_OUT || path.join(process.env.TMPDIR || '/tmp', 'ghd-shots')
const PNG_DIR = path.join(OUT, 'png')
const SEED_FILE = path.join(OUT, 'seed.json')

const args = process.argv.slice(2)

// ── Shot table ────────────────────────────────────────────────────────────────
//
// Each shot: file, phase, role (seed user key or null), path (string or fn of
// seed), vp (viewport), mobile (bool), requires (seed.features flag, optional),
// optional (bool), passkey (bool), prepare(page, ctx, seed), target:
//   'viewport' | {tall: cap, exact?: bool} | {element: fn} | {union: fn}
//   | {cutBetween: fn}  (crop height set to a gap between two cards)
// expect: [w, h] in device pixels, checked after capture (a mismatch is a failure).

const ticketPath = (k) => (s) => `/tickets/${s.tickets[k].id}`

// The quarantined-attachment row for the EICAR archive (shots 12, 13).
const quarantinedRow = (page) => page.locator('div.border-red-300').filter({ hasText: 'invoice-2026-0417.txt.zip' })

function cardOf(page, title) {
  // Card is <div class="rounded-lg ..."> wrapping a CardHeader whose h3 is the title.
  return page
    .getByRole('heading', { name: title, exact: true })
    .locator('xpath=ancestor::div[contains(concat(" ", normalize-space(@class), " "), " rounded-lg ")][1]')
}

function sectionOf(page, title) {
  // Settings Section: <div><h2>title</h2><div class="rounded-lg border">…</div></div>
  return page.getByRole('heading', { name: title, exact: true }).locator('xpath=..')
}

// Data waits throw on timeout: a missing row is a FAIL, never a silent capture.
async function waitVisible(locator, timeout = 15000) {
  await locator.first().waitFor({ state: 'visible', timeout })
}

async function waitActivity(page) {
  await cardOf(page, 'Activity').locator('li').first().waitFor({ timeout: DATA_WAIT })
}

async function waitCirclKnown(page) {
  // CIRCL is the provider the reviewed shots depend on (design 3.2): it must say "known".
  await quarantinedRow(page).getByText(/CIRCL: known file/).first().waitFor({ timeout: DATA_WAIT })
}

const SHOTS = [
  // ── Refreshed (same names) ──
  {
    file: '01-login.png', phase: 'main', path: '/login', vp: DESK,
    async prepare() {},
  },
  {
    file: '02-dashboard.png', phase: 'main', role: 'admin', path: '/dashboard', vp: DESK,
    async prepare(page) {
      await waitVisible(page.getByRole('heading', { name: 'By Client', exact: true }))
    },
  },
  {
    file: '03-ticket-list.png', phase: 'main', role: 'admin', path: '/tickets', vp: DESK,
    async prepare(page) {
      // Admin defaults to "Mine", which is empty for Alex. Show every ticket.
      await page.getByRole('button', { name: 'all', exact: true }).click()
      await waitVisible(page.getByText('GHD-2026-000001', { exact: true }))
    },
  },
  {
    file: '04-new-ticket.png', phase: 'main', role: 'jordan', path: '/tickets/new', vp: DESK,
    async prepare(page) {
      await page.locator('#category').selectOption({ label: 'Hardware' })
      await page.locator('#type').selectOption({ label: 'Laptop' })
      await waitVisible(page.locator('#item'))
    },
  },
  {
    // break-all app issue applies (see header).
    file: '05-ticket-detail.png', phase: 'main', role: 'jordan', path: ticketPath('T1'), vp: DESK,
    async prepare(page) {
      await waitActivity(page)
    },
  },
  {
    file: '06-admin-categories.png', phase: 'main', role: 'admin', path: '/admin/categories', vp: DESK,
    async prepare(page) {
      // The name is an inline editor (clicking it edits). The chevron is the
      // second button in the row, after the drag handle.
      const row = page.locator('div.group').filter({ hasText: 'Hardware' }).first()
      await row.locator('button').nth(1).click()
      await waitVisible(page.getByText('Printer', { exact: true }))
    },
  },
  {
    file: '07-admin-users.png', phase: 'main', role: 'admin', path: '/admin/users', vp: DESK,
    async prepare(page) {
      await waitVisible(page.getByText('drew.patel@example.com'))
    },
  },
  {
    file: '08-admin-settings.png', phase: 'main', role: 'admin', path: '/admin/settings', vp: DESK,
    async prepare(page) {
      await waitVisible(page.getByRole('heading', { name: 'Tickets', exact: true }))
    },
  },
  {
    file: '09-staff-ticket-list.png', phase: 'main', role: 'jordan', path: '/tickets', vp: DESK,
    async prepare(page) {
      await waitVisible(page.getByText('GHD-2026-000001', { exact: true }))
    },
  },
  {
    file: '10-canned-responses-admin.png', phase: 'main', role: 'admin', path: '/admin/canned-responses', vp: DESK,
    async prepare(page) {
      await waitVisible(page.getByText('VPN troubleshooting steps', { exact: true }))
    },
  },
  {
    file: '11-canned-response-picker.png', phase: 'main', role: 'jordan', path: ticketPath('T1'), vp: DESK,
    async prepare(page) {
      await cardOf(page, 'Add work log entry').evaluate((el) => el.scrollIntoView({ block: 'center' }))
      await page.getByRole('button', { name: 'Insert canned response' }).click()
      await waitVisible(page.getByPlaceholder('Filter by name…'))
    },
  },
  {
    // break-all app issue applies (see header). Crop ends in the gap between the
    // Attachments card and the next card, so no sliver of the next card shows.
    file: '12-quarantined-attachment.png', phase: 'main', role: 'jordan', path: ticketPath('T3'), vp: DESK,
    target: {
      cutBetween: (page) => [
        cardOf(page, 'Attachments'),
        cardOf(page, 'Attachments').locator('xpath=following-sibling::*[1]'),
      ],
    },
    async prepare(page) {
      await waitVisible(page.getByRole('alert').filter({ hasText: 'identified as malicious' }))
      await waitCirclKnown(page)
    },
  },
  {
    file: '13-attachment-reputation.png', phase: 'main', role: 'jordan', path: ticketPath('T3'), vp: DESK,
    requires: 'reputation',
    target: { element: (page) => quarantinedRow(page) },
    async prepare(page) {
      await page.getByRole('button', { name: /^Show all 2 services/ }).click({ timeout: DATA_WAIT })
      await waitCirclKnown(page)
    },
  },
  {
    // Full width (sidebar and tab bar included), cap 2600 CSS px: Uploads,
    // Malware scanning and the top of Reputation lookup (admin-guide.html:791).
    file: '14-admin-settings-attachments.png', phase: 'main', role: 'admin', path: '/admin/settings', vp: DESK,
    target: { tall: 2600, exact: true },
    async prepare(page) {
      await page.getByRole('button', { name: 'Attachments', exact: true }).click()
      await waitVisible(page.getByRole('heading', { name: 'Reputation lookup', exact: true }))
    },
  },

  // ── New for 1.3.0 ──
  {
    file: '15-setup.png', phase: 'pre-setup', path: '/setup', vp: DESK, optional: false,
    async prepare(page) {
      await page.locator('#display-name').fill('Alex Rivera')
      await page.locator('#email').fill('alex.rivera@example.com')
      await page.locator('#category').fill('Hardware')
    },
  },
  {
    file: '15-setup-mobile.png', phase: 'pre-setup', path: '/setup', vp: MOBILE, mobile: true, optional: true,
    async prepare(page) {
      await page.locator('#display-name').fill('Alex Rivera')
      await page.locator('#email').fill('alex.rivera@example.com')
      await page.locator('#category').fill('Hardware')
    },
  },
  {
    file: '16-admin-audit-log.png', phase: 'main', role: 'admin', path: '/admin/audit', vp: DESK,
    async prepare(page) {
      await waitVisible(page.getByText('Password reset by admin', { exact: true }))
    },
  },
  {
    file: '17-ticket-activity-feed.png', phase: 'main', role: 'jordan', path: ticketPath('T1'), vp: DESK,
    target: { element: (page) => cardOf(page, 'Activity') },
    async prepare(page) {
      await waitActivity(page)
    },
  },
  {
    file: '18-admin-webhooks.png', phase: 'main', role: 'admin', path: '/admin/webhooks', vp: DESK,
    requires: 'webhookDelivered',
    target: { tall: 1500 },
    async prepare(page) {
      await waitVisible(page.getByText(/Failing|Delivered/).first(), DATA_WAIT)
    },
  },
  {
    file: '19-admin-settings-privacy.png', phase: 'main', role: 'admin', path: '/admin/settings', vp: DESK,
    target: {
      union: (page) => [sectionOf(page, 'Ticket visibility'), sectionOf(page, 'Audit log'), sectionOf(page, 'Privacy')],
    },
    async prepare(page) {
      await waitVisible(page.getByRole('heading', { name: 'Privacy', exact: true }))
    },
  },
  {
    file: '20-signup.png', phase: 'main', path: '/signup', vp: DESK,
    async prepare(page) {
      await page.locator('#email').fill('riley.chen@example.com')
    },
  },
  {
    file: '21-verify-email.png', phase: 'main', path: (s) => `/verify-email?token=${s.signupToken}`, vp: DESK,
    async prepare(page) {
      await page.locator('#display_name').fill('Riley Chen')
      await page.locator('#password').fill(RILEY_PASSWORD)
      await page.locator('#confirm').fill(RILEY_PASSWORD)
    },
  },
  {
    file: '21-verify-email-mobile.png', phase: 'main', path: (s) => `/verify-email?token=${s.signupToken}`, vp: MOBILE,
    mobile: true, optional: true,
    async prepare(page) {
      await page.locator('#display_name').fill('Riley Chen')
      await page.locator('#password').fill(RILEY_PASSWORD)
      await page.locator('#confirm').fill(RILEY_PASSWORD)
    },
  },
  {
    file: '22-account-passkeys.png', phase: 'main', role: 'sam', path: '/account', vp: DESK,
    passkey: true,
    target: { tall: 4000 },
    async prepare(page, ctx) {
      // A CDP virtual authenticator answers the browser's WebAuthn prompt.
      const cdp = await ctx.newCDPSession(page)
      await cdp.send('WebAuthn.enable')
      await cdp.send('WebAuthn.addVirtualAuthenticator', {
        options: {
          protocol: 'ctap2', transport: 'internal', hasResidentKey: true,
          hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true,
        },
      })
      await page.getByLabel('Name this key').fill('Work laptop')
      await page.getByRole('button', { name: 'Add a passkey' }).click()
      await waitVisible(page.getByText('Work laptop', { exact: true }))
      await waitVisible(page.getByText('This device only').first())
    },
  },
  {
    file: '23-mobile-ticket-list.png', phase: 'main', role: 'jordan', path: '/tickets', vp: MOBILE, mobile: true,
    async prepare(page) {
      // The card list is md:hidden; the desktop table with the same text is in the DOM too.
      await waitVisible(page.locator('ul.md\\:hidden').getByText('GHD-2026-000001', { exact: true }))
    },
  },
  {
    // break-all app issue applies (see header).
    file: '24-mobile-ticket-detail.png', phase: 'main', role: 'jordan', path: ticketPath('T1'), vp: MOBILE, mobile: true,
    async prepare(page) {
      await waitActivity(page)
    },
  },
  {
    file: '25-mobile-nav-drawer.png', phase: 'main', role: 'jordan', path: '/dashboard', vp: MOBILE, mobile: true,
    async prepare(page) {
      await page.getByRole('button', { name: 'Open navigation menu' }).click()
      await waitVisible(page.getByRole('dialog'))
      await page.waitForTimeout(300)
    },
  },
  {
    file: '26-mobile-new-ticket.png', phase: 'main', role: 'morgan', path: '/tickets/new', vp: MOBILE, mobile: true,
    async prepare(page) {
      await page.locator('#category').selectOption({ label: 'Hardware' })
    },
  },
  {
    file: '27-closed-ticket-requester.png', phase: 'main', role: 'taylor', path: ticketPath('T9'), vp: DESK,
    async prepare(page) {
      await waitVisible(page.getByText('Create follow-up').first())
    },
  },
  {
    file: '28-guest-submit.png', phase: 'main', path: '/submit', vp: DESK, optional: true,
    async prepare(page) {
      await page.locator('#name').fill('Pat Lee')
      await page.locator('#email').fill('pat.lee@example.com')
      await page.locator('#subject').fill('Cannot join the visitor Wi-Fi')
    },
  },
]

// Why a shot whose seed data flag is false is skipped. Printed on the SKIP line.
const SKIP_REASON = {
  reputation: 'seed.features.reputation is false (SHOTS_REPUTATION=0), so no provider lookups',
  webhookDelivered: 'seed.features.webhookDelivered is false: not every enabled hook has a recorded delivery',
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function shotPrefix(file) {
  return file.slice(0, file.indexOf('-'))
}

// The id a shot is selected by with --only: its number, plus "m" for the
// mobile variants that carry a "-mobile" suffix (15m, 21m). Also the --list output.
function shotId(shot) {
  const stem = shot.file.replace(/\.png$/, '')
  return stem.endsWith('-mobile') ? `${shotPrefix(shot.file)}m` : shotPrefix(shot.file)
}

const SHOT_IDS = [...new Set(SHOTS.map(shotId))]
const KNOWN_ONLY = new Set([...SHOT_IDS, ...SHOTS.flatMap((x) => [x.file, x.file.replace(/\.png$/, '')])])

function selected(shot) {
  if (!only) return true
  return only.has(shotId(shot)) || only.has(shot.file) || only.has(shot.file.replace(/\.png$/, ''))
}

// Desktop 2880x1800 and mobile 780x1688 unless the shot is tall or an element crop.
// A tall shot with `exact` has a fixed height, so its size is known in advance.
function expectedSize(shot) {
  const t = shot.target
  if (t?.cutBetween || t?.union || t?.element) return null
  if (t?.exact) return [DESK.width * 2, t.tall * 2]
  if (t?.tall) return null
  if (shot.vp === MOBILE) return [MOBILE.width * 2, MOBILE.height * 2]
  return [DESK.width * 2, DESK.height * 2]
}

function pngSize(buf) {
  // IHDR width and height, big-endian, at byte 16.
  return { w: buf.readUInt32BE(16), h: buf.readUInt32BE(20) }
}

async function settle(page, { keepFocus = false } = {}) {
  // networkidle may never settle on a page that polls; the data waits below
  // are what decide whether a shot is valid, so this one is best-effort.
  await page.waitForLoadState('networkidle').catch(() => {})
  await page.locator('.animate-spin').first().waitFor({ state: 'detached', timeout: 8000 }).catch(() => {})
  await page.getByText('Loading…', { exact: true }).first().waitFor({ state: 'detached', timeout: 8000 }).catch(() => {})
  await page.evaluate(() => document.fonts.ready)
  await page.addStyleTag({
    content:
      '*,*::before,*::after{transition:none!important;animation:none!important;caret-color:transparent!important}' +
      '::-webkit-scrollbar{width:0;height:0}',
  })
  if (!keepFocus) await page.evaluate(() => document.activeElement?.blur?.())
  await page.mouse.move(0, 0)
}

async function login(ctx, email) {
  const r = await ctx.request.post('/api/v1/auth/local/login', { data: { email, password: PASSWORD } })
  if (!r.ok()) throw new Error(`login ${email}: HTTP ${r.status()}`)
  const body = await r.json()
  if (body.mfa_needed || body.passkey_needed || body.mfa_enrollment_needed) {
    throw new Error(`login ${email}: unexpected second step ${JSON.stringify(body)}`)
  }
}

async function newContext(browser, shot) {
  const opts = {
    baseURL: BASE,
    viewport: shot.vp,
    deviceScaleFactor: 2,
    colorScheme: 'light',
    locale: 'en-US',
    timezoneId: 'UTC',
    reducedMotion: 'reduce',
  }
  if (shot.mobile) Object.assign(opts, { isMobile: true, hasTouch: true })
  return browser.newContext(opts)
}

// Grow the viewport to the content height of <main>, which is the scroller
// (the document never scrolls; see design section 4.4).
async function fitTall(page, cap, exact) {
  const h = await page.evaluate(() => {
    const m = document.querySelector('main')
    return 900 + (m.scrollHeight - m.clientHeight)
  })
  const height = exact ? cap : Math.min(h, cap)
  await page.setViewportSize({ width: DESK.width, height })
  await settle(page)
}

// Scroll <main> so the locator's top edge sits PAD below main's top edge. This is
// what keeps element clips from starting flush (and clamping to 0), which made
// heights depend on scroll timing. It is a no-op when the element already sits
// there or cannot move further up.
async function alignTop(locator, pad) {
  await locator.evaluate((el, p) => {
    const main = document.querySelector('main')
    const m = main.getBoundingClientRect()
    const e = el.getBoundingClientRect()
    main.scrollTop += e.top - (m.top + p)
  }, pad)
}

// Clip to the union of locators, plus PAD, clamped to the viewport. Loops until the
// layout is stable: the viewport grows when the element is taller than the window,
// and growing it moves <main>'s scroll, so the boxes are re-measured each pass.
async function elementClip(page, locators) {
  for (let pass = 0; pass < 6; pass++) {
    await alignTop(locators[0], PAD)
    await settle(page, { keepFocus: true })
    const boxes = []
    for (const l of locators) {
      const b = await l.boundingBox()
      if (!b) throw new Error('element not visible for clip')
      boxes.push(b)
    }
    const left = Math.min(...boxes.map((b) => b.x))
    const top = Math.min(...boxes.map((b) => b.y))
    const right = Math.max(...boxes.map((b) => b.x + b.width))
    const bottom = Math.max(...boxes.map((b) => b.y + b.height))
    const vp = page.viewportSize()
    const needH = Math.ceil(bottom + PAD)
    if (needH > vp.height) {
      await page.setViewportSize({ width: vp.width, height: needH })
      continue
    }
    const x = Math.max(0, Math.floor(left - PAD))
    const y = Math.max(0, Math.floor(top - PAD))
    const x2 = Math.min(vp.width, Math.ceil(right + PAD))
    const y2 = Math.min(vp.height, Math.ceil(bottom + PAD))
    return { x, y, width: x2 - x, height: y2 - y }
  }
  throw new Error('elementClip: layout did not settle in 6 passes')
}

async function captureShot(browser, shot, s) {
  const ctx = await newContext(browser, shot)
  try {
    if (shot.role) await login(ctx, s.users[shot.role])
    const page = await ctx.newPage()
    const p = typeof shot.path === 'function' ? shot.path(s) : shot.path
    await page.goto(p, { waitUntil: 'domcontentloaded' })
    await settle(page)
    await shot.prepare(page, ctx, s)
    await settle(page, { keepFocus: false })

    const t = shot.target ?? 'viewport'
    const screenshotOpts = { animations: 'disabled', caret: 'hide', scale: 'device' }
    if (t === 'viewport') {
      // as is
    } else if (t.tall) {
      await fitTall(page, t.tall, t.exact === true)
      await settle(page)
    } else if (t.element) {
      await fitTall(page, 4000, false)
      screenshotOpts.clip = await elementClip(page, [t.element(page)])
    } else if (t.union) {
      // Grow the window to the whole page first, so no part of the union sits
      // behind <main>'s own scroll.
      await fitTall(page, 4000, false)
      screenshotOpts.clip = await elementClip(page, t.union(page))
    } else if (t.cutBetween) {
      // Crop height at the midpoint of the gap between two cards. Card positions
      // do not depend on the viewport height, so measuring at 900 px is valid.
      const [above, below] = t.cutBetween(page)
      const a = await above.boundingBox()
      const b = await below.boundingBox()
      if (!a || !b) throw new Error('cutBetween: card not visible')
      const h = Math.floor((a.y + a.height + b.y) / 2)
      await page.setViewportSize({ width: DESK.width, height: h })
      await settle(page)
    }
    screenshotOpts.path = path.join(PNG_DIR, shot.file)
    const buf = await page.screenshot(screenshotOpts)
    return buf
  } finally {
    await ctx.close()
  }
}

// ── --list: every id --only accepts. No browser, no seed, no env needed. ──────

if (args.includes('--list')) {
  for (const id of SHOT_IDS) console.log(id)
  process.exit(0)
}

// ── CLI ───────────────────────────────────────────────────────────────────────

function argValue(name) {
  const i = args.indexOf(name)
  return i >= 0 ? args[i + 1] : undefined
}
const phase = argValue('--phase')
if (phase !== 'pre-setup' && phase !== 'main') {
  console.error('usage: capture.mjs --phase pre-setup|main [--only 05,12]')
  process.exit(2)
}
const onlyArg = argValue('--only')
const only = onlyArg ? new Set(onlyArg.split(',').map((s) => s.trim()).filter(Boolean)) : null
if (only) {
  const unknown = [...only].filter((id) => !KNOWN_ONLY.has(id))
  if (unknown.length) {
    console.error(`capture: unknown --only id(s): ${unknown.join(', ')} (run --list for the valid ids)`)
    process.exit(2)
  }
}

let seed = null
if (phase === 'main') {
  if (!fs.existsSync(SEED_FILE)) {
    console.error(`capture: ${SEED_FILE} not found; run seed.mjs first`)
    process.exit(2)
  }
  seed = JSON.parse(fs.readFileSync(SEED_FILE, 'utf8'))
}
const BASE = process.env.SHOTS_BASE_URL || seed?.baseURL || 'http://localhost:18080'
const PASSWORD = seed?.password || process.env.SHOTS_PASSWORD || PASSWORD_FALLBACK

// ── Runner ────────────────────────────────────────────────────────────────────

fs.mkdirSync(PNG_DIR, { recursive: true })

const chromiumPath =
  process.env.SHOTS_CHROMIUM || (fs.existsSync(DEFAULT_CHROMIUM) ? DEFAULT_CHROMIUM : undefined)

const { chromium } = await import('@playwright/test')
const browser = await chromium.launch({
  executablePath: chromiumPath,
  env: { ...process.env },
})

// Passkey shot last: its registration revokes Sam's sessions (design 3.6, section 7.3).
const queue = SHOTS.filter((x) => x.phase === phase && selected(x)).sort((a, b) => Number(!!a.passkey) - Number(!!b.passkey))

let failed = 0
const rows = []
const written = []
try {
  for (const shot of queue) {
    if (shot.requires && !seed?.features?.[shot.requires]) {
      rows.push({ kind: 'SKIP', file: shot.file, detail: SKIP_REASON[shot.requires] ?? `requires ${shot.requires}` })
      continue
    }
    try {
      const buf = await captureShot(browser, shot, seed)
      const { w, h } = pngSize(buf)
      const expect = expectedSize(shot)
      if (expect && (expect[0] !== w || expect[1] !== h)) {
        failed++
        rows.push({ kind: 'FAIL', file: shot.file, detail: `size ${w}x${h}, expected ${expect.join('x')}` })
        continue
      }
      fs.writeFileSync(path.join(PNG_DIR, shot.file), buf)
      written.push(shot.file)
      rows.push({ kind: 'OK', file: shot.file, size: `${w}x${h}`, kb: Math.round(buf.length / 1024) })
    } catch (err) {
      const reason = String(err.message).split('\n')[0]
      if (shot.optional) {
        rows.push({ kind: 'SKIP', file: shot.file, detail: `optional, failed: ${reason}` })
      } else {
        failed++
        rows.push({ kind: 'FAIL', file: shot.file, detail: reason })
      }
    }
  }
} finally {
  await browser.close()
}

// One line per shot, pixel size included.
for (const r of rows) {
  if (r.kind === 'OK') {
    console.log(`OK    ${r.file.padEnd(34)} ${r.size.padEnd(10)} ${r.kb} KB`)
  } else {
    console.log(`${r.kind.padEnd(5)} ${r.file.padEnd(34)} -          ${r.detail}`)
  }
}

// manifest.txt: pre-setup starts it fresh; main adds its files to it (a full run
// is pre-setup then main, so the file ends up listing every PNG of that run).
const manifestPath = path.join(OUT, 'manifest.txt')
const previous =
  phase === 'main' && fs.existsSync(manifestPath)
    ? fs.readFileSync(manifestPath, 'utf8').split('\n').filter(Boolean)
    : []
const listed = [...new Set([...previous, ...written])]
fs.writeFileSync(manifestPath, listed.map((f) => `${f}\n`).join(''))
console.log(`manifest: ${listed.length} file(s) listed in ${manifestPath} (${written.length} written by this phase)`)

if (failed > 0) {
  console.error(`capture: ${failed} shot(s) failed`)
  process.exit(1)
}
