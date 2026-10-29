import { test, expect } from '@playwright/test'
import fs from 'fs'
import path from 'path'

// scripts/csp-server.mjs checked containment with f.startsWith(dist), which
// also matches a sibling directory whose name begins with "dist". A request for
// /..%2fdist-private/secret.txt reached it. This is a test server, but it binds
// a port on the developer's or CI machine, and it should serve dist/ and
// nothing beside it.
const sibling = path.resolve('dist-private')
const SECRET = 'sibling-secret-do-not-serve'

test.beforeAll(() => {
  fs.mkdirSync(sibling, { recursive: true })
  fs.writeFileSync(path.join(sibling, 'secret.txt'), SECRET)
})
test.afterAll(() => {
  fs.rmSync(sibling, { recursive: true, force: true })
})

for (const p of ['/..%2fdist-private/secret.txt', '/%2e%2e%2fdist-private/secret.txt']) {
  test(`${p} does not reach a sibling of dist`, async ({ request }) => {
    const res = await request.get(p)
    expect(await res.text()).not.toContain(SECRET)
  })
}

test('a real file inside dist is still served', async ({ request }) => {
  const res = await request.get('/index.html')
  expect(res.status()).toBe(200)
  expect(res.headers()['content-type']).toContain('text/html')
})
