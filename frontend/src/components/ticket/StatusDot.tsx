// The dot in front of a status name. Its colour is the one an administrator chose
// and is drawn as chosen, in both themes; a very dark choice would disappear on a
// dark card, so the dot has a hairline ring in a border colour instead of the
// colour being lightened (#709).
export function StatusDot({ color }: { color: string | undefined }) {
  return (
    <span
      aria-hidden="true"
      className="h-1.5 w-1.5 shrink-0 rounded-full ring-1 ring-border-strong"
      style={{ backgroundColor: color }}
    />
  )
}
