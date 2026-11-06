import { defineConfig, devices } from '@playwright/test'

// The production build, served with the backend's real CSP (scripts/csp-server.mjs).
// Run `npm run build` first; `npm run test:e2e:csp` does both.
export default defineConfig({
  testDir: './e2e-csp',
  reporter: 'list',
  use: { baseURL: 'http://127.0.0.1:5199' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: 'node scripts/csp-server.mjs dist 5199',
    url: 'http://127.0.0.1:5199',
    reuseExistingServer: false,
  },
})
