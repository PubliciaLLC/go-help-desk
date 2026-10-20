import { test, expect } from '@playwright/test'
import { mockApi, REPLIES, TICKET_DETAIL } from './mock'

// Replies are capped at 50,000 characters from 1.3.0, but older ones may be any
// size. A post over the cap is shown collapsed (its start and a note), and
// "Show full text" draws all of it in one piece, so that selecting and copying
// gives back exactly what is stored. Runs in Chromium and WebKit: both engines'
// copy behaviour matters (WebKit dropped parts of content-visibility blocks).

// About 1 MB: 83,000 short lines.
const STORED = Array.from({ length: 83_000 }, (_, i) => `line ${i}`).join('\n')

for (const format of ['plain', 'markdown'] as const) {
  test(`an old ${format} reply of ${(STORED.length / 1e6).toFixed(1)} MB is collapsed, and expands to exactly the stored text`, async ({ page }) => {
    test.setTimeout(180_000)
    await mockApi(page, { replies: [{ ...REPLIES[0], body: STORED, body_format: format }] })
    await page.goto(`/tickets/${TICKET_DETAIL.id}`)

    const show = page.getByRole('button', { name: /^Show full text \(\d/ })
    await expect(show).toBeVisible()
    await expect(page.getByRole('note').first()).toContainText(/first 5,000 of [\d,]+ characters/)
    await expect(page.getByRole('note').first()).toContainText(/copying here copies only that part/)
    // The collapsed post holds the start only.
    const collapsedLength = await page.evaluate(() => document.body.innerText.length)
    expect(collapsedLength).toBeLessThan(40_000)
    await expect(page.getByText('line 82999')).toHaveCount(0)

    await show.click()
    const hide = page.getByRole('button', { name: 'Show only the start' })
    await expect(hide).toBeVisible({ timeout: 120_000 })

    const copied = await page.evaluate(() => {
      const p = Array.from(document.querySelectorAll('p')).find((e) => e.textContent!.startsWith('line 0\nline 1'))!
      const range = document.createRange()
      range.selectNodeContents(p)
      const sel = window.getSelection()!
      sel.removeAllRanges()
      sel.addRange(range)
      return sel.toString()
    })
    expect(copied).toBe(STORED)
  })
}

test('the reply form refuses over 50,000 characters and says so', async ({ page }) => {
  await mockApi(page)
  await page.goto(`/tickets/${TICKET_DETAIL.id}`)
  await page.getByRole('button', { name: /switch to plain editor/i }).click()
  const box = page.getByRole('textbox', { name: 'Work log entry' })
  await box.fill('a'.repeat(50_000))
  await expect(page.getByRole('button', { name: 'Save entry' })).toBeEnabled()
  await expect(page.getByText('50,000 / 50,000')).toBeVisible()
  await box.fill('a'.repeat(50_001))
  await expect(page.getByRole('button', { name: 'Save entry' })).toBeDisabled()
  await expect(page.getByRole('alert').filter({ hasText: '1% over the size limit' })).toBeVisible()
})
