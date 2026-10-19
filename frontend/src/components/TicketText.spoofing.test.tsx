import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { TicketText } from './TicketText'

// #443: a link's visible text can lie about where it goes. The rule: the real
// host is shown beside every link unless the visible text is exactly the URL
// being linked, is plain ASCII, and the URL carries no user:password@ part.
// The label is ordinary visible text, never hover-only and never CSS-hidden.

function view(md: string, path: 'markdown' | 'plain-toggled') {
  if (path === 'markdown') return render(<TicketText text={md} format="markdown" />).container
  const { container } = render(<TicketText text={md} format="plain" />)
  fireEvent.click(screen.getByRole('button', { name: /show formatted/i }))
  return container
}

// [name, markdown, the host label that must follow the link]
const mustLabel: [string, string, string][] = [
  ['text looks like another URL', '[https://bank.example](https://evil.example)', '(evil.example)'],
  ['text looks like another host', '[bank.example](https://evil.example)', '(evil.example)'],
  ['text equals the URL only after case folding', '[HTTPS://EVIL.EXAMPLE/](https://evil.example/)', '(evil.example)'],
  ['text equals the URL minus the trailing slash', '[https://evil.example](https://evil.example/)', '(evil.example)'],
  ['text says https, link is http', '[https://evil.example/x](http://evil.example/x)', '(evil.example)'],
  ['text drops www.', '[evil.example](https://www.evil.example)', '(www.evil.example)'],
  ['look-alike Unicode host in the link', '[paypal](https://раураl.example/)', 'xn--'],
  ['look-alike Unicode host as the visible text and the target', '[https://раураl.example/](https://раураl.example/)', 'xn--'],
  ['bare look-alike Unicode URL (autolink)', 'see https://раураl.example/login now', 'xn--'],
  ['right-to-left override in the text', '[‮https://bank.example](https://evil.example)', '(evil.example)'],
  ['zero-width characters in the text', '[https://bank​.example](https://evil.example)', '(evil.example)'],
  ['userinfo trick, as link text', '[https://bank.example@evil.example](https://bank.example@evil.example)', '(evil.example)'],
  ['userinfo trick, bare autolink', 'go to https://bank.example@evil.example/login', '(evil.example)'],
  ['mailto with different text', '[a@bank.example](mailto:a@evil.example)', 'a@evil.example'],
  ['bold around the link', '**[https://bank.example](https://evil.example)**', '(evil.example)'],
  ['link wrapped in emphasis inside the text', '[*https://bank.example*](https://evil.example)', '(evil.example)'],
  ['an image inside a link', '[![https://bank.example](https://t.example/p.gif)](https://evil.example)', '(evil.example)'],
  ['an image on its own', '![bank](https://t.example/p.gif)', '(t.example)'],
  ['a port is part of the host', '[https://bank.example](https://bank.example:8443)', '(bank.example:8443)'],
]

// The safe cases: nothing hides anything, so no label.
const noLabel: [string, string][] = [
  ['the exact URL', '[https://a.example/x](https://a.example/x)'],
  ['a bare URL', 'see https://a.example/x now'],
  ['a bare www. address', 'see www.a.example now'],
]

describe.each(['markdown', 'plain-toggled'] as const)('link spoofing (%s path)', (path) => {
  it.each(mustLabel)('%s', (_n, md, label) => {
    const c = view(md, path)
    expect(c.textContent).toContain(label)
    const a = c.querySelector('a')!
    // The label is a visible sibling of the link: plain text, no hiding classes.
    const labelEl = Array.from(c.querySelectorAll('span')).find((s) => s.textContent?.includes(label.replace(/[()]/g, '')))
    expect(labelEl, 'host label element').toBeTruthy()
    expect(labelEl!.className).not.toMatch(/\b(hidden|sr-only|invisible|opacity-0|max-sm:hidden|sm:hidden)\b/)
    expect(labelEl!.getAttribute('title')).toBeNull()
    expect(a.getAttribute('href')).toBeTruthy()
  })

  it.each(noLabel)('%s needs no label', (_n, md) => {
    const c = view(md, path)
    expect(c.querySelector('a')).not.toBeNull()
    expect(c.textContent).not.toMatch(/\(\S+\)/)
  })
})

describe('invisible and bidi characters in link text', () => {
  it('are removed from what is shown', () => {
    const c = view('[‮abc​⁦def⁩](https://evil.example)', 'markdown')
    expect(c.querySelector('a')!.textContent).toBe('abcdef')
  })

  it('the host label never contains them', () => {
    const c = view('[x](https://evil.example/‮path)', 'markdown')
    expect(c.textContent).toMatch(/\(evil\.example\)/)
    expect(c.textContent).not.toMatch(/[‪-‮⁦-⁩]/)
  })
})
