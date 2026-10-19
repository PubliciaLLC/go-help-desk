import { useEffect, useState } from 'react'
import type { ComponentType } from 'react'
import { Textarea } from '@/components/ui/textarea'
import type { MilkdownViewProps } from './MilkdownView'

// #443: what the forms use. The Milkdown editor (~150 KB) is loaded with
// import() so only pages with a form pay for it. While it loads, if it fails to
// load or start, or if the writer chooses it, the existing Textarea is shown
// bound to the same value. The text is Markdown either way, and the form
// sends format "markdown" either way.

export interface MarkdownEditorProps {
  value: string
  onChange: (markdown: string) => void
  /** Id of the element that labels the field; the form keeps its visible label. */
  labelId: string
  disabled?: boolean
  placeholder?: string
}

export function MarkdownEditor({ value, onChange, labelId, disabled, placeholder }: MarkdownEditorProps) {
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

  return (
    <div className="min-w-0 space-y-1">
      {showTextarea || !View ? (
        <Textarea
          aria-labelledby={labelId}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          placeholder={placeholder}
          rows={5}
        />
      ) : (
        <View
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
          className="text-xs text-gray-500 underline hover:text-gray-700"
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
