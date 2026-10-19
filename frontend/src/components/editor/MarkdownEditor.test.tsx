import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { MarkdownEditor } from './MarkdownEditor'

function Form({ onSubmit }: { onSubmit: (v: string) => void }) {
  const [v, setV] = useState('')
  return (
    <form onSubmit={(e) => { e.preventDefault(); onSubmit(v) }}>
      <span id="lbl">Description</span>
      <MarkdownEditor value={v} onChange={setV} labelId="lbl" />
      <button type="submit">Send</button>
    </form>
  )
}

afterEach(() => vi.resetModules())

describe('MarkdownEditor', () => {
  it('loads the rich editor and labels it', async () => {
    render(<Form onSubmit={() => {}} />)
    await waitFor(() => expect(document.querySelector('[contenteditable="true"]')).not.toBeNull())
    const box = screen.getByRole('textbox', { name: 'Description' })
    expect(box.getAttribute('contenteditable')).toBe('true')
    expect(box.getAttribute('aria-multiline')).toBe('true')
    expect(screen.getByRole('toolbar', { name: 'Formatting' })).toBeTruthy()
  })

  it('toolbar toggles carry aria-pressed and cover the eight actions', async () => {
    render(<Form onSubmit={() => {}} />)
    await screen.findByRole('toolbar')
    const names = ['Bold', 'Italic', 'Inline code', 'Link', 'Bulleted list', 'Numbered list', 'Quote', 'Code block']
    for (const n of names) {
      expect(screen.getByRole('button', { name: n }).getAttribute('aria-pressed')).toBe('false')
    }
  })

  it('the link button refuses an address outside http, https and mailto', async () => {
    const user = userEvent.setup()
    render(<Form onSubmit={() => {}} />)
    await screen.findByRole('toolbar')
    await user.click(screen.getByRole('button', { name: 'Link' }))
    await user.type(screen.getByRole('textbox', { name: 'Link address' }), 'javascript:alert(1){Enter}')
    expect(screen.getByRole('alert').textContent).toMatch(/full web or mail address/)
    expect(document.querySelector('.md-editor a')).toBeNull()
  })

  it('the link button adds a link for a good address', async () => {
    const user = userEvent.setup()
    render(<Form onSubmit={() => {}} />)
    await screen.findByRole('toolbar')
    await user.click(screen.getByRole('button', { name: 'Link' }))
    await user.type(screen.getByRole('textbox', { name: 'Link address' }), 'https://a.example{Enter}')
    await waitFor(() => expect(document.querySelector('.md-editor a')?.getAttribute('href')).toBe('https://a.example'))
  })

  it('a form submitted straight after typing carries the latest text', async () => {
    const submitted: string[] = []
    render(<Form onSubmit={(v) => submitted.push(v)} />)
    await screen.findByRole('toolbar')
    // Switch to the plain editor: same value, same form.
    await userEvent.click(screen.getByRole('button', { name: /switch to plain editor/i }))
    const ta = screen.getByRole('textbox', { name: 'Description' })
    fireEvent.change(ta, { target: { value: '**now**' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send' }))
    expect(submitted).toEqual(['**now**'])
  })

  it('can switch to the plain editor and back, keeping the text', async () => {
    const user = userEvent.setup()
    render(<Form onSubmit={() => {}} />)
    await screen.findByRole('toolbar')
    await user.click(screen.getByRole('button', { name: /switch to plain editor/i }))
    const ta = screen.getByRole('textbox', { name: 'Description' }) as HTMLTextAreaElement
    expect(ta.tagName).toBe('TEXTAREA')
    fireEvent.change(ta, { target: { value: 'hello' } })
    await user.click(screen.getByRole('button', { name: /switch to rich editor/i }))
    await waitFor(() => expect(document.querySelector('.md-editor')?.textContent).toContain('hello'))
  })
})

describe('MarkdownEditor when the editor cannot load', () => {
  it('shows the textarea, and the form still submits the text', async () => {
    vi.doMock('./MilkdownView', () => {
      throw new Error('chunk failed to load')
    })
    const { MarkdownEditor: Fresh } = await import('./MarkdownEditor')
    const submitted: string[] = []
    function F() {
      const [v, setV] = useState('')
      return (
        <form onSubmit={(e) => { e.preventDefault(); submitted.push(v) }}>
          <span id="lbl">Description</span>
          <Fresh value={v} onChange={setV} labelId="lbl" />
          <button type="submit">Send</button>
        </form>
      )
    }
    render(<F />)
    await screen.findByText(/rich editor could not load/i)
    const ta = screen.getByRole('textbox', { name: 'Description' })
    expect(ta.tagName).toBe('TEXTAREA')
    fireEvent.change(ta, { target: { value: 'typed *here*' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send' }))
    expect(submitted).toEqual(['typed *here*'])
    vi.doUnmock('./MilkdownView')
  })
})
