import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { TextLimit } from './TextLimit'

describe('TextLimit', () => {
  it('says nothing for short text', () => {
    const { container } = render(<TextLimit value="hello" />)
    expect(container.textContent).toBe('')
  })

  it('shows the count near the limit', () => {
    const { container } = render(<TextLimit value={'a'.repeat(46_000)} />)
    expect(container.textContent).toContain('46,000 / 50,000 bytes')
    expect(container.querySelector('[role="alert"]')).toBeNull()
  })

  it('says how far over, as an alert, past the limit', () => {
    const { getByRole } = render(<TextLimit value={'a'.repeat(50_250)} />)
    expect(getByRole('alert').textContent).toContain('1% over the size limit (50,000 bytes)')
    expect(getByRole('alert').textContent).toContain('Letters outside basic English and emoji take 2 to 4 bytes each.')
    // Never says "characters": the limit is bytes.
    expect(getByRole('alert').textContent).not.toMatch(/character/i)
  })

  it('says how far over as a percent, rounded up', () => {
    expect(render(<TextLimit value={'a'.repeat(55_000)} />).getByRole('alert').textContent).toContain('10% over')
  })

  it('has a live region that is always there, so a screen reader hears the change', () => {
    const { container } = render(<TextLimit value="" />)
    expect(container.querySelector('[aria-live="polite"]')).not.toBeNull()
  })
})
