import { useState } from 'react'
import { CopyIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'

/** Shows a new API key's token, once, with a copy button. */
export function TokenBanner({ token, onDismiss }: { token: string; onDismiss: () => void }) {
  const [copied, setCopied] = useState(false)

  function copy() {
    navigator.clipboard.writeText(token)
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
        <Button size="sm" variant="outline" onClick={copy} className="shrink-0">
          <CopyIcon className="h-3.5 w-3.5 mr-1" />
          {copied ? 'Copied!' : 'Copy'}
        </Button>
      </div>
    </div>
  )
}
