import { test, expect, type Page } from '@playwright/test'
import { mockApi, TICKET_DETAIL } from './mock'

// #561 / #562: the shared native <select>, in the compact size the ticket
// page's Classification panel uses.
test.use({ viewport: { width: 1280, height: 900 } })

async function openClassificationEditor(page: Page) {
  await mockApi(page)
  await page.goto(`/tickets/${TICKET_DETAIL.id}`)
  const card = page.locator('div.rounded-lg', { has: page.getByText('Classification', { exact: true }) }).last()
  await card.getByRole('button', { name: 'Edit', exact: true }).click()
  return card.getByLabel('Category')
}

test('#562: the compact select is tall enough for its own text', async ({ page }) => {
  const select = await openClassificationEditor(page)
  await expect(select).toBeVisible()
  const m = await select.evaluate((el) => {
    const cs = getComputedStyle(el)
    // The select's own line-height is `normal`, so measure what one line of
    // this font really needs with a probe span in the same font.
    const probe = document.createElement('span')
    probe.textContent = 'Hardware'
    probe.style.cssText = `position:absolute;visibility:hidden;white-space:nowrap;font:${cs.font};line-height:normal`
    document.body.appendChild(probe)
    const line = probe.getBoundingClientRect().height
    probe.remove()
    return { content: el.clientHeight - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom), line }
  })
  expect(m.content, JSON.stringify(m) + ' the select content box is shorter than one line of its text').toBeGreaterThanOrEqual(m.line)
})

test('#561: a select focused from the keyboard shows a ring outside its border', async ({ page }) => {
  const select = await openClassificationEditor(page)
  await expect(select).toBeVisible()

  // Focus it, then step away and back with real keys so the browser's
  // :focus-visible heuristic sees keyboard use (a click on Edit came first).
  await select.focus()
  await page.keyboard.press('Shift+Tab')
  await page.keyboard.press('Tab')
  expect(await select.evaluate((el) => el === document.activeElement), 'Tab did not land on the Category select').toBe(true)

  // WebKit ignores box-shadow on a native select, so the ring has to be an
  // outline, in the app's ring colour (not each browser's default).
  const m = await select.evaluate((el) => {
    const cs = getComputedStyle(el)
    const probe = document.createElement('span')
    probe.style.color = 'hsl(var(--ring))'
    document.body.appendChild(probe)
    const ring = getComputedStyle(probe).color
    probe.remove()
    return { style: cs.outlineStyle, width: parseFloat(cs.outlineWidth), color: cs.outlineColor, ring }
  })
  expect(m.style, 'a focused select has no outline').not.toBe('none')
  expect(m.width).toBeGreaterThan(0)
  expect(m.color, 'the outline is not the --ring colour').toBe(m.ring)

  // Pixels, as well as styles. Key presses can scroll, so the box is taken now
  // and the same four clips are used for the focused image and the blurred
  // one (blur() does not scroll). Only the 6px strips OUTSIDE the border box
  // are compared, so a change inside the select (WebKit's own native focus
  // styling) cannot pass for a ring, and neither can `contain: paint` hiding it.
  const b = (await select.boundingBox())!
  const g = 6
  const strips = [
    { x: b.x - g, y: b.y - g, width: b.width + 2 * g, height: g },
    { x: b.x - g, y: b.y + b.height, width: b.width + 2 * g, height: g },
    { x: b.x - g, y: b.y, width: g, height: b.height },
    { x: b.x + b.width, y: b.y, width: g, height: b.height },
  ]
  const focusedShots = await Promise.all(strips.map((clip) => page.screenshot({ clip })))
  await select.evaluate((el) => (el as HTMLElement).blur())
  const blurredShots = await Promise.all(strips.map((clip) => page.screenshot({ clip })))
  const changed = strips.filter((_, k) => !focusedShots[k].equals(blurredShots[k])).length
  expect(changed, 'focusing the select changed no pixels outside its border').toBeGreaterThan(0)
})
