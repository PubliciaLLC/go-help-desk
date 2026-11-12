import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConfirmDialog } from './confirm-dialog'

function renderDialog(props: Partial<React.ComponentProps<typeof ConfirmDialog>> = {}) {
  const onConfirm = vi.fn()
  render(<ConfirmDialog open onOpenChange={() => {}} title="Delete it?" onConfirm={onConfirm} {...props} />)
  return { onConfirm }
}

describe('ConfirmDialog', () => {
  it('shows a failure inside the open dialog (#418)', () => {
    renderDialog({ error: 'database unavailable' })
    expect(screen.getByRole('alert').textContent).toBe('database unavailable')
  })

  it('shows no alert when there is no error', () => {
    renderDialog()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('calls onConfirm from the confirm button', async () => {
    const { onConfirm } = renderDialog()
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
    expect(onConfirm).toHaveBeenCalledOnce()
  })
})
