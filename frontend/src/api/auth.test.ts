import { describe, expect, it, vi, beforeEach } from 'vitest'
import { api } from './client'
import { changePassword, signup, login, verifyEmail, lookupVerification } from './auth'

// auth.ts is mostly thin wrappers, and those are deliberately left alone —
// asserting that api.post was called proves nothing. What is covered here is
// the field mapping, which is where this file can actually be wrong.
//
// The mappings matter more than they look. Every function takes same-typed
// arguments (string, string) and renames them into a snake_case payload, so a
// swap or a typo is invisible to TypeScript and to the caller: the request is
// well-formed, the server rejects it or acts on the wrong field, and the bug
// surfaces as "password change doesn't work" with nothing in the stack to
// point at.

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('changePassword', () => {
  // Swapping these two sends the new password as the current one. The server
  // answers "current password is incorrect" and the real cause is invisible
  // from the UI.
  it('maps the two passwords to the fields the server expects', async () => {
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined })
    await changePassword('old-secret', 'new-secret')

    expect(patch).toHaveBeenCalledWith('/me/password', {
      current_password: 'old-secret',
      new_password: 'new-secret',
    })
  })

  it('does not leak the arguments under their camelCase names', async () => {
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined })
    await changePassword('a', 'b')

    const [, body] = patch.mock.calls[0]
    expect(Object.keys(body as object).sort()).toEqual(['current_password', 'new_password'])
  })
})

describe('signup', () => {
  // The address only (#360, #374): the name and password are chosen at
  // verification, by whoever reads the inbox.
  it('sends the address and nothing else', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: undefined })
    await signup('someone@example.com')

    expect(post).toHaveBeenCalledWith('/auth/signup', { email: 'someone@example.com' })
  })
})

describe('verifyEmail', () => {
  // displayName -> display_name is the only rename here. The server reads a
  // missing name as blank and refuses it, so a typo would block every
  // verification rather than pass silently, but it is asserted all the same.
  it('renames displayName to display_name', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {} })
    await verifyEmail('tok-1', 'Someone Real', 'a-long-passphrase')

    expect(post).toHaveBeenCalledWith('/auth/verify-email', {
      token: 'tok-1',
      display_name: 'Someone Real',
      password: 'a-long-passphrase',
    })
  })
})

describe('responses that decide what the UI does next', () => {
  // login and verifyEmail both return the flags that route the user to the MFA
  // challenge or to enrolment. Dropping or renaming one silently skips MFA, so
  // the shape is asserted rather than assumed.
  it('login returns the MFA flags unchanged', async () => {
    vi.spyOn(api, 'post').mockResolvedValue({
      data: { user: { id: 'u1' }, mfa_needed: true, mfa_enrollment_needed: false },
    })

    await expect(login('e@example.com', 'pw')).resolves.toMatchObject({
      mfa_needed: true,
      mfa_enrollment_needed: false,
    })
  })

  it('verifyEmail returns the same envelope as login', async () => {
    vi.spyOn(api, 'post').mockResolvedValue({
      data: { user: { id: 'u1' }, mfa_needed: false, mfa_enrollment_needed: true },
    })

    const res = await verifyEmail('token-123', 'Someone', 'a-long-passphrase')
    expect(res.mfa_enrollment_needed).toBe(true)
  })
})

// The token goes as a query parameter, the one place the GET route reads it.
describe('lookupVerification', () => {
  it('sends the token as ?token= and returns the address', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({ data: { email: 'a@example.com' } })
    await expect(lookupVerification('tok-1')).resolves.toEqual({ email: 'a@example.com' })
    expect(get).toHaveBeenCalledWith('/auth/verify-email', { params: { token: 'tok-1' } })
  })
})
