// reply_added and note_added: a reply or an internal note was written. The
// entry holds no text, only who and when, so the views say it as a sentence.
export function isReplyAction(action: string): boolean {
  return action === 'reply_added' || action === 'note_added'
}

export function replyActionVerb(action: string): string {
  return action === 'note_added' ? 'added an internal note' : 'replied publicly'
}

// The admin-wide table's Action column.
export function replyActionLabel(action: string): string {
  return action === 'note_added' ? 'Internal note added' : 'Reply added'
}
