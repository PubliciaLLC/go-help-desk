// A megabyte of plain text laid out as one block cost seconds of main-thread time
// (500,000 lines of layout), and a reply has no length cap. Text over the
// threshold is cut into blocks of about CHUNK_TARGET characters that the browser
// may skip while they are off screen (content-visibility: auto). Nothing is
// dropped or rewritten: the cut falls on a newline where there is one (and then
// the block boundary draws that newline), else on a space, else inside a token.

export const CHUNK_THRESHOLD = 10_000
export const CHUNK_TARGET = 4_000

/** text, then sep, is the original: chunks.map(c => c.text + c.sep).join('') === the whole. */
export interface PlainChunk {
  text: string
  sep: '' | '\n' | ' '
}

export function chunkPlain(text: string): PlainChunk[] {
  if (text.length <= CHUNK_THRESHOLD) return [{ text, sep: '' }]
  const out: PlainChunk[] = []
  let i = 0
  while (text.length - i > CHUNK_TARGET) {
    const nl = text.indexOf('\n', i + CHUNK_TARGET)
    if (nl !== -1 && nl - i <= CHUNK_TARGET * 2) {
      out.push({ text: text.slice(i, nl), sep: '\n' })
      i = nl + 1
      continue
    }
    const sp = text.lastIndexOf(' ', i + CHUNK_TARGET)
    if (sp > i) {
      out.push({ text: text.slice(i, sp), sep: ' ' })
      i = sp + 1
      continue
    }
    out.push({ text: text.slice(i, i + CHUNK_TARGET), sep: '' })
    i += CHUNK_TARGET
  }
  if (i < text.length) out.push({ text: text.slice(i), sep: '' })
  return out
}
