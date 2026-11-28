import { describe, expect, it } from 'vitest'
import { isReplyAction, replyActionLabel, replyActionVerb } from './replyAudit'

describe('replyAudit', () => {
  it.each([
    ['reply_added', true, 'replied publicly', 'Reply added'],
    ['note_added', true, 'added an internal note', 'Internal note added'],
    ['closed', false, 'replied publicly', 'Reply added'],
  ])('%s', (action, is, verb, label) => {
    expect(isReplyAction(action)).toBe(is)
    expect(replyActionVerb(action)).toBe(verb)
    expect(replyActionLabel(action)).toBe(label)
  })
})
