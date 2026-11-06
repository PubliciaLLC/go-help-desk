import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { GuestSizeSlider } from './GuestSizeSlider'

// #445: a guest's per-file maximum, 1 to 100 MB, 5 MB by default, with the
// instance-wide maximum (25 MB) winning where it is lower.

const slider = () => screen.getByRole('slider', { name: /largest file a guest may attach/i }) as HTMLInputElement

describe('the guest file size slider', () => {
  it('runs from 1 to 100 MB in steps of 1 and labels its ends', () => {
    render(<GuestSizeSlider value={5} onChange={() => {}} instanceMaxMB={25} />)
    expect(slider().min).toBe('1')
    expect(slider().max).toBe('100')
    expect(slider().step).toBe('1')
    const ticks = screen.getByTestId('guest-size-ticks')
    expect(Array.from(ticks.children).map((c) => c.textContent)).toEqual(['1 MB', '25 MB', '50 MB', '75 MB', '100 MB'])
  })

  it('reads its value to a screen reader in MB', () => {
    const { rerender } = render(<GuestSizeSlider value={5} onChange={() => {}} instanceMaxMB={25} />)
    expect(slider().value).toBe('5')
    expect(slider().getAttribute('aria-valuetext')).toBe('5 MB')
    rerender(<GuestSizeSlider value={1} onChange={() => {}} instanceMaxMB={25} />)
    expect(slider().getAttribute('aria-valuetext')).toBe('1 MB')
  })

  it('moves one megabyte per arrow key and reports it', async () => {
    const onChange = vi.fn()
    render(<GuestSizeSlider value={5} onChange={onChange} instanceMaxMB={25} />)
    slider().focus()
    await userEvent.keyboard('{ArrowRight}')
    expect(onChange).toHaveBeenLastCalledWith(6)
    await userEvent.keyboard('{ArrowLeft}')
    expect(onChange).toHaveBeenLastCalledWith(4)
    await userEvent.keyboard('{End}')
    expect(onChange).toHaveBeenLastCalledWith(100)
    await userEvent.keyboard('{Home}')
    expect(onChange).toHaveBeenLastCalledWith(1)
  })

  it('says so when the figure is above what the instance accepts, and announces the real limit', () => {
    const { rerender } = render(<GuestSizeSlider value={25} onChange={() => {}} instanceMaxMB={25} />)
    expect(screen.queryByRole('note')).toBeNull()
    rerender(<GuestSizeSlider value={60} onChange={() => {}} instanceMaxMB={25} />)
    const note = screen.getByRole('note')
    // No aria-label: it would replace the sentence, and its 25 MB, for a screen reader.
    expect(note.getAttribute('aria-label')).toBeNull()
    expect(note.textContent).toMatch(/25 MB/)
    expect(slider().getAttribute('aria-valuetext')).toBe('60 MB, limited to 25 MB by the instance-wide maximum')
  })

  it('can be disabled', () => {
    render(<GuestSizeSlider value={5} onChange={() => {}} instanceMaxMB={25} disabled />)
    expect(slider().disabled).toBe(true)
  })
})
