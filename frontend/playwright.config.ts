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
// Port is overridable so parallel worktrees can each run their own server.
const port = Number(process.env.PW_PORT ?? 5183)
if (!Number.isInteger(port) || port < 1 || port > 65535) {
  throw new Error(`PW_PORT must be a port number (1-65535), got "${process.env.PW_PORT}"`)
}
const origin = `http://127.0.0.1:${port}`

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: 'list',
  use: {
    baseURL: origin,
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
    // Safari's engine. Chromium and WebKit disagree on flex/grid min-width
    // and on where an unbroken string may wrap, and the CTO's phone is
    // WebKit: #479 and #522 were invisible here until this project existed.
    // Whole suite: it costs about as much as the chromium run. An `iPhone 13`
    // device project was tried and failed/passed on exactly the same tests as
    // this one (the specs set their own viewports), so it is not kept.
    {
      name: 'webkit',
      // Same as chromium: #542's timing tests must run alone, not beside this
      // project's workers. Harmless until that file exists.
      testIgnore: /markdown-dos\.spec\.ts/,
      use: { ...devices['Desktop Safari'] },
    },
  ],
  webServer: {
    // Vite binds to `localhost` by default, not 127.0.0.1 — on at least one
    // CI runner that is not the same address, so the readiness poll below
    // never saw it come up and the whole job timed out. Pinning the host
    // makes the bind address match what's being polled instead of relying on
    // however the runner resolves the difference.
    command: `npm run dev -- --host 127.0.0.1 --port ${port} --strictPort`,
    url: origin,
    // Off by default: a fixed port plus reuse meant a run silently tested
    // whatever other checkout already held it. Set PW_REUSE=1 only when you
    // know the server on PW_PORT is this checkout's.
    reuseExistingServer: process.env.PW_REUSE === '1',
    timeout: 60_000,
    stdout: 'pipe',
    stderr: 'pipe',
  },
})
