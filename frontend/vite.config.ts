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
  // Everything the Markdown renderer, its worker and the editor import. Left to
  // discovery, a cold dev server finds them only when the page first needs them,
  // re-optimizes, and reloads the page in the middle of a test.
  optimizeDeps: {
    include: [
      '@milkdown/kit/core',
      '@milkdown/kit/ctx',
      '@milkdown/kit/plugin/clipboard',
      '@milkdown/kit/plugin/history',
      '@milkdown/kit/preset/commonmark',
      '@milkdown/kit/preset/gfm',
      '@milkdown/kit/prose/model',
      '@milkdown/kit/prose/state',
      '@milkdown/kit/utils',
      'hast-util-to-jsx-runtime',
      'highlight.js/lib/languages/bash',
      'highlight.js/lib/languages/diff',
      'highlight.js/lib/languages/go',
      'highlight.js/lib/languages/ini',
      'highlight.js/lib/languages/javascript',
      'highlight.js/lib/languages/json',
      'highlight.js/lib/languages/plaintext',
      'highlight.js/lib/languages/powershell',
      'highlight.js/lib/languages/python',
      'highlight.js/lib/languages/sql',
      'highlight.js/lib/languages/typescript',
      'highlight.js/lib/languages/xml',
      'highlight.js/lib/languages/yaml',
      'lowlight',
      'rehype-sanitize',
      'remark-gfm',
      'remark-parse',
      'remark-rehype',
      'unified',
      'unist-util-visit',
    ],
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
