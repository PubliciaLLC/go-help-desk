import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { StatusDot } from './StatusDot'

// A status colour is the administrator's own, drawn as chosen (#709). A very dark
// one vanishes on a dark card, so the dot carries a hairline ring in a token
// border colour; the colour itself is not touched.
describe('StatusDot', () => {
  it('draws the colour as given, inside a hairline ring', () => {
    const { container } = render(<StatusDot color="#1e3a8a" />)
    const dot = container.firstElementChild as HTMLElement
    expect(dot.style.backgroundColor).toBe('rgb(30, 58, 138)')
    expect(dot.className).toContain('ring-1')
    expect(dot.className).toContain('ring-border-strong')
    expect(dot.getAttribute('aria-hidden')).toBe('true')
  })

  it('draws nothing coloured when the status has no colour', () => {
    const { container } = render(<StatusDot color={undefined} />)
    expect((container.firstElementChild as HTMLElement).style.backgroundColor).toBe('')
  })
})
