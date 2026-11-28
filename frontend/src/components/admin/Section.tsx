// A titled group of settings rows. Shared by the settings page and the
// components it is built from, so a section can be its own file (#673).
export function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-fg-faint">{title}</h2>
      <div className="divide-y rounded-lg border bg-card">{children}</div>
    </div>
  )
}
