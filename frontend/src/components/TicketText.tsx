import { Component, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'
import type { Root } from 'hast'
import { renderHast } from '@/lib/markdownComponents'
import { formatMarkdown, type FormatResult } from '@/lib/markdownRunner'
import { pageBudget } from '@/lib/pageBudget'
import { MAX_FORMATTED_CHARS } from '@/lib/markdownLimits'
import { plainRows } from '@/lib/plainRows'
import type { TextFormat } from '@/api/types'

interface Props {
  text: string
  /** How the stored text was written. Never guessed from the text itself. */
  format: TextFormat
  /** Extra classes for the text box (size, colour); layout classes stay ours. */
  className?: string
}

const plainClass = 'whitespace-pre-wrap wrap-anywhere'

// Plain text longer than this is laid out only when it scrolls near the screen
// (content-visibility: auto, see .lazy-layout in index.css). 220 replies of 50 KB
// took 3.3 s to lay out at once. Short posts are left alone: skipping them saves
// nothing and every one would swap an estimate for its real height on scrolling.
const LAZY_LAYOUT_CHARS = 5_000

// How wide one Latin character is in the post's own font, in pixels, with room
// for what wrapping loses at the end of each row (part of a word that moved to
// the next one: 6 to 10 percent measured). Measured, not assumed: half the font size
// (Safari's and Chrome's sans-serif on a Mac) was 10 percent low in the wider
// sans-serif of a Linux CI runner, which showed as a 2,000 px jump in WebKit, and
// 20 percent low in a monospace font. A sentence with a normal letter mix, so
// the average is a reader's, not the width of one letter.
const LATIN_SAMPLE = 'Thanks for getting back to us. We tried restarting the service twice, but the error is still there.'
const WRAP_LOSS = 1.1
function latinCharWidth(el: HTMLElement, fontSize: number): number {
  const style = getComputedStyle(el)
  const ctx = document.createElement('canvas').getContext('2d')
  if (ctx) {
    ctx.font = `${style.fontStyle} ${style.fontWeight} ${style.fontSize} ${style.fontFamily}`
    const width = ctx.measureText(LATIN_SAMPLE).width / LATIN_SAMPLE.length
    if (width > 0) return width * WRAP_LOSS
  }
  return 0.5 * fontSize
}

// The plain text of a post. A long one is laid out only when it scrolls near the
// screen, and until then the browser needs a height for it. Too small a guess
// and the page grows under the reader as posts are drawn, and Safari, which does
// not hold the reader's place when content above them changes size, jumps; so the
// guess is the post's own rows at the box's own width, and the browser remembers
// the real height once it has drawn the post (`auto` in .lazy-layout).
function PlainText({ text, className }: { text: string; className?: string }) {
  const lazy = text.length > LAZY_LAYOUT_CHARS
  const ref = useRef<HTMLParagraphElement>(null)
  useEffect(() => {
    const el = ref.current
    if (!lazy || !el || typeof ResizeObserver === 'undefined') return
    const fontSize = parseFloat(getComputedStyle(el).fontSize)
    const charWidth = latinCharWidth(el, fontSize)
    // A wide character is a font size wide, whatever the font's Latin letters are.
    const wideUnits = fontSize / charWidth
    let width = -1
    const observer = new ResizeObserver(() => {
      // Also fires when the post is drawn and its height changes; the width is
      // all the estimate depends on.
      if (el.clientWidth === width) return
      width = el.clientWidth
      // Within about 7 percent of the drawn height for English and Chinese prose
      // in sans-serif and monospace fonts, and exact for text written a line at
      // a time.
      el.style.setProperty('--text-rows', String(plainRows(text, width / charWidth, wideUnits)))
    })
    observer.observe(el)
    return () => observer.disconnect()
  }, [text, lazy])
  return (
    <p ref={ref} className={cn(plainClass, lazy && 'lazy-layout', className)}>
      {text}
    </p>
  )
}

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
        <PlainText text={text} className={className} />
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

/** Characters as a reader counts them (an emoji is one), not UTF-16 units. */
function countChars(s: string): number {
  let n = 0
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i)
    if (c >= 0xd800 && c <= 0xdbff) i++
    n++
  }
  return n
}

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
  const start = startOf(text)

  return (
    <div className="min-w-0">
      {expanded ? (
        <FullText {...props} />
      ) : (
        <>
          <p className={cn(plainClass, className)}>{start}</p>
          <p role="note" className="mt-1 text-xs text-gray-600">
            Showing the first {countChars(start).toLocaleString('en-US')} of {countChars(text).toLocaleString('en-US')} characters.
            This is only part of the post: copying here copies only that part.
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
