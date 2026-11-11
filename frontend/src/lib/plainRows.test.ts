import { describe, expect, it } from 'vitest'
import { DEFAULT_PER_ROW, plainRows } from './plainRows'

describe('plainRows', () => {
  it.each([
    ['one short line', 'hello', 40, 1],
    ['a line that fills a row exactly', 'a'.repeat(40), 40, 1],
    ['a line one character over', 'a'.repeat(41), 40, 2],
    ['each stored line is a row', 'a\nb\nc', 40, 3],
    ['blank lines take a row each', 'a\n\n\nb', 40, 4],
    ['long lines wrap, short ones do not', 'a'.repeat(100) + '\nb', 40, 4],
    ['empty text is one row', '', 40, 1],
  ])('%s', (_, text, perRow, rows) => {
    expect(plainRows(text, perRow)).toBe(rows)
  })
})

// A wide character takes two Latin characters' width, so a row holds half as
// many. Counting one unit per UTF-16 unit made the estimate for Chinese,
// Japanese and Korean text about half the drawn height.
describe('plainRows with wide characters', () => {
  it.each([
    ['CJK ideographs: 20 fit in a row of 40, 21 do not', '\u6253'.repeat(20), 40, 1],
    ['CJK ideographs, one over', '\u6253'.repeat(21), 40, 2],
    ['hiragana', '\u3042'.repeat(41), 40, 3],
    ['Hangul syllables', '\uAC00'.repeat(21), 40, 2],
    ['fullwidth Latin', '\uFF21'.repeat(21), 40, 2],
    ['ideographic space', '\u3000'.repeat(21), 40, 2],
    ['a CJK character beyond the BMP (a surrogate pair) counts as one wide character, not two units', '\u{20BB7}'.repeat(20), 40, 1],
    ['emoji (a surrogate pair) are wide: 20 fit, 21 do not', '\u{1F600}'.repeat(20), 40, 1],
    ['emoji, one over', '\u{1F600}'.repeat(21), 40, 2],
    ['a narrow character beyond the BMP counts as one unit, not two', '\u{1D49C}'.repeat(40), 40, 1],
    ['narrow and wide mixed on one line', 'a'.repeat(20) + '\u6253'.repeat(10), 40, 1],
    ['mixed, one over', 'a'.repeat(21) + '\u6253'.repeat(10), 40, 2],
    ['wide characters on one line do not carry over to the next', '\u6253'.repeat(20) + '\n' + 'a', 40, 2],
  ])('%s', (_, text, perRow, rows) => {
    expect(plainRows(text, perRow)).toBe(rows)
  })
})

// A width that is not a width (hidden ancestor, a font size that would not
// parse) must not give NaN, which CSS drops, leaving the skipped post no height,
// nor one row per character, which is what a width of 0 would give.
describe('plainRows with no usable width', () => {
  const text = 'a'.repeat(1000)
  const fallback = plainRows(text, DEFAULT_PER_ROW)
  it.each([['zero', 0], ['NaN', NaN], ['Infinity', Infinity], ['negative', -5], ['less than one character', 0.4], ['undefined', undefined as unknown as number]])(
    '%s uses the default width',
    (_, perRow) => {
      const rows = plainRows(text, perRow)
      expect(Number.isInteger(rows)).toBe(true)
      expect(rows).toBe(fallback)
    },
  )
  it('the default is a plausible row, not one character', () => {
    expect(DEFAULT_PER_ROW).toBeGreaterThanOrEqual(30)
    expect(fallback).toBeLessThan(100)
  })
})

