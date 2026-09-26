import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'

// React Testing Library registers its own afterEach(cleanup) only when Vitest
// runs with `globals: true`. This project does not, so without this the DOM
// from one test is still mounted during the next: queries then match elements
// belonging to a previous test, and a passing test starts failing purely
// because of what ran before it.
afterEach(cleanup)

// jsdom does not implement matchMedia at all — every component test that
// mounts Layout (nearly all of them) crashed with "window.matchMedia is not
// a function" once #296 added a breakpoint listener to close the mobile
// drawer on resize. The listener's actual behaviour is covered by
// e2e/mobile-layout.spec.ts, a real browser; this exists only so code that
// calls matchMedia has something to call under jsdom. Always reports
// "not matched" (matches: false), which is right for jsdom's fixed,
// desktop-sized default viewport.
if (typeof window.matchMedia !== 'function') {
  window.matchMedia = (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }) as MediaQueryList
}
