import { readFileSync } from 'fs'
import { test, expect } from '@playwright/test'
import { mockApi, ME_ADMIN } from '../e2e/mock'

// #131: the Reports image is made from a data: URL (the chart's svg) drawn on a
// canvas, and saved from a data: URL. The production CSP allows img-src 'self'
// data: and nothing else, so this fails the build if a change makes the image
// need a blob: URL or any other source.

test('Download image works under the production CSP', async ({ page }) => {
  const problems: string[] = []
  page.on('console', (m) => {
    if (/Content Security Policy|Refused to/i.test(m.text())) problems.push(m.text())
  })
  page.on('pageerror', (e) => problems.push(String(e)))

  await mockApi(page, { me: ME_ADMIN })
  await page.goto('/reports')
  await expect(page.getByText(/compared with/)).toBeVisible()
  await expect(page.getByRole('region', { name: 'Volume' }).getByRole('img', { name: /Tickets created and resolved per day/ })).toBeVisible()

  for (const click of [
    () => page.getByRole('button', { name: 'Download image', exact: true }).click(),
    () => page.getByRole('button', { name: 'Download Volume as an image' }).click(),
  ]) {
    const [download] = await Promise.all([page.waitForEvent('download'), click()])
    const b = readFileSync((await download.path())!)
    expect([...b.subarray(0, 8)]).toEqual([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
  }
  // The failure message would be the only sign of a blocked image.
  await expect(page.getByText(/The image could not be created/)).toHaveCount(0)
  expect(problems).toEqual([])
})
