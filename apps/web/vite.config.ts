import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// The SPA is served same-origin behind Caddy in production; in dev, /api is
// proxied to the Go API so cookies stay same-origin (ADR-002 §6).
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.VITE_API_TARGET ?? 'http://localhost:8080',
        changeOrigin: false,
        ws: true,
      },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    restoreMocks: true,
    // The default 5s is too tight for the first test in a file: it also pays
    // the module-transform and jsdom environment cost, which measured 6.6s on
    // a slow machine while every other test stayed under 1.4s. That made the
    // suite flaky (2 failures, then 1, then 0 across three identical runs), and
    // a flaky suite is a failing suite. This raises the ceiling only — it does
    // not weaken a single assertion.
    testTimeout: 15000,
  },
})
