import { SunMoonIcon } from 'lucide-react'
import { ThemeSwitch } from '@/components/theme/ThemeSwitch'

// Your account -> Appearance (#709). The choice saves the moment it is made
// (there is no Save button) and belongs to the account, so every role has it,
// requesters included. The switch itself, its keyboard behaviour and its
// "Saved." / error line are ThemeSwitch's.
export function AppearanceSection() {
  return (
    <section className="rounded-lg border bg-card">
      <div className="flex items-start gap-3 border-b p-4">
        <div className="mt-0.5 text-fg-faint">
          <SunMoonIcon className="h-4 w-4" />
        </div>
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-fg">Appearance</h2>
          <p className="mt-0.5 text-xs text-fg-faint">Saved to your account, so it follows you to other devices.</p>
        </div>
      </div>
      <div className="p-4">
        <ThemeSwitch variant="labelled" />
      </div>
    </section>
  )
}
