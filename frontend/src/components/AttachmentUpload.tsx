import { useRef } from 'react'
import { Button } from '@/components/ui/button'

const ACCEPTED = '.pdf,.docx,.xlsx,.txt,.log,.jpg,.jpeg,.png,.bmp'
const MAX_SIZE_BYTES = 25 * 1024 * 1024
const MB = 1024 * 1024

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
  /** Per-file limit; the instance-wide 25 MB unless the caller has a lower one (a guest, #445). */
  maxBytes?: number
  /** Key of a file in uploadStates. The file name unless the caller can have two of the same name. */
  /** Replaces the "up to N files" wording, for a caller whose limit has two parts. */
  maxFilesNote?: string
  stateKey?: (file: File, index: number) => string
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
    <span role="alert" className="text-xs text-red-600 shrink-0" title={state.error}>
      {state.error ?? 'Failed'}
    </span>
  )
  return <span className="text-xs text-gray-400 shrink-0">Queued</span>
}

export function AttachmentUpload({ files, onChange, uploadStates, disabled, maxFiles = 5, maxBytes = MAX_SIZE_BYTES, stateKey, maxFilesNote }: AttachmentUploadProps) {
  const inputRef = useRef<HTMLInputElement>(null)
  const isUploading = !!uploadStates
  const atLimit = files.length >= maxFiles

  function handleChange(fl: FileList | null) {
    if (!fl) return
    const remaining = maxFiles - files.length
    const toAdd: File[] = []
    for (const f of Array.from(fl)) {
      if (toAdd.length >= remaining) break
      if (f.size > maxBytes) {
        alert(`"${f.name}" exceeds the ${Math.floor(maxBytes / MB)} MB limit and was not added.`)
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
            : `PDF, DOCX, XLSX, TXT, LOG, JPEG, PNG, BMP \u00b7 max ${Math.floor(maxBytes / MB)}\u00a0MB \u00b7 ${maxFilesNote ?? `up to ${maxFiles} files`}`}
        </span>
      </div>

      {files.length > 0 && (
        <ul className="divide-y divide-gray-100 rounded-md border border-gray-200 text-sm">
          {files.map((f, i) => {
            const state = uploadStates?.[stateKey ? stateKey(f, i) : f.name]
            return (
              <li key={i} className="flex items-center gap-2 px-3 py-2">
                <span className="flex-1 truncate text-gray-800" title={f.name}>{f.name}</span>
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
