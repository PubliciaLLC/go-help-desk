// The server marks each matched word with U+0002 before it and U+0003 after it
// instead of HTML tags, so a reply that contains markup has none to confuse.
const START = '\u0002'
const STOP = '\u0003'

export interface SnippetPiece {
  text: string
  match: boolean
}

// Splits a snippet into plain and matched pieces. The pieces are rendered as
// React text nodes, never as HTML: a reply is written by anyone, a guest
// included. A reply holding the marker characters itself can at worst move a
// highlight, so this reads whatever it is given and never throws.
export function splitSnippet(snippet: string): SnippetPiece[] {
  const pieces: SnippetPiece[] = []
  let text = ''
  let match = false
  const flush = () => {
    if (text !== '') pieces.push({ text, match })
    text = ''
  }
  for (const ch of snippet) {
    if (ch === START) {
      flush()
      match = true
    } else if (ch === STOP) {
      flush()
      match = false
    } else {
      text += ch
    }
  }
  flush()
  return pieces
}
