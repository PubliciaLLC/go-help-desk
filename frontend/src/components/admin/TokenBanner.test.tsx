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

  // The token is shown once. Saying "Copied!" before the write has succeeded,
  // or at all when it failed, lets someone dismiss the only copy they have.
  it('says Copied only once the write has succeeded', async () => {
    const user = userEvent.setup()
    let finish: () => void = () => {}
    vi.spyOn(navigator.clipboard, 'writeText').mockReturnValue(new Promise<void>((res) => { finish = res }))
    render(<TokenBanner token="GHD_abc" onDismiss={() => {}} />)
    await user.click(screen.getByRole('button', { name: /copy/i }))
    expect(screen.queryByText('Copied!')).toBeNull()
    finish()
    expect(await screen.findByText('Copied!')).toBeTruthy()
  })

  it('tells the person when the copy was refused, and does not say Copied', async () => {
    const user = userEvent.setup()
    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new DOMException('denied', 'NotAllowedError'))
    render(<TokenBanner token="GHD_abc" onDismiss={() => {}} />)
    await user.click(screen.getByRole('button', { name: /copy/i }))
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', expect.stringMatching(/could not copy/i))
    expect(screen.queryByText('Copied!')).toBeNull()
    expect(screen.getByText('GHD_abc')).toBeTruthy()
  })

  it('tells the person when there is no clipboard to write to', async () => {
    const user = userEvent.setup()
    vi.spyOn(navigator, 'clipboard', 'get').mockReturnValue(undefined as unknown as Clipboard)
    render(<TokenBanner token="GHD_abc" onDismiss={() => {}} />)
    await user.click(screen.getByRole('button', { name: /copy/i }))
    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(screen.queryByText('Copied!')).toBeNull()
  })

  it('is dismissed on request', async () => {
    const onDismiss = vi.fn()
    render(<TokenBanner token="GHD_abc" onDismiss={onDismiss} />)
    await userEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(onDismiss).toHaveBeenCalled()
  })
})
