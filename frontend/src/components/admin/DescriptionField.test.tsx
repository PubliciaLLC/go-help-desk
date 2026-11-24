import { describe, expect, it, vi } from 'vitest'
import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DescriptionField } from './DescriptionField'
import { isDescriptionTooLong } from '@/lib/ctiDescription'

function Harness({ initial = '', onValue }: { initial?: string; onValue?: (v: string) => void }) {
  const [v, setV] = useState(initial)
  return (
    <DescriptionField
      id="d"
      value={v}
      onChange={(x) => {
        setV(x)
        onValue?.(x)
      }}
    />
  )
}

describe('DescriptionField', () => {
  it('is an optional, labelled, multi-line field that says who can read it', () => {
    render(<Harness />)
    const box = screen.getByLabelText(/description/i)
    expect(box.tagName).toBe('TEXTAREA')
    expect(screen.getByText(/optional/i)).toBeDefined()
    expect(screen.getByText(/plain text/i)).toBeDefined()
    expect(screen.getByText(/guest form/i)).toBeDefined()
  })

  it('counts characters against the 500 limit', async () => {
    const user = userEvent.setup()
    render(<Harness />)
    expect(screen.getByText('0 / 500')).toBeDefined()
    await user.type(screen.getByLabelText(/description/i), 'abc')
    expect(screen.getByText('3 / 500')).toBeDefined()
  })

  it('counts characters, not UTF-16 units or bytes', () => {
    render(<Harness initial={'😀'.repeat(500)} />)
    expect(screen.getByText('500 / 500')).toBeDefined()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('warns, as an alert, when over the limit', () => {
    render(<Harness initial={'a'.repeat(501)} />)
    expect(screen.getByText('501 / 500')).toBeDefined()
    expect(screen.getByRole('alert').textContent).toMatch(/too long/i)
  })

  it('reports the typed value unchanged', async () => {
    const user = userEvent.setup()
    const seen = vi.fn()
    render(<Harness onValue={seen} />)
    await user.type(screen.getByLabelText(/description/i), 'a{Enter}b')
    expect(seen).toHaveBeenLastCalledWith('a\nb')
  })
})

describe('isDescriptionTooLong', () => {
  it.each([
    ['', false],
    ['a'.repeat(500), false],
    ['a'.repeat(501), true],
    ['😀'.repeat(500), false],
    ['😀'.repeat(501), true],
    ['é'.repeat(500), false],
    [' '.repeat(10) + 'a'.repeat(500), false], // the server trims first
  ])('%#', (value, want) => {
    expect(isDescriptionTooLong(value)).toBe(want)
  })
})
