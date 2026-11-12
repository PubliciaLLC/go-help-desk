import { useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react'
import type { Ref } from 'react'
import type { Editor } from '@milkdown/kit/core'
import { callCommand } from '@milkdown/kit/utils'
import {
  createCodeBlockCommand,
  toggleEmphasisCommand,
  toggleInlineCodeCommand,
  toggleStrongCommand,
  wrapInBlockquoteCommand,
  wrapInBulletListCommand,
  wrapInOrderedListCommand,
} from '@milkdown/kit/preset/commonmark'
import { Bold, Code, Italic, Link as LinkIcon, List, ListOrdered, Quote, SquareCode } from 'lucide-react'
import { safeUrl } from '@/lib/markdownAllowList'
import { cn } from '@/lib/utils'
import { addLink, createEditor, insertAtCursor, setMarkdown, viewOf } from './editorCore'

// The lazily loaded half of the editor: the Milkdown instance and its toolbar.
// MarkdownEditor.tsx (the adapter the forms use) imports this with import().

export interface MilkdownViewHandle {
  insertAtCursor: (markdown: string) => void
}

export interface MilkdownViewProps {
  ref?: Ref<MilkdownViewHandle>
  value: string
  onChange: (markdown: string) => void
  labelId: string
  disabled?: boolean
  placeholder?: string
  /** Called if the editor cannot start, so the adapter can fall back to a textarea. */
  onFail: () => void
}

type Active = Record<'strong' | 'em' | 'code' | 'link' | 'bullet' | 'ordered' | 'quote' | 'codeblock', boolean>
const noneActive: Active = { strong: false, em: false, code: false, link: false, bullet: false, ordered: false, quote: false, codeblock: false }

const normalise = (md: string) => md.replace(/\n+$/, '')

export function MilkdownView({ ref, value, onChange, labelId, disabled, placeholder, onFail }: MilkdownViewProps) {
  const rootRef = useRef<HTMLDivElement>(null)
  const editorRef = useRef<Editor | null>(null)
  const lastEmitted = useRef(value)
  const disabledRef = useRef(!!disabled)
  const onChangeRef = useRef(onChange)
  const onFailRef = useRef(onFail)
  const [active, setActive] = useState<Active>(noneActive)
  const [empty, setEmpty] = useState(value.trim() === '')
  const [linkOpen, setLinkOpen] = useState(false)
  const [linkUrl, setLinkUrl] = useState('')
  const [linkError, setLinkError] = useState('')

  useEffect(() => {
    onChangeRef.current = onChange
    onFailRef.current = onFail
  })

  useImperativeHandle(ref, () => ({
    insertAtCursor: (markdown: string) => {
      const editor = editorRef.current
      if (!editor || disabledRef.current) return
      insertAtCursor(editor, markdown)
      viewOf(editor).focus()
    },
  }))

  const readActive = useCallback(() => {
    const editor = editorRef.current
    if (!editor) return
    const view = viewOf(editor)
    const { state } = view
    const { from, to, empty: collapsed, $from } = state.selection
    const hasMark = (name: string) => {
      const type = state.schema.marks[name]
      if (!type) return false
      return collapsed
        ? !!type.isInSet(state.storedMarks ?? $from.marks())
        : state.doc.rangeHasMark(from, to, type)
    }
    const inside = (name: string) => {
      for (let d = $from.depth; d > 0; d--) if ($from.node(d).type.name === name) return true
      return false
    }
    setActive({
      strong: hasMark('strong'),
      em: hasMark('emphasis'),
      code: hasMark('inlineCode'),
      link: hasMark('link'),
      bullet: inside('bullet_list'),
      ordered: inside('ordered_list'),
      quote: inside('blockquote'),
      codeblock: inside('code_block'),
    })
    setEmpty(state.doc.textContent === '' && state.doc.childCount <= 1)
  }, [])

  useEffect(() => {
    let cancelled = false
    const root = rootRef.current
    if (!root) return
    createEditor({
      root,
      initial: value,
      labelId,
      placeholder,
      editable: () => !disabledRef.current,
      onMarkdown: (md) => {
        const out = normalise(md)
        lastEmitted.current = out
        onChangeRef.current(out)
      },
      onSelection: readActive,
    })
      .then((editor) => {
        if (cancelled) return void editor.destroy()
        editorRef.current = editor
        readActive()
      })
      .catch(() => {
        if (!cancelled) onFailRef.current()
      })
    return () => {
      cancelled = true
      const editor = editorRef.current
      editorRef.current = null
      void editor?.destroy()
    }
    // The editor is created once. Later changes to value, disabled and so on
    // are applied by the effects below; recreating it would lose the cursor.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // A value that did not come from the editor (the form was reset, a canned
  // response was chosen) replaces the document.
  useEffect(() => {
    const editor = editorRef.current
    if (!editor || value === lastEmitted.current) return
    lastEmitted.current = value
    setMarkdown(editor, value)
    setEmpty(value.trim() === '')
  }, [value])

  useEffect(() => {
    disabledRef.current = !!disabled
    const editor = editorRef.current
    if (editor) {
      const view = viewOf(editor)
      view.dispatch(view.state.tr.setMeta('addToHistory', false)) // re-reads editable()
    }
  }, [disabled])

  const run = (command: Parameters<typeof callCommand>[0], payload?: unknown) => {
    const editor = editorRef.current
    if (!editor || disabled) return
    editor.action(callCommand(command as never, payload as never))
    viewOf(editor).focus()
  }

  const submitLink = () => {
    const editor = editorRef.current
    if (!editor) return
    const href = safeUrl(linkUrl.trim())
    if (!href) {
      setLinkError('Enter a full web or mail address: https://..., http://... or mailto:...')
      return
    }
    addLink(editor, href)
    setLinkOpen(false)
    setLinkUrl('')
    setLinkError('')
    viewOf(editor).focus()
  }

  const buttons: { label: string; icon: typeof Bold; pressed: boolean; onClick: () => void }[] = [
    { label: 'Bold', icon: Bold, pressed: active.strong, onClick: () => run(toggleStrongCommand.key) },
    { label: 'Italic', icon: Italic, pressed: active.em, onClick: () => run(toggleEmphasisCommand.key) },
    { label: 'Inline code', icon: Code, pressed: active.code, onClick: () => run(toggleInlineCodeCommand.key) },
    { label: 'Link', icon: LinkIcon, pressed: active.link || linkOpen, onClick: () => setLinkOpen((o) => !o) },
    { label: 'Bulleted list', icon: List, pressed: active.bullet, onClick: () => run(wrapInBulletListCommand.key) },
    { label: 'Numbered list', icon: ListOrdered, pressed: active.ordered, onClick: () => run(wrapInOrderedListCommand.key) },
    { label: 'Quote', icon: Quote, pressed: active.quote, onClick: () => run(wrapInBlockquoteCommand.key) },
    { label: 'Code block', icon: SquareCode, pressed: active.codeblock, onClick: () => run(createCodeBlockCommand.key) },
  ]

  return (
    <div className="min-w-0 rounded-md border border-input bg-background focus-within:ring-2 focus-within:ring-ring">
      <div role="toolbar" aria-label="Formatting" className="flex flex-wrap gap-1 border-b p-1">
        {buttons.map(({ label, icon: Icon, pressed, onClick }) => (
          <button
            key={label}
            type="button"
            aria-label={label}
            title={label}
            aria-pressed={pressed}
            disabled={disabled}
            // Keep the selection in the editor while the button is used.
            onMouseDown={(e) => e.preventDefault()}
            onClick={onClick}
            className={cn(
              'inline-flex h-8 w-8 items-center justify-center rounded text-gray-700 hover:bg-gray-100 focus-visible:outline focus-visible:outline-2 disabled:opacity-50',
              pressed && 'bg-gray-200',
            )}
          >
            <Icon className="h-4 w-4" aria-hidden="true" />
          </button>
        ))}
      </div>
      {linkOpen && (
        <div role="group" aria-label="Add link" className="flex flex-wrap items-center gap-2 border-b p-2 text-sm">
          <input
            type="text"
            inputMode="url"
            autoFocus
            aria-label="Link address"
            aria-invalid={linkError ? true : undefined}
            aria-describedby={linkError ? `${labelId}-linkerr` : undefined}
            value={linkUrl}
            onChange={(e) => setLinkUrl(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                submitLink()
              }
              if (e.key === 'Escape') setLinkOpen(false)
            }}
            placeholder="https://"
            className="min-w-0 flex-1 rounded border border-input px-2 py-1"
          />
          <button type="button" onClick={submitLink} className="min-h-6 rounded border px-2 py-1">
            Add
          </button>
          <button type="button" onClick={() => setLinkOpen(false)} className="min-h-6 rounded border px-2 py-1">
            Cancel
          </button>
          {linkError && (
            <p id={`${labelId}-linkerr`} role="alert" className="w-full text-xs text-red-600">
              {linkError}
            </p>
          )}
        </div>
      )}
      <div className="relative">
        {empty && placeholder && (
          <span aria-hidden="true" className="pointer-events-none absolute left-3 top-2 text-sm text-muted-foreground">
            {placeholder}
          </span>
        )}
        <div ref={rootRef} className="md-editor min-w-0 px-3 py-2 text-sm" />
      </div>
    </div>
  )
}
