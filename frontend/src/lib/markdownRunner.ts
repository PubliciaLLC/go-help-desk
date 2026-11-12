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
  | { ok: true; tree: Root; /** elements and text nodes, for the page budget */ nodes: number }
  | { ok: false; reason: FormatRefusal }

/**
 * Why a post is shown as written. long and complex are properties of the text.
 * timeout and error are about this attempt. unavailable means the worker could
 * not start at all. budget is the page already holding as much as it will.
 */
export type FormatRefusal = 'long' | 'complex' | 'timeout' | 'error' | 'unavailable' | 'budget'

/** What the backend returns: the same, before the runner has counted the tree. */
export type BackendResult = { ok: true; tree: Root } | { ok: false; reason: FormatRefusal }

/** Descendants of the root: the elements and text nodes the page will create. */
function countNodes(node: { children?: unknown[] }): number {
  let n = 0
  for (const c of node.children ?? []) n += 1 + countNodes(c as { children?: unknown[] })
  return n
}

// Results are kept so that a post is not formatted twice (a re-render, a toggle,
// the same text posted again), and a failure that is only about this attempt is retried.
const results = new Map<string, FormatResult>()
// Small: every entry holds a tree. A page of replies is mostly distinct texts.
const MAX_CACHED = 20

/**
 * Formats Markdown text for display, or says why it will not be: over the
 * length limit, too complex, out of time, or failed. Never throws.
 */
export async function formatMarkdown(text: string): Promise<FormatResult> {
  if (text.length > MAX_FORMATTED_CHARS) return { ok: false, reason: 'long' }
  if (isTooComplexToFormat(text)) return { ok: false, reason: 'complex' }
  const hit = results.get(text)
  if (hit) return hit
  const raw = await runFormat(text)
  const result: FormatResult = raw.ok ? { ok: true, tree: raw.tree, nodes: countNodes(raw.tree) } : raw
  // Remember what will not change. A timeout, an error or an unavailable worker
  // may be a slow moment, so the next view tries again.
  if (result.ok || result.reason === 'long' || result.reason === 'complex') {
    if (results.size >= MAX_CACHED) results.delete(results.keys().next().value!)
    results.set(text, result)
  }
  return result
}
