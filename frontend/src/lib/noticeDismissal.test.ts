import { beforeEach, describe, expect, it } from 'vitest'
import { clearDismissedNotices, noticeKey, readDismissed, storeDismissal } from './noticeDismissal'

beforeEach(() => sessionStorage.clear())

describe('noticeKey', () => {
  it.each([
    [{ id: 'a', severity: 'failure', signature: '1' }, { id: 'a', severity: 'failure', signature: '2' }],
    [{ id: 'a', severity: 'failure', signature: '1' }, { id: 'a', severity: 'config', signature: '1' }],
    [{ id: 'a', severity: 'failure', signature: '1' }, { id: 'b', severity: 'failure', signature: '1' }],
  ])('differs when id, severity or signature differs', (x, y) => {
    expect(noticeKey(x)).not.toBe(noticeKey(y))
  })
})

describe('dismissal storage', () => {
  it('stores, reads and clears only its own keys', () => {
    sessionStorage.setItem('unrelated', 'x')
    storeDismissal('a|failure|1')
    expect(readDismissed().has('a|failure|1')).toBe(true)
    clearDismissedNotices()
    expect(readDismissed().size).toBe(0)
    expect(sessionStorage.getItem('unrelated')).toBe('x')
  })
})
