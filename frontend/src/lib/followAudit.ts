import type { AuditNames } from '@/api/types'

// An unfollowed entry written when a ticket is reassigned has the person who
// reassigned it as its actor, so "Stopped following by <admin>" would name the
// wrong person. It carries whose follow it was in before.assignee_user_id.
// Shared by the ticket's feed and the admin-wide page so they say the same.
//
// Returns the label for that one case, or null for any other entry, which keeps
// whatever label its view already gave it.
export function followRemovalLabel(e: {
  action: string
  before?: Record<string, unknown> | null
  names?: AuditNames
}): string | null {
  const who = e.action === 'unfollowed' ? e.before?.assignee_user_id : undefined
  if (typeof who !== 'string') return null
  return `Follow removed on reassignment (${e.names?.[who]?.name ?? who})`
}

// #695: somebody else added or removed the follower, so the entry's actor is
// the adder and the person it was about is in follower_user_id (after for an
// add, before for a removal). A person following for themselves has no such
// key and keeps the plain label.
function followOtherLabel(e: {
  action: string
  before?: Record<string, unknown> | null
  after?: Record<string, unknown> | null
  names?: AuditNames
}): string | null {
  const who = e.action === 'followed' ? e.after?.follower_user_id : e.action === 'unfollowed' ? e.before?.follower_user_id : undefined
  if (typeof who !== 'string') return null
  const name = e.names?.[who]?.name ?? who
  return e.action === 'followed' ? `Added ${name} as a follower` : `Removed ${name} as a follower`
}

// The label for any follow entry that says more than "started following", or
// null for one that does not. Shared by the ticket's feed and the admin page.
export function followEntryLabel(e: {
  action: string
  before?: Record<string, unknown> | null
  after?: Record<string, unknown> | null
  names?: AuditNames
}): string | null {
  return followRemovalLabel(e) ?? followOtherLabel(e)
}

export function isFollowAction(action: string): boolean {
  return action === 'followed' || action === 'unfollowed'
}
