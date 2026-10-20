import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { setupAdmin } from '@/api/setup'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { UPDATE_CHECK_LABEL } from '@/lib/updateCheck'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

export function SetupPage() {
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  // Pre-filled, not blank: the point is that nobody has to think about it.
  const [category, setCategory] = useState('General')
  const [updateCheck, setUpdateCheck] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    if (password !== confirm) {
      setError('Passwords do not match')
      return
    }
    setLoading(true)
    try {
      // Only a ticked box is sent: absent means off to the server.
      await setupAdmin(email, displayName, password, category, ...(updateCheck ? [true as const] : []))
      navigate({ to: '/login' })
    } catch (err) {
      setError(extractError(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-xl">Welcome to Go Help Desk</CardTitle>
          <p className="text-sm text-gray-500 mt-1">
            Create your administrator account and name your first category.
          </p>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-1">
              <Label htmlFor="display-name">Full name</Label>
              <Input
                id="display-name"
                type="text"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                required
                autoComplete="name"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="email">Email</Label>
              <Input
                id="email"
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
                autoComplete="email"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="password">Password</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                autoComplete="new-password"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="confirm">Confirm password</Label>
              <Input
                id="confirm"
                type="password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                required
                autoComplete="new-password"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="category">First category</Label>
              <Input
                id="category"
                type="text"
                value={category}
                onChange={(e) => setCategory(e.target.value)}
                required
              />
              <p className="text-xs text-gray-500">
                Tickets are filed under a category. You can rename this one or add more later.
              </p>
            </div>
            <div className="flex items-start gap-2">
              <input
                id="update-check"
                type="checkbox"
                className="mt-0.5 h-4 w-4 shrink-0 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                checked={updateCheck}
                onChange={(e) => setUpdateCheck(e.target.checked)}
              />
              <div className="space-y-1">
                <Label htmlFor="update-check" className="font-normal">{UPDATE_CHECK_LABEL}</Label>
                <p className="text-xs text-gray-500">
                  Off unless you tick it. You can change it later under Settings.
                </p>
              </div>
            </div>
            {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
            <Button type="submit" className="w-full" disabled={loading}>
              {loading ? 'Creating account…' : 'Create admin account'}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
