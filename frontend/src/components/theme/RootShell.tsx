import { Outlet, useRouterState } from '@tanstack/react-router'
import { isPublicPath } from '@/lib/publicPaths'
import { ThemeSwitch } from './ThemeSwitch'

// The root route's component (#709): the page, plus the appearance switch in the
// top right corner of every page that has no sidebar to hold it. Mounted once
// here so the nine page files do not each have to remember it.
export function RootShell() {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  return (
    <>
      <Outlet />
      {isPublicPath(pathname) && (
        <div className="fixed right-3 top-3 z-30">
          <ThemeSwitch variant="icons" />
        </div>
      )}
    </>
  )
}
