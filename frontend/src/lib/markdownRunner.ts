import type { Root } from 'hast'
import { runFormat } from '@/lib/markdownBackend'
import { isTooComplexToFormat, MAX_FORMATTED_CHARS } from './markdownLimits'

// #443: Markdown is parsed in a Web Worker with a hard time budget (see
// markdownBackend.ts). Counting markers in the text cannot catch every input
// whose parse is slow or whose result is huge (a table header's width
// multiplies every short row; unclosed brackets rescan the rest of the text),
// and a reply has no length cap, so the only guarantee is to bound the work
// itself: past the budget the worker is terminated and the post is shown as
// plain text.

export type FormatResult =
  | { ok: true; tree: Root }
  | { ok: false; reason: 'long' | 'complex' | 'timeout' | 'error' }

// Results are kept so that a post is not formatted twice (a re-render, a toggle,
// the same text posted again), and a post that timed out is not retried.
const results = new Map<string, FormatResult>()
const MAX_CACHED = 100

/**
 * Formats Markdown text for display, or says why it will not be: over the
 * length limit, too complex, out of time, or failed. Never throws.
 */
export async function formatMarkdown(text: string): Promise<FormatResult> {
  if (text.length > MAX_FORMATTED_CHARS) return { ok: false, reason: 'long' }
  if (isTooComplexToFormat(text)) return { ok: false, reason: 'complex' }
  const hit = results.get(text)
  if (hit) return hit
  const result = await runFormat(text)
  if (results.size >= MAX_CACHED) results.delete(results.keys().next().value!)
  results.set(text, result)
  return result
}
