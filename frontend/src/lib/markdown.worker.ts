import { FormatRefused, markdownToHast } from './markdownCore'

// #443: formatting runs here, off the page, so that however long the parser
// takes the page stays responsive and the caller can terminate this worker when
// its time budget runs out (lib/markdownRunner.ts). The sanitize step is part
// of markdownToHast; nothing leaves this file unsanitized.

const scope = self as unknown as {
  postMessage(message: unknown): void
  onmessage: ((event: MessageEvent<{ id: number; text: string }>) => void) | null
}

scope.onmessage = ({ data: { id, text } }) => {
  try {
    scope.postMessage({ id, ok: true, tree: markdownToHast(text) })
  } catch (err) {
    scope.postMessage({ id, ok: false, reason: err instanceof FormatRefused ? 'complex' : 'error' })
  }
}
// The runner starts its time budget when it hears this, not when it creates the
// worker, so a slow network does not eat the budget.
scope.postMessage({ type: 'ready' })
