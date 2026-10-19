import { FormatRefused, markdownToHast } from './markdownCore'
import type { BackendResult } from './markdownRunner'

// Tests only: the same formatting, in this thread, because jsdom has no Worker.
// The browser build uses markdownBackend.ts (a Web Worker with a time budget).
export async function runFormat(text: string): Promise<BackendResult> {
  try {
    return { ok: true, tree: markdownToHast(text) }
  } catch (err) {
    return { ok: false, reason: err instanceof FormatRefused ? 'complex' : 'error' }
  }
}
