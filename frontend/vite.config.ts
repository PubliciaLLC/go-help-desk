import path from 'path'
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
      // The Markdown parser runs in a Web Worker, which has no document. This
      // package's "browser" build decodes character references with one, so the
      // worker would die on load; its plain build has the same table in a string.
      'decode-named-character-reference': path.resolve(
        __dirname,
        './node_modules/decode-named-character-reference/index.js',
      ),
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/mcp': 'http://localhost:8080',
    },
  },
  test: {
    // jsdom has no Worker, so tests format in this thread (see markdownRunner.ts).
    alias: [{ find: /^@\/lib\/markdownBackend$/, replacement: path.resolve(__dirname, './src/lib/markdownBackend.inthread.ts') }],
    // jsdom, not node: the API client's 401 interceptor reads
    // window.location, so the code under test needs a document.
    environment: 'jsdom',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    restoreMocks: true,
    // Unmounts each test's DOM. Without it component tests leak into one
    // another — see src/test/setup.ts.
    setupFiles: ['./src/test/setup.ts'],
    // Must stay above asyncUtilTimeout (5000ms in setup.ts) so failing waits
    // show "Unable to find..." with a DOM dump, not "Test timed out".
    testTimeout: 15000,
  },
})
