import { describe, expect, it } from 'vitest'
import {
  ATTACHMENT_MAX_MB_DEFAULT,
  ATTACHMENT_MAX_MB_MAX,
  ATTACHMENT_MAX_MB_MIN,
  shownAttachmentMaxMB,
} from './attachmentLimit'

describe('shownAttachmentMaxMB', () => {
  it('keeps the bounds and the default the server uses', () => {
    expect([ATTACHMENT_MAX_MB_MIN, ATTACHMENT_MAX_MB_MAX, ATTACHMENT_MAX_MB_DEFAULT]).toEqual([1, 100, 25])
  })

  const cases: { name: string; input: unknown; want: number }[] = [
    { name: 'a value in range is shown as it is', input: 50, want: 50 },
    { name: 'the lowest allowed value', input: 1, want: 1 },
    { name: 'the highest allowed value', input: 100, want: 100 },
    { name: 'unset is the default', input: undefined, want: 25 },
    { name: 'null is the default', input: null, want: 25 },
    { name: 'zero is out of range', input: 0, want: 25 },
    { name: 'above the top is out of range', input: 101, want: 25 },
    { name: 'a negative number is out of range', input: -5, want: 25 },
    { name: 'a fraction is not whole megabytes', input: 12.5, want: 25 },
    { name: 'NaN is not a limit', input: Number.NaN, want: 25 },
    { name: 'a string is not a limit, even a numeric one', input: '50', want: 25 },
  ]
  for (const c of cases) {
    it(c.name, () => {
      expect(shownAttachmentMaxMB(c.input)).toBe(c.want)
    })
  }
})
