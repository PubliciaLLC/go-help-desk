import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AttachmentSizeLimitSection } from './AttachmentSizeLimitSection'

// #673: the largest file anyone may attach, 1 to 100 MB, 25 by default.

const field = () => screen.getByRole('spinbutton', { name: /largest file anyone may attach/i }) as HTMLInputElement
const HINT = /your virus scanner must accept files this large/i

function show(props: Partial<React.ComponentProps<typeof AttachmentSizeLimitSection>> = {}) {
  return render(
    <AttachmentSizeLimitSection onChange={() => {}} scanning={false} guestMB={5} {...props} />,
  )
}

describe('the file size section', () => {
  it('is titled File size and shows 25 when nothing is stored', () => {
    show()
    expect(screen.getByRole('heading', { name: 'File size' })).toBeTruthy()
    expect(field().value).toBe('25')
    expect([field().min, field().max]).toEqual(['1', '100'])
    expect(screen.getByText('1 to 100 MB. Default 25.')).toBeTruthy()
  })

  it.each([0, 101, -3, 2.5])('shows 25 for a stored %s', (v) => {
    show({ value: v })
    expect(field().value).toBe('25')
  })

  it.each([1, 10, 100])('shows a stored %s as stored', (v) => {
    show({ value: v })
    expect(field().value).toBe(String(v))
  })

  it('says it applies to everyone, and who is held to the lower figure', () => {
    show()
    expect(screen.getByText(
      'Applies to every upload: staff, requesters and guests. Guests are held to the lower of this and their own limit.',
    )).toBeTruthy()
  })

  it('reports a typed value', async () => {
    const onChange = vi.fn()
    show({ onChange })
    await userEvent.clear(field())
    await userEvent.type(field(), '40')
    expect(onChange).toHaveBeenLastCalledWith(40)
  })

  describe('the guest line', () => {
    it('shows the guest limit when it is the lower', () => {
      show({ value: undefined, guestMB: 5 })
      expect(screen.getByTestId('guest-limit-line').textContent).toBe('Guests: up to 5 MB.')
    })

    it('shows the overall limit, and says why, when it is the lower', () => {
      show({ value: 10, guestMB: 20 })
      expect(screen.getByTestId('guest-limit-line').textContent).toBe(
        'Guests: up to 10 MB. Their own limit is 20 MB; this one is lower, so it applies.',
      )
    })

    it('adds nothing when the two are equal', () => {
      show({ value: 10, guestMB: 10 })
      expect(screen.getByTestId('guest-limit-line').textContent).toBe('Guests: up to 10 MB.')
    })
  })

  describe('the scanner hint', () => {
    it.each([
      ['above 25 with scanning on', 30, true, true],
      ['at 25 with scanning on', 25, true, false],
      ['below 25 with scanning on', 10, true, false],
      ['unset with scanning on', undefined, true, false],
      ['above 25 with scanning off', 30, false, false],
    ])('%s', (_n, value, scanning, shown) => {
      show({ value, scanning })
      if (!shown) {
        expect(screen.queryByText(HINT)).toBeNull()
        return
      }
      const hint = screen.getByText(HINT)
      // A grey aside, not an amber warning: the stock scanner is fine.
      expect(hint.className).toContain('text-xs')
      expect(hint.className).toContain('text-gray-500')
      expect(hint.className).not.toMatch(/amber/)
      expect(hint.textContent).toMatch(/clamd 0\.105 and later accept up to 100 MB/)
      expect(hint.getAttribute('role')).toBeNull()
    })
  })
})
