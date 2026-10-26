import { describe, expect, it } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { renderNow } from '@/test/formatted'
import userEvent from '@testing-library/user-event'
import { TicketText } from './TicketText'

describe('TicketText: plain', () => {
  it('renders exactly the stored text in a pre-wrap paragraph, with no links', () => {
    const { container } = render(<TicketText text={'line 1\nsee https://a.example'} format="plain" />)
    const p = container.querySelector('p')!
    expect(p.className).toContain('whitespace-pre-wrap')
    expect(p.className).toContain('wrap-anywhere')
    expect(p.textContent).toBe('line 1\nsee https://a.example')
    expect(container.querySelector('a')).toBeNull()
  })

  it('"Show formatted" is off by default, then shows the rendered form, and back', async () => {
    const user = userEvent.setup()
    const { container } = render(<TicketText text={'**bold** and [l](https://a.example)'} format="plain" />)
    const btn = screen.getByRole('button', { name: /show formatted/i })
    expect(btn.getAttribute('aria-pressed')).toBe('false')
    expect(container.querySelector('strong')).toBeNull()

    await user.click(btn)
    await act(async () => {})
    expect(btn.getAttribute('aria-pressed')).toBe('true')
    expect(container.querySelector('strong')!.textContent).toBe('bold')
    expect(container.querySelector('a')!.getAttribute('href')).toBe('https://a.example')

    await user.click(btn)
    expect(btn.getAttribute('aria-pressed')).toBe('false')
    expect(container.querySelector('strong')).toBeNull()
  })

  it('the toggle is reachable and operable by keyboard', async () => {
    const user = userEvent.setup()
    const { container } = render(<TicketText text="*x*" format="plain" />)
    await user.tab()
    const btn = screen.getByRole('button', { name: /show formatted/i })
    expect(document.activeElement).toBe(btn)
    await user.keyboard('{Enter}')
    await act(async () => {})
    expect(container.querySelector('em')).not.toBeNull()
    await user.keyboard(' ')
    expect(container.querySelector('em')).toBeNull()
  })
})

describe('TicketText: markdown', () => {
  it('renders formatted by default, with a Plain text toggle that shows the stored source', async () => {
    const user = userEvent.setup()
    const { container } = await renderNow(<TicketText text={'**b** <server01>'} format="markdown" />)
    expect(container.querySelector('strong')!.textContent).toBe('b')
    const btn = screen.getByRole('button', { name: /plain text/i })
    expect(btn.getAttribute('aria-pressed')).toBe('false')

    await user.click(btn)
    expect(btn.getAttribute('aria-pressed')).toBe('true')
    expect(container.querySelector('strong')).toBeNull()
    expect(container.querySelector('p')!.textContent).toBe('**b** <server01>')
  })

  it('shows the real host next to a link whose text hides it', async () => {
    const { container } = await renderNow(<TicketText text="[Reset password](https://evil.example/x)" format="markdown" />)
    expect(container.textContent).toContain('Reset password')
    expect(container.textContent).toContain('(evil.example)')
  })

  it('shows no host for a bare URL, whose text is the address', async () => {
    const { container } = await renderNow(<TicketText text="see https://a.example/x now" format="markdown" />)
    expect(container.querySelector('a')!.getAttribute('href')).toBe('https://a.example/x')
    expect(container.textContent).not.toContain('(a.example)')
  })

  it('shows a look-alike domain in its xn-- spelling', async () => {
    const { container } = await renderNow(<TicketText text="[paypal](https://раураl.example/)" format="markdown" />)
    expect(container.textContent).toMatch(/\(xn--[a-z0-9-]+\.example\)/)
  })

  it('shows the address for a mailto link', async () => {
    const { container } = await renderNow(<TicketText text="[mail me](mailto:a@example.com)" format="markdown" />)
    expect(container.querySelector('a')!.getAttribute('href')).toBe('mailto:a@example.com')
    expect(container.textContent).toContain('a@example.com')
  })

  it('code and tables scroll inside their own box and are keyboard focusable', async () => {
    const { container } = await renderNow(
      <TicketText text={'```\nlong\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |'} format="markdown" />,
    )
    const pre = container.querySelector('pre')!
    expect(pre.className).toContain('overflow-x-auto')
    expect(pre.getAttribute('tabindex')).toBe('0')
    const wrap = container.querySelector('table')!.parentElement!
    expect(wrap.className).toContain('overflow-x-auto')
    expect(wrap.getAttribute('tabindex')).toBe('0')
  })

  it('an ordered list keeps its start number', async () => {
    const { container } = await renderNow(<TicketText text={'3. c\n4. d'} format="markdown" />)
    expect(container.querySelector('ol')!.getAttribute('start')).toBe('3')
  })

  it('wraps long prose', async () => {
    const { container } = await renderNow(<TicketText text="x" format="markdown" />)
    expect(container.querySelector('p')!.className).toContain('wrap-anywhere')
  })

  it('the toggle does not submit a surrounding form', () => {
    let submitted = false
    render(
      <form onSubmit={(e) => { e.preventDefault(); submitted = true }}>
        <TicketText text="x" format="markdown" />
      </form>,
    )
    fireEvent.click(screen.getByRole('button', { name: /plain text/i }))
    expect(submitted).toBe(false)
  })
})
