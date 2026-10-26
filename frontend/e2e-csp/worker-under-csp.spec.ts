import { test, expect } from '@playwright/test'
import { mockApi, REPLIES, TICKET_DETAIL } from '../e2e/mock'

// #443: Markdown is formatted in a Web Worker. script-src 'self' (no worker-src,
// so it falls back to it) must let the built worker load, and nothing may be
// reported as blocked.

test('the worker loads under the real CSP and formats a post', async ({ page }) => {
  const problems: string[] = []
  page.on('console', (m) => {
    if (/Content Security Policy|Refused to/i.test(m.text())) problems.push(m.text())
  })
  page.on('pageerror', (e) => problems.push(String(e)))
  const workers: string[] = []
  page.on('worker', (w) => workers.push(w.url()))

  await mockApi(page, { replies: [{ ...REPLIES[0], body: 'a **bold** word', body_format: 'markdown' }] })
  await page.goto(`/tickets/${TICKET_DETAIL.id}`)
  await expect(page.locator('[data-md-body] strong').first()).toHaveText('bold', { timeout: 5000 })
  expect(workers.some((u) => u.startsWith('http://127.0.0.1:5199/assets/'))).toBe(true)
  expect(problems).toEqual([])
})

test('hostile input still degrades to plain text under the CSP', async ({ page }) => {
  const table = ('|' + 'a|'.repeat(300) + '\n|' + '-|'.repeat(300) + '\n' + '|x\n'.repeat(20_000)).slice(0, 50_000)
  await mockApi(page, { replies: [{ ...REPLIES[0], body: table, body_format: 'markdown' }] })
  await page.goto(`/tickets/${TICKET_DETAIL.id}`)
  await expect(page.getByText(/Shown as plain text: too (long|complex) to format/).first()).toBeVisible({ timeout: 8000 })
  await expect(page.getByRole('button', { name: 'Plain text' }).first()).toBeVisible()
})
