import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'

import { QuarantinedBadge } from './QuarantinedBadge'

// #628: the ticket list marks a ticket that holds a quarantined file. The
// server decides it and sends the flag only when true, so anything else is
// rendered as nothing.

describe('QuarantinedBadge', () => {
  it('shows, in words, when the ticket holds a quarantined file', () => {
    const { container } = render(<QuarantinedBadge hasQuarantined={true} />)
    const badge = container.querySelector('[data-testid="quarantined-badge"]')
    expect(badge?.textContent).toBe('Quarantined file')
    expect(badge?.getAttribute('title')).toMatch(/malicious/i)
  })

  it.each([
    ['false', false],
    ['absent (omitted by the server, or a requester)', undefined],
  ])('is absent when the flag is %s', (_name, flag) => {
    const { container } = render(<QuarantinedBadge hasQuarantined={flag} />)
    expect(container.firstChild).toBeNull()
  })
})
