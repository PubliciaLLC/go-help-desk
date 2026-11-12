import { describe, expect, it } from 'vitest'
import { needsAfterUpgrade } from './afterUpgrade'

describe('needsAfterUpgrade', () => {
  it.each([
    ['an upgraded install: nothing recorded', {}, true],
    ['an upgraded install that already has other settings', { staff_email_assigned_override: 'off' }, true],
    ['the wizard was finished or skipped, or setup recorded the answer', { post_upgrade_130_seen: true }, false],
    ['a stored false is not an answer', { post_upgrade_130_seen: false }, true],
    ['a string is not an answer', { post_upgrade_130_seen: 'true' }, true],
  ])('%s', (_name, settings, want) => {
    expect(needsAfterUpgrade(settings)).toBe(want)
  })
})
