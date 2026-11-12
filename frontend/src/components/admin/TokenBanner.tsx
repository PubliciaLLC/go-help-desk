import { useState } from 'react'
import { CopyIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'

/** Shows a new API key's token, once, with a copy button. */
export function TokenBanner({ token, onDismiss }: { token: string; onDismiss: () => void }) {
  const [copied, setCopied] = useState(false)
  const [failed, setFailed] = useState(false)

  // The token is shown once, so "Copied!" must mean it was: the write is awaited,
  // and a refusal (permission denied, an insecure page, no clipboard at all) is
  // said out loud rather than leaving someone to dismiss the only copy they have.
  async function copy() {
    setCopied(false)
    setFailed(false)
    try {
      await navigator.clipboard.writeText(token)
    } catch {
      setFailed(true)
      return
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="rounded-lg border border-green-200 bg-green-50 p-4 space-y-3">
      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="text-sm font-medium text-green-800">API key created — copy it now</p>
          <p className="text-xs text-green-700 mt-0.5">This token will not be shown again.</p>
        </div>
        <button onClick={onDismiss} className="text-green-600 hover:text-green-800 text-xs">Dismiss</button>
      </div>
      <div className="flex items-center gap-2">
        <code className="flex-1 rounded border border-green-200 bg-white px-3 py-2 font-mono text-xs text-gray-800 break-all">
          {token}
        </code>
        <Button size="sm" variant="outline" onClick={() => void copy()} className="shrink-0">
          <CopyIcon className="h-3.5 w-3.5 mr-1" />
          {copied ? 'Copied!' : 'Copy'}
        </Button>
      </div>
      {failed && (
        <p role="alert" className="text-xs text-red-700">
          Could not copy. Select the token above and copy it by hand before you dismiss this.
        </p>
      )}
    </div>
  )
}
