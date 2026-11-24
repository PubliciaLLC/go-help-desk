import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AttachmentUpload } from './AttachmentUpload'

// #673: the per-file limit is the caller's when it has one (the instance's
// attachment_max_bytes from /me), and the 25 MB default when it does not.

const MB = 1024 * 1024

// A File that claims a size without allocating it.
function fileOf(name: string, bytes: number): File {
  const f = new File(['x'], name, { type: 'application/pdf' })
  Object.defineProperty(f, 'size', { value: bytes })
  return f
}

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(window, 'alert').mockImplementation(() => {})
})

describe('the per-file limit', () => {
  it.each([
    { name: 'a caller limit of 30 MB refuses 31 MB', maxBytes: 30 * MB, size: 31 * MB, refused: true },
    { name: 'a caller limit of 30 MB accepts 26 MB', maxBytes: 30 * MB, size: 26 * MB, refused: false },
    { name: 'no caller limit refuses 26 MB at the 25 MB default', maxBytes: undefined, size: 26 * MB, refused: true },
    { name: 'no caller limit accepts 25 MB', maxBytes: undefined, size: 25 * MB, refused: false },
  ])('$name', async ({ maxBytes, size, refused }) => {
    const onChange = vi.fn()
    const { container } = render(
      <AttachmentUpload files={[]} onChange={onChange} maxBytes={maxBytes} />,
    )
    const input = container.querySelector('input[type="file"]') as HTMLInputElement
    await userEvent.upload(input, fileOf('big.pdf', size))

    if (refused) {
      expect(window.alert).toHaveBeenCalledTimes(1)
      expect(onChange).toHaveBeenCalledWith([])
    } else {
      expect(window.alert).not.toHaveBeenCalled()
      expect(onChange).toHaveBeenCalledWith([expect.objectContaining({ name: 'big.pdf' })])
    }
  })
})
