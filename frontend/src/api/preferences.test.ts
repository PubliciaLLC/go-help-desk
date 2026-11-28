import { describe, expect, it, vi, beforeEach } from 'vitest'
import { api } from './client'
import { updateAppearance, updateEmailPreferences } from './preferences'

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('updateEmailPreferences', () => {
  // Only the switches given are sent: the server changes just those, so two
  // tabs cannot undo each other.
  it('sends exactly the switches it was given, under the server field names', async () => {
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: { id: 'u-1' } })
    await updateEmailPreferences({ email_notify_group: false })

    expect(patch).toHaveBeenCalledWith('/me/email-preferences', { email_notify_group: false })
  })

  it('resolves to the profile the server saved', async () => {
    const saved = { id: 'u-1', email_notify_assigned: false }
    vi.spyOn(api, 'patch').mockResolvedValue({ data: saved })
    expect(await updateEmailPreferences({ email_notify_assigned: false })).toEqual(saved)
  })
})

describe('updateAppearance', () => {
  it('sends the one value, as {theme}, to the appearance endpoint', async () => {
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: { id: 'u-1', theme: 'dark' } })
    await updateAppearance('dark')

    expect(patch).toHaveBeenCalledWith('/me/appearance', { theme: 'dark' })
  })

  it('resolves to the profile the server saved', async () => {
    const saved = { id: 'u-1', theme: 'device' }
    vi.spyOn(api, 'patch').mockResolvedValue({ data: saved })
    expect(await updateAppearance('device')).toEqual(saved)
  })
})
