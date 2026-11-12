import { test, expect } from '@playwright/test'
import { mockApi, ME_STAFF } from './mock'

// #632. Adding a passkey on the Account page can ask for a second factor
// first. After it is proved the page must wait for a click before it asks the
// browser for the key (Safari refuses a WebAuthn prompt that no click started).
//
// This runs a real navigator.credentials.create against Chromium's virtual
// authenticator, so the byte encoding and the ceremony are the browser's own;
// only the server is a stand-in. It cannot show Safari's behaviour: the
// virtual authenticator is Chromium-only (CDP) and does not enforce the
// gesture rule, so what is pinned here is the flow, not the engine.
test.skip(({ browserName }) => browserName !== 'chromium', 'CDP virtual authenticator is Chromium-only')

const b64url = (s: string) => Buffer.from(s).toString('base64url')

test('add a passkey after step-up: no prompt until the button is clicked', async ({ page, context }) => {
  // A WebAuthn RP ID cannot be an IP address, and the preview server is
  // addressed as 127.0.0.1 by default, so go in by name.
  const origin = `http://localhost:${process.env.PW_PORT ?? 5183}`

  const cdp = await context.newCDPSession(page)
  await cdp.send('WebAuthn.enable')
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  })
  const credentials = async () => (await cdp.send('WebAuthn.getCredentials', { authenticatorId })).credentials

  await mockApi(page, { me: { ...ME_STAFF, mfa_enabled: true } })

  let startCalls = 0
  let verified = false
  const keys: object[] = []
  let finishName: string | null = null

  await page.route('**/api/v1/me/passkeys', (route) => route.fulfill({ json: keys }))
  await page.route('**/api/v1/auth/local/mfa/verify', (route) => {
    verified = true
    return route.fulfill({ status: 204 })
  })
  await page.route('**/api/v1/me/passkeys/register/start', (route) => {
    startCalls++
    if (!verified) {
      return route.fulfill({
        status: 403,
        json: { error: { code: 'mfa_required', message: 'verify your second factor first' } },
      })
    }
    return route.fulfill({
      json: {
        publicKey: {
          challenge: b64url('0123456789abcdef0123456789abcdef'),
          rp: { id: 'localhost', name: 'Go Help Desk' },
          user: { id: b64url('u-staff'), name: 'sam.staff@example.com', displayName: 'Sam Staff' },
          pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
          authenticatorSelection: { residentKey: 'preferred', userVerification: 'preferred' },
          timeout: 60000,
          attestation: 'none',
        },
      },
    })
  })
  await page.route('**/api/v1/me/passkeys/register/finish?*', (route) => {
    finishName = new URL(route.request().url()).searchParams.get('name')
    keys.push({
      id: 'pk-new',
      name: finishName,
      transports: ['internal'],
      backup_eligible: false,
      backup_state: false,
      created_at: '2026-11-12T10:00:00Z',
      last_used_at: null,
    })
    return route.fulfill({ status: 204 })
  })

  await page.goto(`${origin}/account`)
  await expect(page.getByText('No passkeys yet.')).toBeVisible()

  await page.getByLabel('Name this key').fill('Work laptop')
  await page.getByRole('button', { name: 'Add a passkey' }).click()

  const panel = page.getByRole('group', { name: 'Verify your second factor' })
  await panel.getByLabel('Code from your authenticator app').fill('123456')
  await panel.getByRole('button', { name: 'Verify' }).click()

  const button = page.getByRole('button', { name: 'Verified — add the passkey' })
  await expect(button).toBeVisible()
  // Nothing has asked the browser for a key, and the server has not been asked
  // to start a second ceremony.
  expect(startCalls).toBe(1)
  expect(await credentials()).toHaveLength(0)

  await button.click()

  await expect(page.getByText('Work laptop', { exact: true })).toBeVisible()
  await expect(button).toHaveCount(0)
  expect(startCalls).toBe(2)
  expect(finishName).toBe('Work laptop')
  expect(await credentials()).toHaveLength(1)
})
