import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ExpiryField } from './ExpiryField'

describe('ExpiryField', () => {
  it('is a labelled date field that reports what was typed', async () => {
    const onChange = vi.fn()
    render(<ExpiryField value="" onChange={onChange} maxYears={-1} />)
    await userEvent.type(screen.getByLabelText(/expires/i), '2099-03-04')
    expect(onChange).toHaveBeenLastCalledWith('2099-03-04')
  })

  it('with no limit, says that empty means never', () => {
    render(<ExpiryField value="" onChange={() => {}} maxYears={-1} />)
    expect(screen.getByText(/never expires/i)).toBeTruthy()
    expect((screen.getByLabelText(/expires/i) as HTMLInputElement).required).toBe(false)
  })

  it('with a limit, is required, capped, and says so', () => {
    render(<ExpiryField value="2028-10-16" onChange={() => {}} maxYears={2} />)
    const input = screen.getByLabelText(/expires/i) as HTMLInputElement
    expect(input.required).toBe(true)
    expect(input.max).not.toBe('')
    expect(screen.getByText(/at most 2 years/i)).toBeTruthy()
    expect(screen.queryByText(/never expires/i)).toBeNull()
  })

  it('says a day past the limit is too late', () => {
    render(<ExpiryField value="2099-01-01" onChange={() => {}} maxYears={2} />)
    expect(screen.getByRole('alert').textContent).toMatch(/later than the 2 year limit/i)
  })

  it('can be disabled while a key is being created', () => {
    render(<ExpiryField value="" onChange={() => {}} maxYears={2} disabled />)
    expect((screen.getByLabelText(/expires/i) as HTMLInputElement).disabled).toBe(true)
  })
})
