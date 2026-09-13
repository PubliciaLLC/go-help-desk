import { describe, expect, it, vi, beforeEach } from 'vitest'
import { api } from './client'
import { updateUser } from './admin'

// Clearing MFA without rotating the password is a compromise chain rather than
// a recovery: whoever already holds the password burns the victim's TOTP
// budget, the victim reports a lockout, an administrator clears MFA in good
// faith, and the attacker — password still working — enrols first.
//
// The server refuses reset_mfa without new_password. This pins that the client
// sends them together, because the failure mode if it does not is a 400 on a
// button an administrator is pressing during an incident.

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('updateUser with reset_mfa', () => {
  it('sends the new password alongside the reset', async () => {
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} })

    await updateUser('u1', { reset_mfa: true, new_password: 'set-by-the-admin' })

    expect(patch).toHaveBeenCalledWith('/admin/users/u1', {
      reset_mfa: true,
      new_password: 'set-by-the-admin',
    })
  })

  // Unrelated edits must not drag a password rotation along with them.
  it('does not send a password for an ordinary edit', async () => {
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} })

    await updateUser('u1', { display_name: 'New Name' })

    const [, body] = patch.mock.calls[0]
    expect('new_password' in (body as object)).toBe(false)
    expect('reset_mfa' in (body as object)).toBe(false)
  })
})
