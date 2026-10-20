import { describe, expect, it } from 'vitest'
import { CHUNK_TARGET, CHUNK_THRESHOLD, chunkPlain } from './plainChunks'

const joined = (text: string) => chunkPlain(text).map((c) => c.text + c.sep).join('')

describe('chunkPlain', () => {
  it('leaves ordinary text whole', () => {
    expect(chunkPlain('hello\nworld')).toEqual([{ text: 'hello\nworld', sep: '' }])
    const atLimit = 'x\n'.repeat(CHUNK_THRESHOLD / 2)
    expect(chunkPlain(atLimit)).toHaveLength(1)
  })

  const fill = (u: string, n: number) => u.repeat(Math.ceil(n / u.length)).slice(0, n)
  const texts: [string, string][] = [
    ['soft newlines', fill('a\n', 1_000_000)],
    ['hard breaks', fill('a  \n', 300_000)],
    ['words', fill('lorem ipsum ', 300_000)],
    ['one unbroken token', 'x'.repeat(300_000)],
    ['blank lines', fill('a\n\n\n', 100_000)],
    ['starts and ends with newlines', '\n\n' + fill('line\n', 50_000) + '\n\n'],
    ['crlf', fill('a\r\n', 50_000)],
    ['spaces only', ' '.repeat(100_000)],
    ['unicode', fill('naïve 日本語 😀\n', 50_000)],
    ['a newline exactly at each boundary', fill('x'.repeat(CHUNK_TARGET - 1) + '\n', 100_000)],
  ]

  it.each(texts)('loses nothing: %s', (_n, text) => {
    expect(joined(text)).toBe(text)
  })

  it.each(texts)('keeps every chunk small: %s', (_n, text) => {
    for (const c of chunkPlain(text)) expect(c.text.length).toBeLessThanOrEqual(CHUNK_TARGET * 2)
  })

  it.each(texts)('never makes an empty chunk (it would draw no line): %s', (_n, text) => {
    const chunks = chunkPlain(text)
    expect(chunks.filter((c) => c.text === '' && c.sep !== '')).toEqual([])
  })

  it('splits a megabyte into many chunks, so layout can skip the ones off screen', () => {
    expect(chunkPlain(fill('a\n', 1_000_000)).length).toBeGreaterThan(100)
  })

  it('splits at line ends when it can, so no line is broken', () => {
    for (const c of chunkPlain(fill('word one two\n', 200_000)).slice(0, -1)) expect(c.sep).toBe('\n')
  })
})
