import { afterEach, describe, expect, it } from 'vitest'
import { renderNow } from '@/test/formatted'
import type { Editor } from '@milkdown/kit/core'
import { DOMParser as PMDOMParser, DOMSerializer } from '@milkdown/kit/prose/model'
import { TextSelection } from '@milkdown/kit/prose/state'
import { addLink, createEditor, insertAtCursor, markdownOf, setMarkdown, viewOf } from './editorCore'
import { EDITOR_MARKS, EDITOR_NODES } from '@/lib/markdownAllowList'
import { TicketText } from '@/components/TicketText'

// #443: the editor is built from the same allow-list as the renderer. These
// tests run the real Milkdown/ProseMirror in jsdom; what jsdom cannot do (a
// browser fetching an image) is covered by e2e/markdown-editor.spec.ts.

let editor: Editor | null = null
let root: HTMLElement

async function start(initial = '') {
  root = document.createElement('div')
  document.body.appendChild(root)
  editor = await createEditor({
    root,
    initial,
    labelId: 'lbl',
    editable: () => true,
    onMarkdown: () => {},
    onSelection: () => {},
  })
  return viewOf(editor)
}

afterEach(async () => {
  await editor?.destroy()
  editor = null
  root?.remove()
})

const keydown = (init: KeyboardEventInit) => new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init })

// Whether ProseMirror's key handling consumed the event. Consumed means the
// browser would not move focus, which is what a keyboard trap is.
function consumed(view: ReturnType<typeof viewOf>, init: KeyboardEventInit): boolean {
  const ev = keydown(init)
  return Boolean(view.someProp('handleKeyDown', (f) => f(view, ev)))
}

function placeCursorIn(view: ReturnType<typeof viewOf>, text: string) {
  let pos = -1
  view.state.doc.descendants((node, p) => {
    if (pos < 0 && node.isText && node.text?.includes(text)) pos = p + 1
  })
  expect(pos, `"${text}" not found`).toBeGreaterThan(-1)
  view.dispatch(view.state.tr.setSelection(TextSelection.create(view.state.doc, pos)))
}

describe('the editor schema is the allow-list', () => {
  it('its nodes and marks are exactly the ones the shared constant derives', async () => {
    const view = await start()
    expect(Object.keys(view.state.schema.nodes).sort()).toEqual([...EDITOR_NODES].sort())
    expect(Object.keys(view.state.schema.marks).sort()).toEqual([...EDITOR_MARKS].sort())
  })

  it('has no task-list, footnote or column-resizing parts', async () => {
    const view = await start()
    const names = [...Object.keys(view.state.schema.nodes), ...Object.keys(view.state.schema.marks)].join(' ')
    expect(names).not.toMatch(/footnote|task|resiz/i)
    expect(view.state.schema.nodes.list_item.spec.attrs).not.toHaveProperty('checked')
  })
})

describe('images are never live elements', () => {
  it('a pasted <img> is dropped by the schema', async () => {
    const view = await start()
    const holder = document.createElement('div')
    holder.innerHTML = '<p>before <img src="//t.example/x.gif" alt="a"> after</p>'
    const doc = PMDOMParser.fromSchema(view.state.schema).parse(holder)
    let images = 0
    doc.descendants((n) => {
      if (n.type.name === 'image') images++
    })
    expect(images).toBe(0)
  })

  it('an image node serialises to a span reading "[image: alt]", never an img', async () => {
    const view = await start()
    const img = view.state.schema.nodes.image.create({ src: 'https://t.example/x.gif', alt: 'a cat' })
    const dom = DOMSerializer.fromSchema(view.state.schema).serializeNode(img) as HTMLElement
    expect(dom.tagName).toBe('SPAN')
    expect(dom.textContent).toBe('[image: a cat]')
    expect(dom.querySelector('img')).toBeNull()
  })

  it('Markdown with an image shows no img in the live document', async () => {
    await start()
    setMarkdown(editor!, 'x ![a cat](https://t.example/x.gif) y')
    expect(root.querySelector('img')).toBeNull()
    expect(root.querySelector('.md-image')!.textContent).toBe('[image: a cat]')
  })
})

describe('raw HTML is text', () => {
  it('is shown literally and saved unchanged', async () => {
    await start()
    setMarkdown(editor!, 'a <script>alert(1)</script> and <server01>')
    expect(root.querySelector('script, server01')).toBeNull()
    expect(root.textContent).toContain('<script>alert(1)</script>')
    expect(markdownOf(editor!)).toContain('<server01>')
  })
})

describe('input the editor has no node for does not throw', () => {
  it.each([
    ['footnote', 'text[^1]\n\n[^1]: the note'],
    ['task list', '- [x] done\n- [ ] todo'],
  ])('%s', async (_name, md) => {
    await start()
    expect(() => setMarkdown(editor!, md)).not.toThrow()
    expect(root.textContent).toContain(_name === 'task list' ? 'done' : 'text')
  })
})

describe('Tab always leaves the editor (WCAG 2.1.2)', () => {
  it.each([
    ['a paragraph', 'plain text'],
    ['a list item', '- one\n- two'],
    ['a nested list item', '- one\n  - nested'],
    ['an ordered list item', '1. one\n2. two'],
    ['a table cell', '| a | b |\n|---|---|\n| 1 | 2 |'],
    ['a code block', '```\ncode\n```'],
  ])('%s', async (_name, md) => {
    const view = await start()
    setMarkdown(editor!, md)
    placeCursorIn(view, md.includes('two') ? 'two' : md.includes('nested') ? 'nested' : md.includes('1') && md.includes('|') ? '1' : md.includes('code') ? 'code' : md.includes('one') ? 'one' : 'plain')
    expect(consumed(view, { key: 'Tab' })).toBe(false)
    expect(consumed(view, { key: 'Tab', shiftKey: true })).toBe(false)
  })

  it('Mod-] still indents a list item, so nothing is lost', async () => {
    const view = await start()
    setMarkdown(editor!, '- one\n- two')
    placeCursorIn(view, 'two')
    expect(consumed(view, { key: ']', ctrlKey: true, metaKey: false })).toBe(true)
    expect(markdownOf(editor!)).toMatch(/- one\n {2}- two|\* one\n {2}\* two/)
  })
})

// Literal strings: the user means exactly these characters. They are placed
// in a paragraph as text (no input rule fires, as when a rule is undone or
// the text is escaped), saved, and shown through the real renderer. The
// rendered text must be the typed text. Markdown characters that would
// otherwise take effect have to be escaped on the way out.
const literal = [
  '1. not a list',
  '# not heading',
  '- dash item',
  '* star item',
  '+ plus item',
  '> not a quote',
  '*not emphasis*',
  '_not emphasis either_',
  '**not strong**',
  '~~not struck~~',
  '`not code`',
  'https://example.com/a_b_c/file_name_here.txt',
  'C:\\temp\\new\\file',
  'back\\slash and \\*escaped\\*',
  'Server <server01> is down',
  '<b>not bold</b>',
  '[not a link](https://example.com)',
  'a | b | c',
  'price is $5 & tax < 10 > 2',
  'ends with two spaces  ',
  '    four spaces in',
  '---',
  '1) paren list',
  'email me at a_b@example.com',
]

describe('round trip: what is typed literally is what is shown', () => {
  it.each(literal)('%j', async (text) => {
    const view = await start()
    // The paragraph's text becomes the document, with no input rule involved.
    view.dispatch(view.state.tr.insertText(text, 1))
    const saved = markdownOf(editor!).replace(/\n+$/, '')
    const { container } = await renderNow(<TicketText text={saved} format="markdown" />)
    const body = container.querySelector('[data-md-body]')!
    expect(body.textContent).toBe(text.replace(/ +$/, ''))
    // And it is one paragraph of text: nothing became a list, heading or quote.
    expect(body.querySelectorAll('ul, ol, h4, blockquote, hr, pre, table')).toHaveLength(0)
  })

  it('loading the saved text back into the editor gives the same document', async () => {
    for (const text of literal) {
      const view = await start()
      view.dispatch(view.state.tr.insertText(text, 1))
      const saved = markdownOf(editor!)
      await editor!.destroy()
      root.remove()
      const again = await start(saved)
      // Trailing spaces are not significant in Markdown, so they are not kept.
      expect(again.state.doc.textContent, text).toBe(text.trimEnd())
    }
  })
})

describe('what the writer asks for is what is saved', () => {
  it('Markdown typed as a rule becomes structure, and is saved as Markdown', async () => {
    await start()
    setMarkdown(editor!, '# Title\n\n- a\n- b\n\n1. x\n2. y\n\n> q\n\n```bash\nls\n```\n\n**b** *i* `c` ~~s~~ [l](https://a.example)')
    const saved = markdownOf(editor!)
    expect(saved).toContain('# Title')
    expect(saved).toMatch(/[-*] a/)
    expect(saved).toContain('1. x')
    expect(saved).toContain('> q')
    expect(saved).toContain('```bash')
    expect(saved).toContain('**b**')
    expect(saved).toContain('~~s~~')
    expect(saved).toContain('[l](https://a.example)')
  })

  it('a fence language is kept as the info string and shown on the block', async () => {
    await start()
    setMarkdown(editor!, '```bash\nls\n```')
    expect(root.querySelector('pre')!.getAttribute('data-language')).toBe('bash')
    expect(markdownOf(editor!)).toContain('```bash')
  })
})

describe('accessibility', () => {
  it('the editable area is a labelled multi-line textbox', async () => {
    await start()
    const area = root.querySelector('[contenteditable="true"]')!
    expect(area.getAttribute('role')).toBe('textbox')
    expect(area.getAttribute('aria-multiline')).toBe('true')
    expect(area.getAttribute('aria-labelledby')).toBe('lbl')
  })
})

describe('inserting at the caret (a canned response)', () => {
  it('lands in the middle of existing text and leaves the caret after it', async () => {
    const view = await start()
    setMarkdown(editor!, 'hello world')
    placeCursorIn(view, 'hello world') // start of the text
    view.dispatch(view.state.tr.setSelection(TextSelection.create(view.state.doc, 1 + 5)))
    insertAtCursor(editor!, 'Hi **there**')
    expect(markdownOf(editor!).trim()).toBe('helloHi **there** world')
    // Caret: directly after the inserted text, before " world".
    const after = viewOf(editor!).state.selection
    expect(viewOf(editor!).state.doc.textBetween(after.from, after.to + 6)).toBe(' world')
  })

  it('replaces a selection', async () => {
    const view = await start()
    setMarkdown(editor!, 'hello big world')
    view.dispatch(view.state.tr.setSelection(TextSelection.create(view.state.doc, 1 + 5, 1 + 9)))
    insertAtCursor(editor!, 'X')
    expect(markdownOf(editor!).trim()).toBe('helloX world')
  })

  it('puts several paragraphs where the caret is', async () => {
    const view = await start()
    setMarkdown(editor!, 'start')
    view.dispatch(view.state.tr.setSelection(TextSelection.atEnd(view.state.doc)))
    insertAtCursor(editor!, 'one\n\ntwo')
    const out = markdownOf(editor!)
    expect(out).toContain('one')
    expect(out).toContain('two')
    expect(out.indexOf('start')).toBeLessThan(out.indexOf('two'))
  })
})

// The Link button with and without a selection. The caret must end up outside
// the link: otherwise the toolbar shows Link as active when nothing is selected
// (seen on an iPhone), and the next word typed would join the link.
describe('adding a link', () => {
  const marksAtCaret = (view: ReturnType<typeof viewOf>) =>
    view.state.selection.$from.marks().map((m) => m.type.name)
  const linkRanges = (view: ReturnType<typeof viewOf>) => {
    const out: { text: string; href: string }[] = []
    view.state.doc.descendants((n) => {
      const m = n.marks.find((x) => x.type.name === 'link')
      if (n.isText && m) out.push({ text: n.text!, href: m.attrs.href })
    })
    return out
  }

  it('with nothing selected after a word: a space, then the address as its own link, caret after it', async () => {
    const view = await start()
    setMarkdown(editor!, 'Link')
    view.dispatch(view.state.tr.setSelection(TextSelection.atEnd(view.state.doc)))
    addLink(editor!, 'https://www.google.com')
    expect(view.state.doc.textContent).toBe('Link https://www.google.com')
    expect(linkRanges(view)).toEqual([{ text: 'https://www.google.com', href: 'https://www.google.com' }])
    expect(marksAtCaret(view)).not.toContain('link')
    expect(view.state.selection.empty).toBe(true)
    expect(view.state.selection.from).toBe(1 + 'Link https://www.google.com'.length)
    // And the next thing typed is not part of the link.
    view.dispatch(view.state.tr.insertText(' next'))
    expect(linkRanges(view)).toEqual([{ text: 'https://www.google.com', href: 'https://www.google.com' }])
  })

  it('at the start of an empty paragraph there is no leading space', async () => {
    const view = await start()
    addLink(editor!, 'https://a.example')
    expect(view.state.doc.textContent).toBe('https://a.example')
    expect(marksAtCaret(view)).not.toContain('link')
  })

  it('after whitespace there is no second space', async () => {
    const view = await start()
    setMarkdown(editor!, 'see')
    view.dispatch(view.state.tr.setSelection(TextSelection.atEnd(view.state.doc)))
    view.dispatch(view.state.tr.insertText(' '))
    addLink(editor!, 'https://a.example')
    expect(view.state.doc.textContent).toBe('see https://a.example')
  })

  it('with text selected: the selection becomes the link and the caret moves after it', async () => {
    const view = await start()
    setMarkdown(editor!, 'go to the docs now')
    view.dispatch(view.state.tr.setSelection(TextSelection.create(view.state.doc, 1 + 10, 1 + 14)))
    addLink(editor!, 'https://a.example')
    expect(view.state.doc.textContent).toBe('go to the docs now')
    expect(linkRanges(view)).toEqual([{ text: 'docs', href: 'https://a.example' }])
    expect(view.state.selection.empty).toBe(true)
    expect(view.state.selection.from).toBe(1 + 14)
    expect(marksAtCaret(view)).not.toContain('link')
  })

  it('refuses an address that fails the link rule, whoever calls it', async () => {
    const view = await start()
    setMarkdown(editor!, 'Link')
    for (const bad of ['javascript:alert(1)', '/relative', 'mailto:a@b.example?bcc=x@evil.example', 'data:text/html,x']) {
      addLink(editor!, bad)
    }
    expect(view.state.doc.textContent).toBe('Link')
    expect(linkRanges(view)).toEqual([])
  })

  it('is saved as Markdown', async () => {
    const view = await start()
    setMarkdown(editor!, 'Link')
    view.dispatch(view.state.tr.setSelection(TextSelection.atEnd(view.state.doc)))
    addLink(editor!, 'https://www.google.com')
    expect(markdownOf(editor!).trim()).toBe('Link <https://www.google.com>')
  })
})
