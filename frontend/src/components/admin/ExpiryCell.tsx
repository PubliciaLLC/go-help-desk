/**
 * A key's expiry in a list. A key made before the limit existed, or under a
 * longer one, still works; it is marked so it can be rotated (#450).
 */
export function ExpiryCell({ expiresAt, beyondLimit }: { expiresAt?: string; beyondLimit?: boolean }) {
  if (!expiresAt) {
    return beyondLimit
      ? <span className="text-amber-700">No expiry (longer than the current limit)</span>
      : <>—</>
  }
  return (
    <>
      {new Date(expiresAt).toLocaleDateString()}
      {beyondLimit && <div className="text-xs text-amber-700">(longer than the current limit)</div>}
    </>
  )
}
