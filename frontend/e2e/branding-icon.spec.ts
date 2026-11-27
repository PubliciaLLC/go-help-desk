import { test, expect, type Page } from '@playwright/test'
import { mockAdminApi, mockSiteIcon } from './mock'

// System -> Branding, the Icon section and the home screen name (#708), in a
// real browser. The API is mocked (e2e/mock.ts), so this runs without the Go
// server, like the rest of this directory:
//
//   cd frontend && npx playwright test e2e/branding-icon.spec.ts
//
// What it cannot show is the server side: the files really being generated and
// the page head really carrying them. That is the backend's tests, and a manual
// look once both halves are merged.

const PREVIEW_COUNT = 7

// <main> is the scrolling box of every page (see mobile-layout.spec.ts for why
// the document alone proves nothing).
async function noSidewaysScroll(page: Page, width: number) {
  const m = await page.evaluate(() => {
    const main = document.querySelector('main')
    if (!main) throw new Error('no <main> element found')
    return {
      main: main.scrollWidth,
      mainBox: main.clientWidth,
      doc: document.documentElement.scrollWidth,
    }
  })
  expect(m.main, `main is ${m.main}px wide in a ${m.mainBox}px box at ${width}px`).toBeLessThanOrEqual(m.mainBox)
  expect(m.doc, `the document is ${m.doc}px wide at ${width}px`).toBeLessThanOrEqual(width)
}

async function open(page: Page, opts: { set?: boolean } = {}) {
  await mockAdminApi(page)
  const icon = await mockSiteIcon(page, opts)
  await page.goto('/admin/settings/branding')
  await expect(page.getByRole('heading', { level: 1, name: 'Branding' })).toBeVisible()
  await expect(page.getByRole('heading', { level: 2, name: 'Icon' })).toBeVisible()
  return icon
}

test.describe('at phone width', () => {
  for (const width of [320, 375]) {
    test(`all seven previews are shown and the page does not scroll sideways: ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 })
      await open(page, { set: true })

      const group = page.getByRole('group', { name: 'Generated icons' })
      const figures = group.locator('figure')
      await expect(figures).toHaveCount(PREVIEW_COUNT)
      for (let i = 0; i < PREVIEW_COUNT; i++) await expect(figures.nth(i)).toBeVisible()
      // The images arrived (the mock serves a real PNG), not just their boxes.
      await expect
        .poll(() => group.locator('img').evaluateAll((imgs) => imgs.every((i) => (i as HTMLImageElement).naturalWidth > 0)))
        .toBe(true)

      await expect(page.getByRole('button', { name: 'Replace icon' })).toBeVisible()
      await expect(page.getByRole('button', { name: 'Apply colour' })).toBeVisible()
      await noSidewaysScroll(page, width)
    })
  }

  test('with no icon, and with the home screen name ticked: 320px', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 844 })
    await open(page)
    await page.getByRole('checkbox', { name: 'Use a different name on the home screen' }).check()
    await page.getByLabel('Home screen name', { exact: true }).fill('A name of more than twelve characters')
    await expect(page.getByText('Most home screens show about 12 characters')).toBeVisible()
    await noSidewaysScroll(page, 320)
  })
})

test('the home screen name is reached and ticked from the keyboard', async ({ page }) => {
  await open(page)
  // Site name is the one text box with this placeholder until the home screen
  // name is ticked.
  await page.getByPlaceholder('Go Help Desk').focus()
  await page.keyboard.press('Tab')
  const checkbox = page.getByRole('checkbox', { name: 'Use a different name on the home screen' })
  await expect(checkbox).toBeFocused()
  await page.keyboard.press('Space')
  await expect(checkbox).toBeChecked()
  await page.keyboard.press('Tab')
  await expect(page.getByLabel('Home screen name', { exact: true })).toBeFocused()
})

test('uploading an icon shows the seven previews and says to reload', async ({ page }) => {
  const icon = await open(page)
  await expect(page.getByText('No icon uploaded. The built-in icon is used.')).toBeVisible()
  await expect(page.locator('figure')).toHaveCount(0)

  // The logo's picker comes first on the page; the icon's is the last.
  await page.locator('input[type="file"]').last().setInputFiles({
    name: 'icon.png',
    mimeType: 'image/png',
    buffer: Buffer.from('not inspected by the mock'),
  })

  await expect(page.getByRole('status')).toHaveText('Icon saved. Reload the page to see it in this tab.')
  await expect(page.getByRole('group', { name: 'Generated icons' }).locator('figure')).toHaveCount(PREVIEW_COUNT)
  await expect(page.getByRole('button', { name: 'Replace icon' })).toBeVisible()

  const post = icon.requests.find((r) => r.method === 'POST')
  expect(post?.body, 'the upload is multipart with the file and the colour').toContain('name="icon"')
  expect(post?.body).toContain('name="background"')
})

test('applying a colour and removing the icon', async ({ page }) => {
  const icon = await open(page, { set: true })

  await page.getByLabel('Home screen background colour, hex').fill('#112233')
  await page.getByRole('button', { name: 'Apply colour' }).click()
  await expect(page.getByRole('status')).toHaveText('Background colour applied.')
  expect(icon.requests.find((r) => r.method === 'PATCH')?.body).toBe(JSON.stringify({ background: '#112233' }))

  await page.getByRole('button', { name: 'Remove icon' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByText('Remove the icon?')).toBeVisible()
  await dialog.getByRole('button', { name: 'Remove icon' }).click()
  await expect(page.getByRole('status')).toHaveText('Icon removed.')
  await expect(page.getByText('No icon uploaded. The built-in icon is used.')).toBeVisible()
  await expect(page.locator('figure')).toHaveCount(0)
})

test('the previews are in a tidy grid: captions line up and the colour row comes first', async ({ page }) => {
  for (const width of [320, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    await open(page, { set: true })
    const group = page.getByRole('group', { name: 'Generated icons' })
    await expect(group.locator('figure')).toHaveCount(PREVIEW_COUNT)

    // Within one row of previews the captions share a top edge, and the
    // thumbnails end at the same line, whatever size each is drawn at.
    const boxes = await group.locator('figure').evaluateAll((figs) =>
      figs.map((f) => ({
        row: Math.round(f.getBoundingClientRect().top),
        caption: Math.round(f.querySelector('figcaption')!.getBoundingClientRect().top),
        thumbBottom: Math.round(f.querySelector('img')!.getBoundingClientRect().bottom),
      })),
    )
    for (const row of new Set(boxes.map((b) => b.row))) {
      const same = boxes.filter((b) => b.row === row)
      expect(new Set(same.map((b) => b.caption)).size, `captions in the row at ${row}px, ${width}px wide`).toBe(1)
      expect(new Set(same.map((b) => b.thumbBottom)).size, `thumbnails in the row at ${row}px`).toBe(1)
    }

    // The colour decides how four of them look, so it is above them.
    const colourY = (await page.getByLabel('Home screen background colour, hex').boundingBox())!.y
    const groupY = (await group.boundingBox())!.y
    const replaceY = (await page.getByRole('button', { name: 'Replace icon' }).boundingBox())!.y
    expect(colourY).toBeLessThan(groupY)
    expect(replaceY).toBeGreaterThan(groupY)
  }
})

test('after removing the icon the keyboard is on Upload icon', async ({ page }) => {
  await open(page, { set: true })
  await page.getByRole('button', { name: 'Remove icon' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Remove icon' }).click()
  await expect(page.getByRole('status')).toHaveText('Icon removed.')
  await expect(page.getByRole('button', { name: 'Upload icon' })).toBeFocused()
})
