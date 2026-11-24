import { useRef } from 'react'
import { Button } from '@/components/ui/button'

const ACCEPTED = '.pdf,.docx,.xlsx,.txt,.log,.jpg,.jpeg,.png,.bmp'
const MAX_SIZE_BYTES = 25 * 1024 * 1024

export interface UploadState {
  status: 'pending' | 'uploading' | 'done' | 'error'
  error?: string
}

interface AttachmentUploadProps {
  files: File[]
  onChange: (files: File[]) => void
  uploadStates?: Record<string, UploadState>
  disabled?: boolean
  maxFiles?: number
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function statusIcon(state?: UploadState) {
  if (!state) return null
  if (state.status === 'uploading') return <span className="text-xs text-blue-600 shrink-0">Uploading…</span>
  if (state.status === 'done') return <span className="text-xs text-green-600 shrink-0">Done</span>
  if (state.status === 'error') return (
    <span className="text-xs text-red-600 shrink-0" title={state.error}>
      {state.error ?? 'Failed'}
    </span>
  )
  return <span className="text-xs text-gray-400 shrink-0">Queued</span>
}

export function AttachmentUpload({ files, onChange, uploadStates, disabled, maxFiles = 5 }: AttachmentUploadProps) {
  const inputRef = useRef<HTMLInputElement>(null)
  const isUploading = !!uploadStates
  const atLimit = files.length >= maxFiles

  function handleChange(fl: FileList | null) {
    if (!fl) return
    const remaining = maxFiles - files.length
    const toAdd: File[] = []
    for (const f of Array.from(fl)) {
      if (toAdd.length >= remaining) break
      if (f.size > MAX_SIZE_BYTES) {
        alert(`"${f.name}" exceeds the 25 MB limit and was not added.`)
        continue
      }
      toAdd.push(f)
    }
    // reset input so the same file can be re-selected after removal
    if (inputRef.current) inputRef.current.value = ''
    onChange([...files, ...toAdd])
  }

  function remove(index: number) {
    onChange(files.filter((_, i) => i !== index))
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-3">
        <input
          ref={inputRef}
          type="file"
          accept={ACCEPTED}
          multiple
          className="hidden"
          onChange={(e) => handleChange(e.target.files)}
          disabled={disabled || isUploading || atLimit}
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => inputRef.current?.click()}
          disabled={disabled || isUploading || atLimit}
        >
          Add files
        </Button>
        <span className="text-xs text-gray-500">
          {atLimit
            ? `${maxFiles} file limit reached`
            : `PDF, DOCX, XLSX, TXT, LOG, JPEG, PNG, BMP \u00b7 max 25\u00a0MB \u00b7 up to ${maxFiles} files`}
        </span>
      </div>

      {files.length > 0 && (
        <ul className="divide-y divide-gray-100 rounded-md border border-gray-200 text-sm">
          {files.map((f, i) => {
            const state = uploadStates?.[f.name]
            return (
              <li key={i} className="flex items-center gap-2 px-3 py-2">
                <span className="flex-1 truncate text-gray-800">{f.name}</span>
                <span className="shrink-0 text-xs text-gray-400">{formatBytes(f.size)}</span>
                {isUploading ? (
                  statusIcon(state)
                ) : (
                  <button
                    type="button"
                    className="shrink-0 text-xs text-gray-400 hover:text-red-600"
                    onClick={() => remove(i)}
                  >
                    ✕
                  </button>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
