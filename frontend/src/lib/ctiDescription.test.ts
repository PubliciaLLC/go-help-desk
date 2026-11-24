import { describe, expect, it } from 'vitest'
import { mostSpecificDescription, trimDescription, isDescriptionTooLong, CTI_DESCRIPTION_MAX } from './ctiDescription'

// One state per level: not chosen, chosen with no description, chosen with one.
type State = 'none' | 'bare' | 'text'
const STATES: State[] = ['none', 'bare', 'text']
const row = (state: State, label: string) =>
  state === 'none' ? undefined : { description: state === 'text' ? label : '' }

describe('mostSpecificDescription', () => {
  // Every combination of "is this level chosen" and "has it a description", 27 of
  // them. The expectation is written the other way round from the code (walk from
  // the category down and keep the last level that has text) so the two cannot
  // share a mistake.
  const cases = STATES.flatMap((c) =>
    STATES.flatMap((t) =>
      STATES.map((i) => {
        let want = ''
        if (c === 'text') want = 'category text'
        if (t === 'text') want = 'type text'
        if (i === 'text') want = 'item text'
        return { c, t, i, want }
      }),
    ),
  )
  it.each(cases)('category $c, type $t, item $i', ({ c, t, i, want }) => {
    expect(mostSpecificDescription(row(c, 'category text'), row(t, 'type text'), row(i, 'item text'))).toBe(want)
  })

  // The rules that matter most, spelled out as well as generated.
  it.each([
    { name: 'the item wins over the type and the category', c: 'C', t: 'T', i: 'I', want: 'I' },
    { name: 'an item with none falls back to its type', c: 'C', t: 'T', i: '', want: 'T' },
    { name: 'a type with none falls back to its category', c: 'C', t: '', i: '', want: 'C' },
    { name: 'an item with none, under a type with none, falls back to the category', c: 'C', t: '', i: '', want: 'C' },
    { name: 'nothing shows when no chosen level has one', c: '', t: '', i: '', want: '' },
    { name: 'text only on the item still shows', c: '', t: '', i: 'I', want: 'I' },
  ])('$name', ({ c, t, i, want }) => {
    expect(mostSpecificDescription({ description: c }, { description: t }, { description: i })).toBe(want)
  })

  it('shows nothing before a category is chosen', () => {
    expect(mostSpecificDescription(undefined, undefined, undefined)).toBe('')
    expect(mostSpecificDescription(null, null, null)).toBe('')
  })

  it('treats a row from an older server, with no description key, as having none', () => {
    expect(mostSpecificDescription({}, { description: 'T' }, {})).toBe('T')
    expect(mostSpecificDescription({}, {}, {})).toBe('')
  })

  it('treats whitespace as none and returns the text trimmed', () => {
    expect(mostSpecificDescription({ description: 'C' }, { description: ' \n ' })).toBe('C')
    expect(mostSpecificDescription({ description: ' C \n' })).toBe('C')
  })

  it('returns the text as it is, markup included: it is shown as text, never parsed', () => {
    expect(mostSpecificDescription({ description: '<b>x</b>\n**y**' })).toBe('<b>x</b>\n**y**')
  })

  // JS trim() strips U+FEFF and keeps U+0085; Go's TrimSpace does the opposite.
  // The server strips both, and so does this, so what the form accepts the
  // server does not refuse for length.
  it('trims the same set as the server: a byte order mark and a next-line character too', () => {
    expect(trimDescription('﻿ a \u0085')).toBe('a')
    expect(trimDescription('   a  ')).toBe('a')
    expect(trimDescription(' a ﻿ b ')).toBe('a ﻿ b') // only at the ends
    expect(mostSpecificDescription({ description: '\u0085﻿' })).toBe('')
    expect(isDescriptionTooLong('﻿' + 'a'.repeat(500) + '\u0085')).toBe(false)
    expect(isDescriptionTooLong('a'.repeat(501))).toBe(true)
  })

  it('has the same limit as the server', () => {
    expect(CTI_DESCRIPTION_MAX).toBe(500)
  })
})
