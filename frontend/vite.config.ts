import path from 'path'
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/mcp': 'http://localhost:8080',
    },
  },
  test: {
    // jsdom, not node: the API client's 401 interceptor reads
    // window.location, so the code under test needs a document.
    environment: 'jsdom',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    restoreMocks: true,
    // Unmounts each test's DOM. Without it component tests leak into one
    // another — see src/test/setup.ts.
    setupFiles: ['./src/test/setup.ts'],
  },
})
