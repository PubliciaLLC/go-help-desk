import { existsSync } from 'fs'
import { defineConfig, devices } from '@playwright/test'

// A pinned sandbox build of Chromium, used only where it actually exists.
// CI runs `playwright install` and finds its own browser at the normal
// cache path; this never overrides that, only fills in for a sandbox whose
// installed build doesn't match the version @playwright/test expects.
const sandboxChrome = '/opt/pw-browsers/chromium-1194/chrome-linux/chrome'
const executablePath = existsSync(sandboxChrome) ? sandboxChrome : undefined

// Layout-only tests: every backend call is mocked in the test itself (see
// e2e/mock.ts), so this never needs Postgres or the Go server — just the
// Vite dev server serving the built React app. Kept out of the vitest run
// (jsdom does not compute real layout, which is the entire thing being
// checked here: no CSS engine, no media queries, no actual box sizes).
export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: 'list',
  use: {
    baseURL: 'http://127.0.0.1:5183',
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        launchOptions: executablePath ? { executablePath } : {},
      },
    },
  ],
  webServer: {
    command: 'npm run dev -- --port 5183 --strictPort',
    url: 'http://127.0.0.1:5183',
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
  },
})
