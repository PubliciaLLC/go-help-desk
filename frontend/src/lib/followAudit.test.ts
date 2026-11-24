import { describe, expect, it } from 'vitest'
import { followEntryLabel, followRemovalLabel, isFollowAction } from './followAudit'

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

// #695: somebody else added or removed the follower, so the entry says whose
// follow it was, in follower_user_id.
describe('followEntryLabel', () => {
  const names = { b: { name: 'Bob Staff' } }

  it('says who was added, from the resolved names', () => {
    expect(
      followEntryLabel({ action: 'followed', after: { follower_user_id: 'b' }, names }),
    ).toBe('Added Bob Staff as a follower')
  })

  it('says who was removed', () => {
    expect(
      followEntryLabel({ action: 'unfollowed', before: { follower_user_id: 'b' }, names }),
    ).toBe('Removed Bob Staff as a follower')
  })

  it('falls back to the id when the person no longer resolves', () => {
    expect(followEntryLabel({ action: 'followed', after: { follower_user_id: 'u-gone' } })).toBe(
      'Added u-gone as a follower',
    )
  })

  it('still labels a reassignment removal', () => {
    expect(
      followEntryLabel({ action: 'unfollowed', before: { assignee_user_id: 'b' }, names }),
    ).toBe('Follow removed on reassignment (Bob Staff)')
  })

  it('is null for a person following or unfollowing for themselves, and for other actions', () => {
    expect(followEntryLabel({ action: 'followed', after: {} })).toBeNull()
    expect(followEntryLabel({ action: 'unfollowed' })).toBeNull()
    expect(followEntryLabel({ action: 'assigned', after: { follower_user_id: 'b' } })).toBeNull()
  })
})
