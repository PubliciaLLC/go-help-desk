import { describe, expect, it } from 'vitest'
import { followRemovalLabel, isFollowAction } from './followAudit'

describe('followRemovalLabel', () => {
  it('names whose follow was removed, from the resolved names', () => {
    expect(
      followRemovalLabel({
        action: 'unfollowed',
        before: { assignee_user_id: 'u1' },
        names: { u1: { name: 'Sam Staff' } },
      }),
    ).toBe('Follow removed on reassignment (Sam Staff)')
  })

  it('falls back to the id when the person no longer resolves', () => {
    expect(followRemovalLabel({ action: 'unfollowed', before: { assignee_user_id: 'u-gone' } })).toBe(
      'Follow removed on reassignment (u-gone)',
    )
  })

  it('is null for an ordinary unfollow and for every other action', () => {
    expect(followRemovalLabel({ action: 'unfollowed' })).toBeNull()
    expect(followRemovalLabel({ action: 'unfollowed', before: {} })).toBeNull()
    expect(followRemovalLabel({ action: 'assigned', before: { assignee_user_id: 'u1' } })).toBeNull()
  })
})

describe('isFollowAction', () => {
  it('is true for exactly the two follow actions', () => {
    expect(isFollowAction('followed')).toBe(true)
    expect(isFollowAction('unfollowed')).toBe(true)
    expect(isFollowAction('assigned')).toBe(false)
  })
})
