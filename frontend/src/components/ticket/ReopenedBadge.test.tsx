import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'

import { ReopenedBadge } from './ReopenedBadge'

// "Reopened" (#464): shown while a ticket that has been reopened is open, and
// hidden once it is Resolved or Closed again. The count is never reset on the
// server, so this component is where "until it is resolved again" lives.

describe('ReopenedBadge', () => {
  it('shows on an open ticket that has been reopened', () => {
    const { container } = render(<ReopenedBadge reopenCount={1} statusName="In Progress" />)
    const badge = container.querySelector('[data-testid="reopened-badge"]')
    expect(badge?.textContent).toBe('Reopened')
  })

  it('still shows after a second reopen', () => {
    const { container } = render(<ReopenedBadge reopenCount={3} statusName="New" />)
    expect(container.querySelector('[data-testid="reopened-badge"]')).not.toBeNull()
  })

  it.each([
    ['never reopened', 0, 'New'],
    ['no count at all (a response without one)', undefined, 'New'],
    ['resolved again', 2, 'Resolved'],
    ['closed', 1, 'Closed'],
    ['status not known yet', 1, undefined],
  ])('is absent when %s', (_name, count, status) => {
    const { container } = render(<ReopenedBadge reopenCount={count} statusName={status} />)
    expect(container.firstChild).toBeNull()
  })
})
