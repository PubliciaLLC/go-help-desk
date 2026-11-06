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

export function isFollowAction(action: string): boolean {
  return action === 'followed' || action === 'unfollowed'
}
