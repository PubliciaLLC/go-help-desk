import { Component, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'
import type { Root } from 'hast'
import { renderHast } from '@/lib/markdownComponents'
import { formatMarkdown, type FormatResult } from '@/lib/markdownRunner'
import { pageBudget } from '@/lib/pageBudget'
import { MAX_FORMATTED_CHARS } from '@/lib/markdownLimits'
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
const NOTES: Record<string, string> = {
  long: 'too long to format',
  complex: 'too complex to format',
  timeout: 'too complex to format',
  error: 'too complex to format',
  unavailable: 'formatting unavailable',
  budget: 'this page already shows a lot of formatted text',
}

// Far enough around the screen that a post is formatted before it is reached.
const NEAR = '1500px 0px'

// The page scrolls inside <main>, not the window, and a scrolling ancestor clips
// what an observer rooted at the window can see: rootMargin would not reach
// below it. So the observer is rooted at the nearest scrolling ancestor.
function scrollParent(el: Element): Element | null {
  for (let p = el.parentElement; p; p = p.parentElement) {
    const { overflowY } = getComputedStyle(p)
    if (overflowY === 'auto' || overflowY === 'scroll') return p
  }
  return null
}

/** True once the element has been within NEAR of the screen. Stays true. */
function useNearScreen(enabled: boolean) {
  const ref = useRef<HTMLDivElement>(null)
  // No IntersectionObserver (tests): everything counts as near.
  const [near, setNear] = useState(typeof IntersectionObserver === 'undefined')
  useEffect(() => {
    const el = ref.current
    if (!enabled || near || !el) return
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) setNear(true)
      },
      { root: scrollParent(el), rootMargin: NEAR },
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [enabled, near])
  return [ref, near] as const
}

function FullText({ text, format, className }: Props) {
  const [flipped, setFlipped] = useState(false)
  const wantsRendered = (format === 'markdown') !== flipped
  // A long page is not formatted all at once: a post waits until it is near the
  // screen. Until then, and past the page budget, it is shown as written.
  const [boxRef, near] = useNearScreen(wantsRendered)
  // The result is kept with the text it is for, so a new text is never shown
  // with the previous text's formatting.
  const [done, setDone] = useState<{ text: string; result: FormatResult } | null>(null)

  useEffect(() => {
    if (!wantsRendered || !near) return
    let alive = true
    let held = 0
    void formatMarkdown(text).then((result) => {
      if (!alive) return
      if (result.ok) {
        if (pageBudget.reserve(result.nodes)) held = result.nodes
        else result = { ok: false, reason: 'budget' }
      }
      setDone({ text, result })
    })
    return () => {
      alive = false
      pageBudget.release(held)
    }
  }, [text, wantsRendered, near])

  // Until the worker answers (and whenever it refuses), the text is shown as
  // written: the post is readable at once and never blocks the page.
  const result = wantsRendered && done?.text === text ? done.result : null
  const tree = result?.ok ? result.tree : null
  const refusal = result && !result.ok ? NOTES[result.reason] : null

  return (
    <div ref={boxRef} className="min-w-0">
      {tree ? (
        <FallbackToSource key={text} text={text} className={className}>
          <Formatted tree={tree} className={className} />
        </FallbackToSource>
      ) : (
        <p className={cn(plainClass, className)}>{text}</p>
      )}
      {refusal && <p className="mt-1 text-xs text-gray-500">Shown as plain text: {refusal}.</p>}
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

/** How much of a very long post is shown before the reader asks for the rest. */
const PREVIEW_CHARS = 5_000

function sizeOf(text: string): string {
  const bytes = new TextEncoder().encode(text).length
  return bytes >= 1_000_000 ? `${(bytes / 1_000_000).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1000))} KB`
}

/** The first PREVIEW_CHARS, never ending in half of a surrogate pair. */
function startOf(text: string): string {
  let end = PREVIEW_CHARS
  const last = text.charCodeAt(end - 1)
  if (last >= 0xd800 && last <= 0xdbff) end--
  return text.slice(0, end)
}

/**
 * Ticket text: a description, a reply or an internal note.
 *
 * `plain` shows the stored characters as typed (today's behaviour: no links,
 * nothing interpreted). `markdown` shows them through the strict renderer in
 * lib/markdownPipeline. The toggle is the reader's: it swaps to the other view
 * of the same text and stores nothing.
 *
 * Text over 50,000 characters can only be older data (a reply had no cap before
 * 1.3.0; new ones are refused). It is shown collapsed: the start, a note that
 * this is only the start, and a button for all of it. Laying out a megabyte takes
 * seconds, so that happens only when asked for, and then in one piece, so the
 * whole post can be selected and copied exactly as stored. Nothing is cut or
 * rewritten; the rest is one click away.
 */
export function TicketText(props: Props) {
  const { text, className } = props
  const [expanded, setExpanded] = useState(false)
  if (text.length <= MAX_FORMATTED_CHARS) return <FullText {...props} />

  return (
    <div className="min-w-0">
      {expanded ? (
        <FullText {...props} />
      ) : (
        <>
          <p className={cn(plainClass, className)}>{startOf(text)}</p>
          <p role="note" className="mt-1 text-xs text-gray-600">
            Showing the first {PREVIEW_CHARS.toLocaleString('en-US')} of {text.length.toLocaleString('en-US')} characters. This is
            only part of the post: copying here copies only that part.
          </p>
        </>
      )}
      <button
        type="button"
        aria-expanded={expanded}
        onClick={() => setExpanded((e) => !e)}
        className="mt-1 inline-flex min-h-6 items-center py-1 text-xs text-gray-700 underline hover:text-gray-900 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2"
      >
        {expanded ? 'Show only the start' : `Show full text (${sizeOf(text)})`}
      </button>
    </div>
  )
}
