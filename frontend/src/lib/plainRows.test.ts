import { describe, expect, it } from 'vitest'
import { plainRows } from './plainRows'

describe('plainRows', () => {
  it.each([
    ['one short line', 'hello', 40, 1],
    ['a line that fills a row exactly', 'a'.repeat(40), 40, 1],
    ['a line one character over', 'a'.repeat(41), 40, 2],
    ['each stored line is a row', 'a\nb\nc', 40, 3],
    ['blank lines take a row each', 'a\n\n\nb', 40, 4],
    ['long lines wrap, short ones do not', 'a'.repeat(100) + '\nb', 40, 4],
    ['empty text is one row', '', 40, 1],
    ['a width too small for one character still counts a row per character', 'abc', 0, 3],
  ])('%s', (_, text, perRow, rows) => {
    expect(plainRows(text, perRow)).toBe(rows)
  })
})
