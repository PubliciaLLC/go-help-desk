import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { CtiDescription } from './CtiDescription'

describe('CtiDescription', () => {
  it('shows the most specific description', () => {
    render(
      <CtiDescription id="d" category={{ description: 'Cat' }} type={{ description: 'Typ' }} item={{ description: '' }} />,
    )
    expect(screen.getByText('Typ')).toBeDefined()
    expect(screen.queryByText('Cat')).toBeNull()
  })

  it('shows the text as text: markup is not parsed and newlines are kept for display', () => {
    const { container } = render(<CtiDescription id="d" category={{ description: '<b>x</b>\nsecond' }} />)
    const p = screen.getByText((_, el) => el?.tagName === 'P')
    expect(p.textContent).toBe('<b>x</b>\nsecond')
    expect(container.querySelector('b')).toBeNull()
    expect(p.className).toContain('whitespace-pre-line')
  })

  it('draws no box and takes no space when there is nothing to say, but the live region stays', () => {
    const { container } = render(<CtiDescription id="d" category={{ description: '' }} />)
    const region = container.querySelector('#d') as HTMLElement
    expect(region).not.toBeNull()
    expect(region.getAttribute('aria-live')).toBe('polite')
    expect(region.childElementCount).toBe(0)
    expect(region.textContent).toBe('')
  })

  it('is not a hover tooltip: no title attribute anywhere', () => {
    const { container } = render(<CtiDescription id="d" category={{ description: 'Cat' }} />)
    expect(container.querySelector('[title]')).toBeNull()
  })

  it('wraps long unbroken text instead of widening the page', () => {
    render(<CtiDescription id="d" category={{ description: 'x'.repeat(400) }} />)
    expect(screen.getByText('x'.repeat(400)).className).toContain('wrap-anywhere')
  })
})
