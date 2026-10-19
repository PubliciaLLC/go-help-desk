import { describe, expect, it } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { TicketText } from './TicketText'

// #443 security review round 2: things a link must not be allowed to do.

async function md(text: string) {
  const { container } = render(<TicketText text={text} format="markdown" />)
  await waitFor(() => expect(container.querySelector('[data-md-body]')).not.toBeNull(), { timeout: 5000 })
  return container
}

describe('mailto links', () => {
  it('show the domain in its xn-- form, like http hosts', async () => {
    const c = await md('[mail](mailto:boss@раураl.example)')
    expect(c.textContent).toMatch(/boss@xn--[a-z0-9-]+\.example/)
    expect(c.textContent).not.toMatch(/[Ѐ-ӿ]/)
  })

  it('treat a fullwidth dot as the dot it looks like', async () => {
    const c = await md('[mail](mailto:a@evil．example)')
    expect(c.textContent).toContain('a@evil.example')
  })

  it.each([
    ['bcc', '[x](mailto:a@b.example?bcc=spy@evil.example)'],
    ['cc', '[x](mailto:a@b.example?cc=spy@evil.example)'],
    ['subject', '[x](mailto:a@b.example?subject=hello)'],
    ['body', '[x](mailto:a@b.example?body=hello)'],
    ['an empty query', '[x](mailto:a@b.example?)'],
  ])('with %s are not linked at all', async (_n, text) => {
    const { container } = render(<TicketText text={text} format="markdown" />)
    await waitFor(() => expect(container.querySelector('[data-md-body]')).not.toBeNull(), { timeout: 5000 })
    expect(container.querySelector('a')).toBeNull()
    expect(container.textContent).toContain('x')
  })

  it('with several addresses show every one of them', async () => {
    const c = await md('[x](mailto:a@one.example,b@two.example)')
    expect(c.textContent).toContain('a@one.example')
    expect(c.textContent).toContain('b@two.example')
  })

  it('with a domain that is not a host are not linked', async () => {
    const { container } = render(<TicketText text="[x](mailto:a@)" format="markdown" />)
    await waitFor(() => expect(container.querySelector('[data-md-body]')).not.toBeNull(), { timeout: 5000 })
    expect(container.querySelector('a')).toBeNull()
  })
})

describe("links into the help desk's own attachment routes", () => {
  const own = () => window.location.origin
  it.each([
    ['a ticket attachment', () => `${own()}/api/v1/tickets/3f2a/attachments/9b1c`],
    ['the guest upload route', () => `${own()}/api/v1/guest/attachments`],
    ['an attachment with a query', () => `${own()}/api/v1/tickets/1/attachments/2?download=1`],
  ])('%s is not linked', async (_n, url) => {
    const { container } = render(<TicketText text={`[the logs](${url()})`} format="markdown" />)
    await waitFor(() => expect(container.querySelector('[data-md-body]')).not.toBeNull(), { timeout: 5000 })
    expect(container.querySelector('a')).toBeNull()
    expect(container.textContent).toContain('the logs')
    expect(container.textContent).toContain('(attachment: open it from the attachments list)')
  })

  it('a bare attachment URL is not linked either', async () => {
    const { container } = render(<TicketText text={`see ${own()}/api/v1/tickets/1/attachments/2 now`} format="markdown" />)
    await waitFor(() => expect(container.querySelector('[data-md-body]')).not.toBeNull(), { timeout: 5000 })
    expect(container.querySelector('a')).toBeNull()
    expect(container.textContent).toContain('attachment: open it from the attachments list')
  })

  it('another site with the same path is an ordinary link', async () => {
    const c = await md('[x](https://elsewhere.example/api/v1/tickets/1/attachments/2)')
    expect(c.querySelector('a')).not.toBeNull()
  })

  it('this site, any other path, is an ordinary link', async () => {
    const c = await md(`[x](${own()}/tickets/1)`)
    expect(c.querySelector('a')).not.toBeNull()
  })
})

describe('text too large to format', () => {
  it('over 50,000 characters is shown as plain text with a note, and can still be toggled', async () => {
    const { container } = render(<TicketText text={'word '.repeat(10001)} format="markdown" />)
    await screen.findByText(/too long to format/i)
    expect(container.querySelector('[data-md-body]')).toBeNull()
    expect(screen.getByRole('button', { name: /plain text/i })).toBeTruthy()
  })

  it('exactly at the limit is still formatted', async () => {
    const c = await md('a'.repeat(50000))
    expect(c.querySelector('p')).not.toBeNull()
  })

  it('a table with too many cells is shown as plain text', async () => {
    const wide = '|' + 'a|'.repeat(100) + '\n|' + '-|'.repeat(100) + '\n' + '|x\n'.repeat(60)
    const { container } = render(<TicketText text={wide} format="markdown" />)
    await screen.findByText(/too complex to format/i)
    expect(container.querySelector('table')).toBeNull()
  })

  it('a reasonable table is formatted', async () => {
    const c = await md('| a | b |\n|---|---|\n| 1 | 2 |')
    expect(c.querySelector('table')).not.toBeNull()
  })
})
