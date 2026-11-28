import type { FieldType } from '@/api/types'

// The longest a custom field's value can be, in characters, by type (owner,
// 2026-11-11). The server holds every request to these; an administrator can set
// a lower one on a field (max_length), which the server reports as
// effective_max_length. They are repeated here only so the admin form can say
// what the type allows: nothing else reads them.
export const TEXT_CAP = 255
export const LONG_TEXT_CAP = 10_000

// Only a text or text area field has a limit of its own.
export function hasOwnLimit(type: FieldType): boolean {
  return type === 'text' || type === 'textarea'
}

export function typeCap(type: FieldType): number {
  return type === 'textarea' ? LONG_TEXT_CAP : TEXT_CAP
}

const fmt = (n: number) => n.toLocaleString('en-US')

export function maxLengthHint(type: FieldType): string {
  const cap = typeCap(type)
  return `Up to ${fmt(cap - 1)} characters. Leave empty for ${fmt(cap)}.`
}

export type MaxLengthInput = { ok: true; value: number | null } | { ok: false; message: string }

// What an administrator typed in the Maximum length box: empty is none, and
// anything else is a whole number from 1 to one below the type's cap, which is
// what the server accepts. Anything outside that would only be refused there.
export function parseMaxLength(text: string, type: FieldType): MaxLengthInput {
  const t = text.trim()
  if (t === '') return { ok: true, value: null }
  const cap = typeCap(type)
  if (/^\d+$/.test(t)) {
    const n = Number(t)
    if (n >= 1 && n < cap) return { ok: true, value: n }
  }
  return { ok: false, message: `The maximum length must be a whole number between 1 and ${fmt(cap - 1)}.` }
}

// Characters as the server counts them: code points, not UTF-16 units.
export function charCount(value: string): number {
  let n = 0
  for (const _ of value) n++ // eslint-disable-line @typescript-eslint/no-unused-vars
  return n
}

// The words for a value over its limit, or '' when it fits. The same limit the
// input's maxlength sets, but maxlength counts UTF-16 units and so stops a person
// writing emoji short; this is the exact check, run on submit.
export function overLimit(name: string, value: string, limit: number | undefined): string {
  if (!limit || charCount(value) <= limit) return ''
  return `${name} is too long: at most ${fmt(limit)} characters.`
}
