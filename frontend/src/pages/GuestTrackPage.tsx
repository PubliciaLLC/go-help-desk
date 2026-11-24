import { useState } from 'react'

import { resendGuestLink } from '@/api/guest'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

// Request a fresh link.
//
// The server answers the same way whether or not anything matched, so that this
// cannot be used to find out whether a ticket or an address exists. This page
// has to hold that line: it says a link has been sent *if the details match*,
// in those words, because it genuinely does not know whether they did.
export function GuestTrackPage() {
  const [trackingNumber, setTrackingNumber] = useState('')
  const [email, setEmail] = useState('')
  const [sent, setSent] = useState(false)
  const [loading, setLoading] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setLoading(true)
    try {
      await resendGuestLink(trackingNumber.trim(), email.trim())
    } finally {
      // Shown regardless, including on a network failure. A page that only
      // confirmed on success would answer the question the API refuses to.
      setSent(true)
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-gray-50 flex items-center justify-center p-4">
      <div className="max-w-md w-full">
        <Card>
          <CardHeader>
            <CardTitle className="text-lg">Find your ticket</CardTitle>
          </CardHeader>
          <CardContent>
            {sent ? (
              <div className="space-y-3 text-sm text-gray-700">
                <p>
                  If those details match an open ticket, we have emailed a new
                  link to that address.
                </p>
                <p className="text-gray-500">
                  Any link you were sent before this one has stopped working.
                </p>
              </div>
            ) : (
              <form onSubmit={submit} className="space-y-4">
                <p className="text-sm text-gray-600">
                  Enter your tracking number and the email address you used, and
                  we will send you a fresh link.
                </p>
                <div className="space-y-1">
                  <Label htmlFor="tn">Tracking number</Label>
                  <Input
                    id="tn"
                    value={trackingNumber}
                    onChange={(e) => setTrackingNumber(e.target.value.toUpperCase())}
                    placeholder="GHD-2026-000001"
                    required
                  />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="email">Email address</Label>
                  <Input
                    id="email"
                    type="email"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    required
                  />
                </div>
                <Button type="submit" className="w-full" disabled={loading}>
                  {loading ? 'Sending…' : 'Send me a link'}
                </Button>
              </form>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
