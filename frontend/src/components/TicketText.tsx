import { Component, useState } from 'react'
import type { ReactNode } from 'react'
import ReactMarkdown from 'react-markdown'
import { cn } from '@/lib/utils'
import { markdownProps } from '@/lib/markdownPipeline'
import { isTooComplexToFormat } from '@/lib/markdownLimits'
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
  // Text that would make the parser slow is shown as written, and says so.
  const tooComplex = wantsRendered && isTooComplexToFormat(text)
  const rendered = wantsRendered && !tooComplex

  return (
    <div className="min-w-0">
      {rendered ? (
        <FallbackToSource text={text} className={className}>
          <div data-md-body className={cn('min-w-0 space-y-2', className)}>
            <ReactMarkdown {...markdownProps}>{text}</ReactMarkdown>
          </div>
        </FallbackToSource>
      ) : (
        <p className={cn(plainClass, className)}>{text}</p>
      )}
      {tooComplex && (
        <p className="mt-1 text-xs text-gray-500">Shown as plain text: too complex to format.</p>
      )}
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
