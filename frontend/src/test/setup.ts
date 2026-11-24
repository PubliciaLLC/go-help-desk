import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'

// React Testing Library registers its own afterEach(cleanup) only when Vitest
// runs with `globals: true`. This project does not, so without this the DOM
// from one test is still mounted during the next: queries then match elements
// belonging to a previous test, and a passing test starts failing purely
// because of what ran before it.
afterEach(cleanup)
