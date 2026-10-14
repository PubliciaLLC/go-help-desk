import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getSettings, updateSettings, listGroups, listAssignableStaff } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

// The two fallbacks a new ticket gets when no group's scope covers it
// (auto_assign_group_id, auto_assign_user_ids). Rules 1 and 4 are listed so the
// order is visible; they are not settings (DESIGN.md -> Routing).
export function RoutingPage() {
  const qc = useQueryClient()
  const settings = useQuery({ queryKey: ['admin', 'settings'], queryFn: getSettings })
  const groups = useQuery({ queryKey: ['admin', 'groups'], queryFn: listGroups })
  const staff = useQuery({ queryKey: ['staff'], queryFn: listAssignableStaff })

  // null = untouched, so the form shows what is saved without copying it into
  // state in an effect.
  const [groupDraft, setGroupDraft] = useState<string | null>(null)
  const [usersDraft, setUsersDraft] = useState<string[] | null>(null)
  const [saved, setSaved] = useState(false)

  const save = useMutation({
    mutationFn: (v: { group: string; users: string[] }) =>
      updateSettings({ auto_assign_group_id: v.group, auto_assign_user_ids: v.users }),
    onSuccess: () => {
      setSaved(true)
      setGroupDraft(null)
      setUsersDraft(null)
      qc.invalidateQueries({ queryKey: ['admin', 'settings'] })
    },
  })

  if (settings.isLoading || groups.isLoading || staff.isLoading) {
    return <Layout><div className="flex justify-center py-12"><Spinner /></div></Layout>
  }

  const stored = settings.data ?? {}
  const storedGroup = typeof stored.auto_assign_group_id === 'string' ? stored.auto_assign_group_id : ''
  const storedUsers = Array.isArray(stored.auto_assign_user_ids)
    ? (stored.auto_assign_user_ids as unknown[]).filter((v): v is string => typeof v === 'string')
    : []

  const groupList = groups.data ?? []
  const everyone = staff.data ?? []
  const exists = new Set(everyone.map((p) => p.id))

  // A suspended person stays in the rotation on the server (they are skipped
  // while suspended and take their turn again when re-enabled), so one who is
  // already saved is shown, ticked and labelled, and sent back unchanged
  // unless the admin unticks them. /staff cannot tell "suspended" from "moved
  // to a reporting role" (the server refuses the latter, naming the person),
  // so the box must stay usable or that person could never be removed.
  const people = everyone.filter((p) => p.assignable || storedUsers.includes(p.id))

  // A saved id that names nobody (deleted) is refused by the server, so it is
  // left out of what is sent, and the admin is told.
  const gone = storedUsers.filter((id) => !exists.has(id)).length
  const group = groupDraft ?? (groupList.some((g) => g.id === storedGroup) ? storedGroup : '')
  const users = usersDraft ?? storedUsers.filter((id) => exists.has(id))

  const toggle = (id: string) => {
    setSaved(false)
    setUsersDraft(users.includes(id) ? users.filter((u) => u !== id) : [...users, id])
  }

  return (
    <Layout>
      <div className="max-w-2xl space-y-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Routing</h1>
          <p className="mt-1 text-sm text-gray-500">
            Where a new ticket goes. The first rule that matches wins, and routing runs once, when the ticket is created.
          </p>
        </div>

        <ol className="list-decimal space-y-1 pl-5 text-sm text-gray-700">
          <li>A group whose scope covers the ticket&apos;s category (set on the Groups page).</li>
          <li>The catch-all group below.</li>
          <li>The round-robin list below, dealing tickets out in turn.</li>
          <li>Otherwise the ticket stays unassigned.</li>
        </ol>

        <form
          className="space-y-6"
          onSubmit={(e) => {
            e.preventDefault()
            setSaved(false)
            save.mutate({ group, users })
          }}
        >
          <Card>
            <CardHeader><CardTitle className="text-base">Catch-all group</CardTitle></CardHeader>
            <CardContent className="space-y-2">
              <Label htmlFor="catch-all">Catch-all group</Label>
              <Select
                id="catch-all"
                value={group}
                onChange={(e) => { setSaved(false); setGroupDraft(e.target.value) }}
              >
                <option value="">None</option>
                {groupList.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
              </Select>
            </CardContent>
          </Card>

          <Card>
            <CardHeader><CardTitle className="text-base">Round-robin staff</CardTitle></CardHeader>
            <CardContent className="space-y-2">
              <p className="text-xs text-gray-500">
                Tickets are dealt out to the people ticked here, in the order they were ticked. The turn order is kept
                in memory, so it starts again when the server restarts. Someone who is suspended when their turn comes is skipped.
              </p>
              {gone > 0 && (
                <p className="text-xs text-amber-700">
                  {gone} saved {gone === 1 ? 'entry no longer exists' : 'entries no longer exist'} and will be dropped when you save.
                </p>
              )}
              {people.length === 0 && <p className="text-sm text-gray-400">No staff to choose from.</p>}
              <ul className="space-y-1">
                {people.map((p) => (
                  <li key={p.id}>
                    <label className="flex items-center gap-2 text-sm">
                      <input
                        type="checkbox"
                        checked={users.includes(p.id)}
                        onChange={() => toggle(p.id)}
                      />
                      {p.display_name}{!p.assignable && " (can't take work right now)"}
                    </label>
                  </li>
                ))}
              </ul>
            </CardContent>
          </Card>

          <div className="flex items-center gap-3">
            <Button type="submit" disabled={save.isPending}>{save.isPending ? 'Saving…' : 'Save'}</Button>
            {saved && <span className="text-sm text-green-700">Saved.</span>}
            {save.isError && <p role="alert" className="text-sm text-red-600">{extractError(save.error)}</p>}
          </div>
        </form>
      </div>
    </Layout>
  )
}
