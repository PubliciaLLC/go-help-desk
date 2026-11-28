// The per-file upload limit, attachment_max_mb (#673): whole megabytes, 1 to
// 100, 25 when unset. The same rule the server's reader applies.

export const ATTACHMENT_MAX_MB_MIN = 1
export const ATTACHMENT_MAX_MB_MAX = 100
export const ATTACHMENT_MAX_MB_DEFAULT = 25

/** The overall limit as the server reads it: unset or out of range is 25. */
export function shownAttachmentMaxMB(v: unknown): number {
  return typeof v === 'number' && Number.isInteger(v) && v >= ATTACHMENT_MAX_MB_MIN && v <= ATTACHMENT_MAX_MB_MAX
    ? v
    : ATTACHMENT_MAX_MB_DEFAULT
}
