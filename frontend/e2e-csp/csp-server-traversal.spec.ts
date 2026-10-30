import { test, expect } from '@playwright/test'
import fs from 'fs'
import path from 'path'

// scripts/csp-server.mjs checked containment with f.startsWith(dist), which
// also matches a sibling directory whose name begins with "dist". A request for
// /..%2fdist-private/secret.txt reached it. This is a test server, but it binds
// a port on the developer's or CI machine, and it should serve dist/ and
// nothing beside it.
// The sibling is created with mkdtemp so the test never reuses, overwrites or
// deletes a directory it did not make. The name must still start with "dist":
// that prefix is what the old startsWith(dist) check wrongly accepted.
const dist = path.resolve('dist')
const SECRET = 'sibling-secret-do-not-serve'
let sibling = ''

test.beforeAll(() => {
  sibling = fs.mkdtempSync(path.join(path.dirname(dist), 'dist-private-'))
  fs.writeFileSync(path.join(sibling, 'secret.txt'), SECRET)
})
test.afterAll(() => {
  if (sibling) fs.rmSync(sibling, { recursive: true, force: true })
})

for (const dots of ['..%2f', '%2e%2e%2f']) {
  test(`/${dots}<sibling>/secret.txt does not reach a sibling of dist`, async ({ request }) => {
    const res = await request.get(`/${dots}${path.basename(sibling)}/secret.txt`)
    expect(await res.text()).not.toContain(SECRET)
  })
}

test('a real file inside dist is still served', async ({ request }) => {
  const res = await request.get('/index.html')
  expect(res.status()).toBe(200)
  expect(res.headers()['content-type']).toContain('text/html')
})

test('a malformed percent escape is answered 400 and the server keeps serving', async ({ request }) => {
  const res = await request.get('/%zz')
  expect(res.status()).toBe(400)
  expect((await request.get('/index.html')).status()).toBe(200)
})
