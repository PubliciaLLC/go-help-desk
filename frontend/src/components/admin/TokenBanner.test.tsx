import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TokenBanner } from './TokenBanner'

describe('TokenBanner', () => {
  it('shows the token and says it will not be shown again', () => {
    render(<TokenBanner token="GHD_abc" onDismiss={() => {}} />)
    expect(screen.getByText('GHD_abc')).toBeTruthy()
    expect(screen.getByText(/will not be shown again/i)).toBeTruthy()
  })

  it('copies the token to the clipboard', async () => {
    const user = userEvent.setup()
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
    render(<TokenBanner token="GHD_abc" onDismiss={() => {}} />)
    await user.click(screen.getByRole('button', { name: /copy/i }))
    expect(writeText).toHaveBeenCalledWith('GHD_abc')
  })

  it('is dismissed on request', async () => {
    const onDismiss = vi.fn()
    render(<TokenBanner token="GHD_abc" onDismiss={onDismiss} />)
    await userEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(onDismiss).toHaveBeenCalled()
  })
})
