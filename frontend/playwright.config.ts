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
// Vite preview server serving the production build of the React app. Kept out
// of the vitest run (jsdom does not compute real layout, which is the entire thing being
// checked here: no CSS engine, no media queries, no actual box sizes).
// Port is overridable so parallel worktrees can each run their own server.
const port = Number(process.env.PW_PORT ?? 5183)
if (!Number.isInteger(port) || port < 1 || port > 65535) {
  throw new Error(`PW_PORT must be a port number (1-65535), got "${process.env.PW_PORT}"`)
}
const origin = `http://127.0.0.1:${port}`
const dev = process.env.PW_DEV === '1'
const outDir = `dist-e2e-${port}`

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  // No retries, here or on CI. A retry turns a flaky test into a green run that
  // nobody investigates; a test that fails once should fail the run. (CI used to
  // retry once with no recorded reason.)
  retries: 0,
  reporter: 'list',
  use: {
    baseURL: origin,
    // There is no retry to take a trace on, so keep the one from the failure itself.
    trace: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      testIgnore: /markdown-dos\.spec\.ts/,
      use: {
        ...devices['Desktop Chrome'],
        launchOptions: executablePath ? { executablePath } : {},
      },
    },
    {
      // The hostile-Markdown timing tests assert wall-clock bounds (the page keeps
      // answering within 1.5 s; a post settles within 8 s). They run after every
      // other test and one at a time, so another worker's CPU load cannot make
      // them fail, and the bounds stay as tight as they are.
      name: 'markdown-dos',
      testMatch: /markdown-dos\.spec\.ts/,
      dependencies: ['chromium'],
      fullyParallel: false,
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
    // The production build, not the dev server. Vite's dev server serves every
    // module separately, re-optimises dependencies and reloads the page mid-test
    // when it finds a new one, and drops its hot-reload socket under load (all seen
    // as flakes in markdown-dos.spec.ts); and what ships is the bundle, so the
    // bundle is what should be tested (it has no dev-only React checks, and a
    // worker is a hashed file, not a module the dev server transforms).
    // PW_DEV=1 opts back into the dev server, for a test being written.
    //
    // Built into its own directory per port: `dist` belongs to the CSP project
    // and to the backend embed, and two runs on different ports must not empty
    // each other's files. Skips `tsc -b`: the build job type-checks.
    // Binds 127.0.0.1 explicitly: on at least one CI runner `localhost` resolves
    // to a different address than the readiness poll below, and the job timed out.
    command: dev
      ? `npm run dev -- --host 127.0.0.1 --port ${port} --strictPort`
      : `npx vite build --outDir ${outDir} --emptyOutDir && npx vite preview --outDir ${outDir} --host 127.0.0.1 --port ${port} --strictPort`,
    url: origin,
    // Off by default: a fixed port plus reuse meant a run silently tested
    // whatever other checkout already held it. Set PW_REUSE=1 only when you
    // know the server on PW_PORT is this checkout's.
    reuseExistingServer: process.env.PW_REUSE === '1',
    // Covers the build when there is one (about a minute on a loaded runner).
    timeout: 180_000,
    stdout: 'pipe',
    stderr: 'pipe',
  },
})
