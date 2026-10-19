import { useEffect, useImperativeHandle, useRef, useState } from 'react'
import type { ComponentType, Ref } from 'react'
import { Textarea } from '@/components/ui/textarea'
import type { MilkdownViewHandle, MilkdownViewProps } from './MilkdownView'

// #443: what the forms use. The Milkdown editor (~150 KB) is loaded with
// import() so only pages with a form pay for it. While it loads, if it fails to
// load or start, or if the writer chooses it, the existing Textarea is shown
// bound to the same value. The text is Markdown either way, and the form
// sends format "markdown" either way.

export interface MarkdownEditorHandle {
  /** Puts Markdown at the caret, replacing a selection, and leaves the caret after it. */
  insertAtCursor: (markdown: string) => void
}

export interface MarkdownEditorProps {
  ref?: Ref<MarkdownEditorHandle>
  value: string
  onChange: (markdown: string) => void
  /** Id of the element that labels the field; the form keeps its visible label. */
  labelId: string
  disabled?: boolean
  placeholder?: string
}

export function MarkdownEditor({ ref, value, onChange, labelId, disabled, placeholder }: MarkdownEditorProps) {
  const [View, setView] = useState<ComponentType<MilkdownViewProps> | null>(null)
  const [failed, setFailed] = useState(false)
  const [chosenPlain, setChosenPlain] = useState(false)

  useEffect(() => {
    let alive = true
    import('./MilkdownView')
      .then((m) => alive && setView(() => m.MilkdownView))
      .catch(() => alive && setFailed(true))
    return () => {
      alive = false
    }
  }, [])

  const showTextarea = failed || chosenPlain || !View
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const viewRef = useRef<MilkdownViewHandle>(null)

  useImperativeHandle(ref, () => ({
    insertAtCursor: (markdown: string) => {
      const ta = textareaRef.current
      if (!ta) {
        if (viewRef.current) viewRef.current.insertAtCursor(markdown)
        else onChange(value === '' ? markdown : `${value.replace(/\s+$/, '')}\n\n${markdown}`)
        return
      }
      // The plain editor: splice at the caret, as a textarea always has.
      const start = ta.selectionStart ?? ta.value.length
      const end = ta.selectionEnd ?? ta.value.length
      const next = ta.value.slice(0, start) + markdown + ta.value.slice(end)
      const caret = start + markdown.length
      onChange(next)
      requestAnimationFrame(() => {
        ta.focus()
        ta.setSelectionRange(caret, caret)
      })
    },
  }))

  return (
    <div className="min-w-0 space-y-1">
      {showTextarea || !View ? (
        <Textarea
          ref={textareaRef}
          aria-labelledby={labelId}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          placeholder={placeholder}
          rows={5}
        />
      ) : (
        <View
          ref={viewRef}
          value={value}
          onChange={onChange}
          labelId={labelId}
          disabled={disabled}
          placeholder={placeholder}
          onFail={() => setFailed(true)}
        />
      )}
      {!failed && (
        <button
          type="button"
          onClick={() => setChosenPlain((p) => !p)}
          className="inline-flex min-h-6 items-center py-1 text-xs text-gray-500 underline hover:text-gray-700"
        >
          {chosenPlain ? 'Switch to rich editor' : 'Switch to plain editor'}
        </button>
      )}
      {failed && (
        <p className="text-xs text-gray-500">The rich editor could not load. You can write Markdown here.</p>
      )}
    </div>
  )
}
