import { describe, expect, it } from 'vitest'
import { EMAIL_SWITCHES } from '@/components/EmailNotificationsSection'
import { STAFF_EMAIL_SWITCHES } from './StaffEmailSection'

// The administrator's override rows (Settings -> Email) mirror the switches on
// each person's own profile (#670). The two lists must not drift: same names,
// same order. A change to one without the other fails here.
describe('staff email override mirrors the profile switches', () => {
  it('names the overrides after the profile labels, in order', () => {
    expect(STAFF_EMAIL_SWITCHES.map((s) => s.name)).toEqual(EMAIL_SWITCHES.map((s) => s.label))
  })
})
