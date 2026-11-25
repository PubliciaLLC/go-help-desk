import { render, screen } from '@testing-library/react'
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { describe, expect, it } from 'vitest'
import { PUBLIC_PATHS, isPublicPath } from '@/lib/publicPaths'
import { router } from '@/router'
import { RootShell } from './RootShell'

// #709: the pages with no sidebar (sign-in, setup, the guest pages) have no
// footer to hold the appearance switch, so the root route puts one in the top
// right corner of exactly those pages.

async function renderAt(path: string) {
  const root = createRootRoute({ component: RootShell })
  const page = createRoute({ getParentRoute: () => root, path: '$', component: () => <p>the page</p> })
  const r = createRouter({
    routeTree: root.addChildren([page]),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  render(<RouterProvider router={r} />)
  await screen.findByText('the page')
}

describe('the root shell', () => {
  it('lists the pages that have no sidebar', () => {
    expect([...PUBLIC_PATHS].sort()).toEqual(
      ['/forgot-password', '/g', '/login', '/reset-password', '/setup', '/signup', '/submit', '/track', '/verify-email'].sort(),
    )
  })

  it.each([...PUBLIC_PATHS])('shows the switch on %s', async (path) => {
    await renderAt(path)
    expect(screen.getByRole('group', { name: 'Appearance' })).toBeTruthy()
    expect(screen.getAllByRole('radio')).toHaveLength(3)
  })

  it('shows it on a guest ticket address under /g/', async () => {
    await renderAt('/g/anything')
    expect(screen.getByRole('group', { name: 'Appearance' })).toBeTruthy()
  })

  it.each(['/dashboard', '/tickets', '/account', '/admin/users', '/gallery', '/login-help'])('does not show it on %s', async (path) => {
    await renderAt(path)
    expect(screen.queryByRole('group', { name: 'Appearance' })).toBeNull()
  })

  it('puts it in the top right corner of the page (absolute, so it scrolls away with it), above the content', async () => {
    await renderAt('/login')
    const corner = screen.getByRole('group', { name: 'Appearance' }).closest('.absolute')!
    expect(corner.className).toMatch(/\btop-3\b/)
    expect(corner.className).toMatch(/\bright-3\b/)
    expect(corner.className).toMatch(/\bz-30\b/)
  })
})

describe('isPublicPath', () => {
  it.each([
    ['/login', true],
    ['/login/', true],
    ['/g', true],
    ['/g/', true],
    ['/g/abc', true],
    ['/gx', false],
    ['/', false],
    ['/dashboard', false],
  ])('%s is %s', (path, want) => {
    expect(isPublicPath(path)).toBe(want)
  })
})

describe('the application router', () => {
  it('uses the root shell', () => {
    expect(router.routesById.__root__.options.component).toBe(RootShell)
  })
})
