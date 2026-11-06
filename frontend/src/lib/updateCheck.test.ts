import { describe, expect, it } from 'vitest'
import { UPDATE_CHECK_DETAIL, UPDATE_CHECK_LABEL } from './updateCheck'

// The wording is a promise about what leaves the server. Setup, the wizard and
// Settings all use these strings, so one test pins them.
describe('the update check wording', () => {
  it('names who is contacted and what is sent', () => {
    expect(UPDATE_CHECK_LABEL).toBe(
      'Check for new releases (contacts GitHub once a week; sends nothing about your server)',
    )
  })

  it('says it is off unless chosen, and that no identifier is sent', () => {
    expect(UPDATE_CHECK_DETAIL).toMatch(/off unless you turn it on/i)
    expect(UPDATE_CHECK_DETAIL).toMatch(/no version number/i)
    expect(UPDATE_CHECK_DETAIL).not.toMatch(/version number in its User-Agent/i)
  })
})
