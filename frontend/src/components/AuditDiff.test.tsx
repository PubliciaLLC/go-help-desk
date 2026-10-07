import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { AuditDiff } from './AuditDiff'

describe('AuditDiff', () => {
  it('shows only the fields that actually changed', () => {
    render(<AuditDiff before={{ status_id: 'a', priority: 'low' }} after={{ status_id: 'b', priority: 'low' }} />)

    expect(screen.getByText(/status_id/)).toBeTruthy()
    expect(screen.getByText(/a → b/)).toBeTruthy()
    // priority is unchanged, so it must not appear as a diff line.
    expect(screen.queryByText(/priority/)).toBeNull()
  })

  it('formats a missing value as an em dash, not "undefined" or "null"', () => {
    render(<AuditDiff before={{ assignee_id: null }} after={{ assignee_id: 'u-1' }} />)

    expect(screen.getByText(/— → u-1/)).toBeTruthy()
  })

  it('renders nothing when only one side is present — a create/delete action, not a change', () => {
    const { container: beforeOnly } = render(<AuditDiff before={{ status_id: 'a' }} after={null} />)
    expect(beforeOnly.textContent).toBe('')

    const { container: afterOnly } = render(<AuditDiff before={null} after={{ status_id: 'a' }} />)
    expect(afterOnly.textContent).toBe('')
  })

  it('renders nothing when both sides are present but identical', () => {
    const { container } = render(<AuditDiff before={{ status_id: 'a' }} after={{ status_id: 'a' }} />)
    expect(container.textContent).toBe('')
  })

  it('renders nothing when both sides are absent', () => {
    const { container } = render(<AuditDiff />)
    expect(container.textContent).toBe('')
  })

  // #329: the server replaces a secret with the same placeholder on both
  // sides, so a rotated secret looks "unchanged" to a naive comparison and
  // used to vanish. Showing it lets an admin see the field was touched.
  it('shows a redacted field as [redacted] → [redacted] instead of hiding it', () => {
    render(
      <AuditDiff
        before={{ status_id: 'a', api_key: '[redacted]' }}
        after={{ status_id: 'a', api_key: '[redacted]' }}
      />,
    )

    expect(screen.getByText(/api_key/)).toBeTruthy()
    expect(screen.getByText(/\[redacted\] → \[redacted\]/)).toBeTruthy()
    expect(screen.queryByText(/status_id/)).toBeNull()
  })

  it('shows a field with a redacted value nested inside it', () => {
    render(
      <AuditDiff
        before={{ config: { name: 'smtp', token: '[redacted]' } }}
        after={{ config: { name: 'smtp', token: '[redacted]' } }}
      />,
    )

    expect(screen.getByText(/config/)).toBeTruthy()
  })

  it('stringifies a non-string value rather than showing "[object Object]"', () => {
    render(<AuditDiff before={{ tags: ['a'] }} after={{ tags: ['a', 'b'] }} />)

    expect(screen.getByText(/\["a"\] → \["a","b"\]/)).toBeTruthy()
  })
})
