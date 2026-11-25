import { test, expect, type Page } from '@playwright/test'
import { mockApi, ME_STAFF } from './mock'

// #709: dark mode. The API is mocked, as in the other specs. What is checked here
// is what jsdom cannot: the class on <html> before first paint, the real
// storage event between tabs, real focus, and the order of the page's requests.

const dark = (page: Page) => page.evaluate(() => document.documentElement.classList.contains('dark'))
const stored = (page: Page) => page.evaluate(() => window.localStorage.getItem('ghd.theme'))
const unauthenticated = { status: 401, json: { error: { code: 'unauthorized', message: 'no session' } } }

/** A signed-in page whose account says `theme`; records every PATCH /me/appearance. */
async function signedIn(page: Page, theme: 'light' | 'dark' | 'device') {
  const patches: unknown[] = []
  await mockApi(page, { me: { ...ME_STAFF, theme } })
  await page.route('**/api/v1/me/appearance', async (route) => {
    const body = route.request().postDataJSON() as { theme: string }
    patches.push(body)
    await route.fulfill({ json: { ...ME_STAFF, theme: body.theme } })
  })
  return patches
}

async function signedOut(page: Page) {
  await mockApi(page)
  await page.route('**/api/v1/me', (route) => route.fulfill(unauthenticated))
}

test.describe('the switch, signed in', () => {
  test.use({ viewport: { width: 1280, height: 900 } })

  test('choosing Dark applies at once, is saved to the account, and survives a reload', async ({ page }) => {
    const patches = await signedIn(page, 'light')
    await page.goto('/dashboard')
    const group = page.getByRole('group', { name: 'Appearance' })
    await expect(group).toBeVisible()
    expect(await dark(page)).toBe(false)

    await group.getByTitle('Dark', { exact: true }).click()
    expect(await dark(page)).toBe(true)
    await expect.poll(() => patches).toEqual([{ theme: 'dark' }])
    expect(await stored(page)).toBe('dark')

    // The account now says dark: a reload (with the account answering dark) is dark.
    await signedIn(page, 'dark')
    await page.reload()
    await expect(page.getByRole('group', { name: 'Appearance' })).toBeVisible()
    expect(await dark(page)).toBe(true)
  })

  test('Your account has the labelled choices and saves on change', async ({ page }) => {
    const patches = await signedIn(page, 'device')
    await page.goto('/account')
    await expect(page.getByRole('heading', { name: 'Appearance' })).toBeVisible()
    await page.getByRole('main').getByText('Dark', { exact: true }).click()
    await expect(page.getByText('Saved.')).toBeVisible()
    expect(patches).toEqual([{ theme: 'dark' }])
    expect(await dark(page)).toBe(true)
  })

  test('the keyboard: the arrows select, and the focused option has a visible ring', async ({ page, browserName }) => {
    await signedIn(page, 'device')
    await page.goto('/dashboard')
    const device = page.getByRole('radio', { name: 'Match my device' })
    await device.focus()
    await page.keyboard.press('ArrowLeft')
    await expect(page.getByRole('radio', { name: 'Dark' })).toBeChecked()
    expect(await dark(page)).toBe(true)
    // WebKit does not call a script-focused radio :focus-visible after an arrow
    // key the way Chromium does; the ring is asserted where the browser says it applies.
    test.skip(browserName === 'webkit', 'focus-visible heuristics differ in WebKit')
    expect(await page.evaluate(() => document.activeElement?.matches(':focus-visible'))).toBe(true)
    const ring = await page.getByRole('radio', { name: 'Dark' }).evaluate((el) => getComputedStyle(el.closest('label')!).boxShadow)
    expect(ring, 'the focused option has no ring').not.toBe('none')
  })

  test('the switch costs the sidebar no height: its row is as tall as the Sign out button, and both fit the width', async ({ page }) => {
    await signedIn(page, 'device')
    await page.goto('/dashboard')
    const signOut = page.getByRole('button', { name: 'Sign out' })
    await expect(page.getByRole('group', { name: 'Appearance' })).toBeVisible()
    const row = (await signOut.locator('..').boundingBox())!
    const button = (await signOut.boundingBox())!
    expect(row.height).toBeCloseTo(button.height, 0)
    const sw = (await page.getByRole('group', { name: 'Appearance' }).boundingBox())!
    const aside = (await page.locator('aside').first().boundingBox())!
    expect(sw.x + sw.width, 'the switch runs past the sidebar').toBeLessThanOrEqual(aside.x + aside.width)
    expect(button.x + button.width, 'Sign out overlaps the switch').toBeLessThanOrEqual(sw.x + 0.5)
  })

  test('there is no animated switch: transitions are off for one frame', async ({ page }) => {
    await signedIn(page, 'light')
    await page.goto('/dashboard')
    const seen = await page.evaluate(
      () =>
        new Promise<{ during: boolean; after: boolean }>((resolve) => {
          const html = document.documentElement
          document.querySelector<HTMLLabelElement>('label[title="Dark"]')!.click()
          const during = html.hasAttribute('data-theme-switching')
          requestAnimationFrame(() => requestAnimationFrame(() => resolve({ during, after: html.hasAttribute('data-theme-switching') })))
        }),
    )
    expect(seen).toEqual({ during: true, after: false })
  })
})

test.describe('the pages before sign-in', () => {
  test('follow the device when nothing is stored, and Light on the login page is remembered and sends nothing', async ({ browser }) => {
    const context = await browser.newContext({ colorScheme: 'dark' })
    const page = await context.newPage()
    const requests: string[] = []
    page.on('request', (r) => requests.push(`${r.method()} ${new URL(r.url()).pathname}`))
    await signedOut(page)
    await page.goto('/login')
    await expect(page.getByRole('group', { name: 'Appearance' })).toBeVisible()
    expect(await dark(page)).toBe(true)

    await page.getByRole('group', { name: 'Appearance' }).getByTitle('Light', { exact: true }).click()
    expect(await dark(page)).toBe(false)
    expect(await stored(page)).toBe('light')
    expect(requests.filter((r) => r.includes('appearance'))).toEqual([])

    await page.reload()
    await expect(page.getByRole('group', { name: 'Appearance' })).toBeVisible()
    expect(await dark(page)).toBe(false)
    await context.close()
  })

  test("signing in: the account's value wins over what this browser had", async ({ browser }) => {
    const context = await browser.newContext({ colorScheme: 'light' })
    await context.addInitScript(() => window.localStorage.setItem('ghd.theme', 'dark'))
    const page = await context.newPage()
    await mockApi(page)
    let signedInNow = false
    await page.route('**/api/v1/me', (route) =>
      signedInNow ? route.fulfill({ json: { ...ME_STAFF, theme: 'device' } }) : route.fulfill(unauthenticated),
    )
    await page.route('**/api/v1/auth/local/login', async (route) => {
      signedInNow = true
      await route.fulfill({
        json: { user: { ...ME_STAFF, theme: 'device' }, mfa_needed: false, passkey_needed: false, mfa_enrollment_needed: false },
      })
    })
    await page.goto('/login')
    expect(await dark(page)).toBe(true) // the browser's last choice

    await page.getByLabel('Email').fill('sam.staff@example.com')
    await page.locator('input[type="password"]').fill('correct horse battery staple')
    await page.getByRole('button', { name: 'Sign in', exact: true }).click()
    await page.waitForURL('**/dashboard')

    expect(await dark(page)).toBe(false) // device, on a light system
    expect(await stored(page)).toBe('device')
    await context.close()
  })

  test('signing out keeps the choice, so the login page looks the way the session did', async ({ page }) => {
    await signedIn(page, 'dark')
    await page.setViewportSize({ width: 1280, height: 900 })
    await page.goto('/dashboard')
    expect(await dark(page)).toBe(true)

    await page.route('**/api/v1/me', (route) => route.fulfill(unauthenticated))
    await page.getByRole('button', { name: 'Sign out' }).click()
    await page.waitForURL('**/login')
    expect(await dark(page)).toBe(true)
    expect(await stored(page)).toBe('dark')
  })

  test('another tab follows a change without reloading', async ({ browser }) => {
    const context = await browser.newContext({ colorScheme: 'light' })
    const a = await context.newPage()
    const b = await context.newPage()
    for (const p of [a, b]) {
      await signedOut(p)
      await p.goto('/login')
      await expect(p.getByRole('group', { name: 'Appearance' })).toBeVisible()
    }
    expect(await dark(b)).toBe(false)

    await a.getByRole('group', { name: 'Appearance' }).getByTitle('Dark', { exact: true }).click()
    await expect.poll(() => dark(b)).toBe(true)
    await context.close()
  })

  test('storage that refuses is tolerated: the page follows the device and a choice still applies', async ({ browser }) => {
    const context = await browser.newContext({ colorScheme: 'dark' })
    await context.addInitScript(() => {
      const refuse = () => { throw new DOMException('denied', 'SecurityError') }
      Storage.prototype.getItem = refuse
      Storage.prototype.setItem = refuse
    })
    const page = await context.newPage()
    const errors: string[] = []
    page.on('pageerror', (e) => errors.push(e.message))
    await signedOut(page)
    await page.goto('/login')
    await expect(page.getByRole('group', { name: 'Appearance' })).toBeVisible()
    expect(await dark(page)).toBe(true)

    await page.getByRole('group', { name: 'Appearance' }).getByTitle('Light', { exact: true }).click()
    expect(await dark(page)).toBe(false)
    expect(errors).toEqual([])
    await context.close()
  })
})

test.describe('no flash of the wrong theme', () => {
  const darkAtDOMContentLoaded = () => {
    document.addEventListener('DOMContentLoaded', () => {
      ;(window as unknown as { __darkAtDCL: boolean }).__darkAtDCL = document.documentElement.classList.contains('dark')
    })
  }

  for (const [name, stored, colorScheme] of [
    ['a stored dark', 'dark', 'light'],
    ['device on a dark system', 'device', 'dark'],
  ] as const) {
    test(`${name}: <html> is dark by DOMContentLoaded, before the application runs`, async ({ browser }) => {
      const context = await browser.newContext({ colorScheme })
      await context.addInitScript((v) => window.localStorage.setItem('ghd.theme', v), stored)
      await context.addInitScript(darkAtDOMContentLoaded)
      const page = await context.newPage()
      const order: string[] = []
      page.on('request', (r) => order.push(new URL(r.url()).pathname))
      await signedOut(page)
      await page.goto('/login')
      expect(await page.evaluate(() => (window as unknown as { __darkAtDCL: boolean }).__darkAtDCL)).toBe(true)

      // The script is a blocking file requested before the application bundle.
      const init = order.indexOf('/theme-init.js')
      const bundle = order.findIndex((p) => /^\/assets\/index-.*\.js$/.test(p))
      expect(init, 'theme-init.js was never requested').toBeGreaterThanOrEqual(0)
      if (bundle >= 0) expect(init).toBeLessThan(bundle)
      await context.close()
    })
  }

  test('the metas follow the theme', async ({ browser }) => {
    const context = await browser.newContext({ colorScheme: 'dark' })
    const page = await context.newPage()
    await signedOut(page)
    await page.goto('/login')
    await expect(page.locator('meta[name="color-scheme"]')).toHaveAttribute('content', 'dark')
    await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute('content', '#101828')
    await context.close()
  })
})
