import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ExpiryField } from './ExpiryField'

describe('ExpiryField', () => {
  it('is a labelled date field that reports what was typed', async () => {
    const onChange = vi.fn()
    render(<ExpiryField value="" onChange={onChange} />)
    await userEvent.type(screen.getByLabelText(/expires/i), '2099-03-04')
    expect(onChange).toHaveBeenLastCalledWith('2099-03-04')
  })

  it('says that empty means never', () => {
    render(<ExpiryField value="" onChange={() => {}} />)
    expect(screen.getByText(/never expires/i)).toBeTruthy()
  })

  it('can be disabled while a key is being created', () => {
    render(<ExpiryField value="" onChange={() => {}} disabled />)
    expect((screen.getByLabelText(/expires/i) as HTMLInputElement).disabled).toBe(true)
  })
})
