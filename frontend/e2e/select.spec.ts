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
  const box = (await select.boundingBox())!
  // Pixels in a 6px band around the select. A ring drawn outside the border
  // changes them; a box-shadow WebKit drops, or one clipped by `contain: paint`,
  // leaves them identical.
  const clip = { x: box.x - 6, y: box.y - 6, width: box.width + 12, height: box.height + 12 }
  await page.mouse.move(0, 0)
  const before = await page.screenshot({ clip })
  // A real Tab, so the browser's :focus-visible heuristic applies.
  await select.evaluate((el) => (el as HTMLElement).blur())
  await page.keyboard.press('Shift+Tab')
  while (!(await select.evaluate((el) => el === document.activeElement))) await page.keyboard.press('Tab')
  const after = await page.screenshot({ clip })
  expect(after.equals(before), 'focusing the select changed no pixels around it').toBe(false)
  // WebKit ignores box-shadow on a native select, so the ring has to be an outline.
  const outline = await select.evaluate((el) => {
    const cs = getComputedStyle(el)
    return { style: cs.outlineStyle, width: parseFloat(cs.outlineWidth) }
  })
  expect(outline.style, 'a focused select has no outline').not.toBe('none')
  expect(outline.width).toBeGreaterThan(0)
})
