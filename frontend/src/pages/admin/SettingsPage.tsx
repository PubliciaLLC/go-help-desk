import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getSettings, updateSettings } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Spinner } from '@/components/ui/spinner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useState, useEffect } from 'react'

export function SettingsPage() {
  const qc = useQueryClient()
  const [fields, setFields] = useState<Record<string, string>>({})
  const [saveError, setSaveError] = useState('')
  const [saved, setSaved] = useState(false)

  const { data: settings, isLoading } = useQuery({
    queryKey: ['admin', 'settings'],
    queryFn: getSettings,
  })

  useEffect(() => {
    if (settings) {
      setFields(Object.fromEntries(Object.entries(settings).map(([k, v]) => [k, String(v)])))
    }
  }, [settings])

  const saveMutation = useMutation({
    mutationFn: () => {
      const patch: Record<string, unknown> = {}
      for (const [k, v] of Object.entries(fields)) {
        try { patch[k] = JSON.parse(v) } catch { patch[k] = v }
      }
      return updateSettings(patch)
    },
    onSuccess: () => {
      setSaved(true)
      setSaveError('')
      setTimeout(() => setSaved(false), 2000)
      qc.invalidateQueries({ queryKey: ['admin', 'settings'] })
    },
    onError: (err) => setSaveError(extractError(err)),
  })

  if (isLoading) return <Layout><div className="flex justify-center py-12"><Spinner /></div></Layout>

  return (
    <Layout>
      <div className="max-w-xl space-y-6">
        <h1 className="text-2xl font-bold text-gray-900">Settings</h1>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">System settings</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            {Object.entries(fields).map(([key, value]) => (
              <div key={key} className="space-y-1">
                <Label htmlFor={key} className="font-mono text-xs">{key}</Label>
                <Input
                  id={key}
                  value={value}
                  onChange={(e) => setFields((f) => ({ ...f, [key]: e.target.value }))}
                  className="font-mono text-sm"
                />
              </div>
            ))}

            {saveError && <p className="text-sm text-red-600">{saveError}</p>}
            {saved && <p className="text-sm text-green-600">Settings saved.</p>}

            <Button onClick={() => saveMutation.mutate()} disabled={saveMutation.isPending}>
              {saveMutation.isPending ? 'Saving…' : 'Save settings'}
            </Button>
          </CardContent>
        </Card>
      </div>
    </Layout>
  )
}
