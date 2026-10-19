import { Component, useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'
import type { Root } from 'hast'
import { renderHast } from '@/lib/markdownComponents'
import { formatMarkdown, type FormatResult } from '@/lib/markdownRunner'
import type { TextFormat } from '@/api/types'

interface Props {
  text: string
  /** How the stored text was written. Never guessed from the text itself. */
  format: TextFormat
  /** Extra classes for the text box (size, colour); layout classes stay ours. */
  className?: string
}

const plainClass = 'whitespace-pre-wrap wrap-anywhere'

// A document the parser cannot handle (pathological nesting) falls back to the
// stored source rather than taking the page down with it.
class FallbackToSource extends Component<{ text: string; className?: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() {
    return { failed: true }
  }
  render() {
    if (this.state.failed) return <p className={cn(plainClass, this.props.className)}>{this.props.text}</p>
    return this.props.children
  }
}

// Turning the tree into elements happens here, inside the boundary, so a tree
// that fails the page's own check falls back to the source instead of taking
// the page down.
function Formatted({ tree, className }: { tree: Root; className?: string }) {
  return (
    <div data-md-body className={cn('min-w-0 space-y-2', className)}>
      {renderHast(tree)}
    </div>
  )
}

/**
 * Ticket text: a description, a reply or an internal note.
 *
 * `plain` shows the stored characters as typed (today's behaviour: no links,
 * nothing interpreted). `markdown` shows them through the strict renderer in
 * lib/markdownPipeline. The toggle is the reader's: it swaps to the other view
 * of the same text and stores nothing.
 */
export function TicketText({ text, format, className }: Props) {
  const [flipped, setFlipped] = useState(false)
  const wantsRendered = (format === 'markdown') !== flipped
  // The result is kept with the text it is for, so a new text is never shown
  // with the previous text's formatting.
  const [done, setDone] = useState<{ text: string; result: FormatResult } | null>(null)

  useEffect(() => {
    if (!wantsRendered) return
    let alive = true
    void formatMarkdown(text).then((result) => {
      if (alive) setDone({ text, result })
    })
    return () => {
      alive = false
    }
  }, [text, wantsRendered])

  // Until the worker answers (and whenever it refuses), the text is shown as
  // written: the post is readable at once and never blocks the page.
  const result = wantsRendered && done?.text === text ? done.result : null
  const tree = result?.ok ? result.tree : null
  const refusal = result && !result.ok ? (result.reason === 'long' ? 'too long' : 'too complex') : null

  return (
    <div className="min-w-0">
      {tree ? (
        <FallbackToSource key={text} text={text} className={className}>
          <Formatted tree={tree} className={className} />
        </FallbackToSource>
      ) : (
        <p className={cn(plainClass, className)}>{text}</p>
      )}
      {refusal && <p className="mt-1 text-xs text-gray-500">Shown as plain text: {refusal} to format.</p>}
      {text.trim() !== '' && (
        <button
          type="button"
          aria-pressed={flipped}
          onClick={() => setFlipped((f) => !f)}
          className="mt-1 inline-flex min-h-6 items-center py-1 text-xs text-gray-500 underline hover:text-gray-700 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2"
        >
          {format === 'markdown' ? 'Plain text' : 'Show formatted'}
        </button>
      )}
    </div>
  )
}
