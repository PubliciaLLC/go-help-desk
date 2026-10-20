import type { ReactElement } from 'react'
import { act, render } from '@testing-library/react'

/**
 * Renders and lets TicketText finish formatting. In tests the formatter runs in
 * this thread and settles within microtasks, so one async act is enough; the
 * browser build uses a Web Worker (see e2e/markdown-dos.spec.ts).
 */
export async function renderNow(ui: ReactElement) {
  let result!: ReturnType<typeof render>
  await act(async () => {
    result = render(ui)
  })
  return result
}
