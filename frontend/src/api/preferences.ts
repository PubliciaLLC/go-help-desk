import { api } from './client'
import type { EmailPreferencesPatch, User } from './types'

// Its own module rather than auth.ts, which is about signing in; this is a
// preference of someone already signed in.

/** Saves some of the caller's own staff-email switches; resolves to the saved profile. */
export async function updateEmailPreferences(patch: EmailPreferencesPatch): Promise<User> {
  const res = await api.patch<User>('/me/email-preferences', patch)
  return res.data
}
