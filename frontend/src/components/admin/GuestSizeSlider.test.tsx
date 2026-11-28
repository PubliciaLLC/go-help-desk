import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { GuestSizeSlider } from './GuestSizeSlider'

// #445, reshaped by #673: a guest's per-file maximum, 1 MB up to the per-file
// limit for every upload, 5 MB by default. The limit is the slider's top.

const slider = () => screen.getByRole('slider', { name: /largest file a guest may attach/i }) as HTMLInputElement

describe('the guest file size slider', () => {
  it('runs from 1 MB to the per-file limit in steps of 1 and labels its two ends', () => {
    const { rerender } = render(<GuestSizeSlider value={5} onChange={() => {}} instanceMaxMB={25} />)
    expect(slider().min).toBe('1')
    expect(slider().max).toBe('25')
    expect(slider().step).toBe('1')
    const ticks = screen.getByTestId('guest-size-ticks')
    expect(Array.from(ticks.children).map((c) => c.textContent)).toEqual(['1 MB', '25 MB'])
    rerender(<GuestSizeSlider value={5} onChange={() => {}} instanceMaxMB={100} />)
    expect(slider().max).toBe('100')
    expect(Array.from(ticks.children).map((c) => c.textContent)).toEqual(['1 MB', '100 MB'])
  })

  it('raises its top in the same render when the limit is raised', () => {
    const { rerender } = render(<GuestSizeSlider value={5} onChange={() => {}} instanceMaxMB={10} />)
    expect(slider().max).toBe('10')
    rerender(<GuestSizeSlider value={5} onChange={() => {}} instanceMaxMB={40} />)
    expect(slider().max).toBe('40')
  })

  it('reads its value to a screen reader in MB', () => {
    const { rerender } = render(<GuestSizeSlider value={5} onChange={() => {}} instanceMaxMB={25} />)
    expect(slider().value).toBe('5')
    expect(slider().getAttribute('aria-valuetext')).toBe('5 MB')
    rerender(<GuestSizeSlider value={1} onChange={() => {}} instanceMaxMB={25} />)
    expect(slider().getAttribute('aria-valuetext')).toBe('1 MB')
  })

  it('moves one megabyte per arrow key and reports it, and Home and End go to the ends', async () => {
    const onChange = vi.fn()
    render(<GuestSizeSlider value={5} onChange={onChange} instanceMaxMB={25} />)
    slider().focus()
    await userEvent.keyboard('{ArrowRight}')
    expect(onChange).toHaveBeenLastCalledWith(6)
    await userEvent.keyboard('{ArrowLeft}')
    expect(onChange).toHaveBeenLastCalledWith(4)
    await userEvent.keyboard('{End}')
    expect(onChange).toHaveBeenLastCalledWith(25)
    await userEvent.keyboard('{Home}')
    expect(onChange).toHaveBeenLastCalledWith(1)
  })

  it('never reports a value above the limit', async () => {
    const onChange = vi.fn()
    render(<GuestSizeSlider value={10} onChange={onChange} instanceMaxMB={10} />)
    slider().focus()
    await userEvent.keyboard('{ArrowRight}')
    expect(onChange).toHaveBeenLastCalledWith(10)
  })

  describe('a stored value above the limit', () => {
    it('sits at the limit and says what is saved and what applies', () => {
      render(<GuestSizeSlider value={20} onChange={() => {}} instanceMaxMB={10} />)
      expect(slider().value).toBe('10')
      const note = screen.getByRole('note')
      // No aria-label: it would replace the sentence, and its figures, for a screen reader.
      expect(note.getAttribute('aria-label')).toBeNull()
      expect(note.textContent).toBe(
        'Saved as 20 MB. Every upload is limited to 10 MB, so guests are held to 10 MB until you raise that limit or lower this one.',
      )
      expect(slider().getAttribute('aria-valuetext')).toBe('10 MB, saved as 20 MB, limited by the per-file limit')
      expect(slider().getAttribute('aria-describedby')).toBe(note.id)
    })

    it('has no note at the limit or below it', () => {
      const { rerender } = render(<GuestSizeSlider value={10} onChange={() => {}} instanceMaxMB={10} />)
      expect(screen.queryByRole('note')).toBeNull()
      rerender(<GuestSizeSlider value={9} onChange={() => {}} instanceMaxMB={10} />)
      expect(screen.queryByRole('note')).toBeNull()
    })

    it('reads an absent or out-of-range value as 5, not as the top of the old range', () => {
      const { rerender } = render(<GuestSizeSlider onChange={() => {}} instanceMaxMB={25} />)
      expect(slider().value).toBe('5')
      rerender(<GuestSizeSlider value={0} onChange={() => {}} instanceMaxMB={25} />)
      expect(slider().value).toBe('5')
      rerender(<GuestSizeSlider value={101} onChange={() => {}} instanceMaxMB={25} />)
      expect(slider().value).toBe('5')
    })
  })

  it('can be disabled', () => {
    render(<GuestSizeSlider value={5} onChange={() => {}} instanceMaxMB={25} disabled />)
    expect(slider().disabled).toBe(true)
  })
})
