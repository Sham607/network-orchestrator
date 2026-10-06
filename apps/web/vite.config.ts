import { defineConfig } from 'vite'
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
      },
    },
  },
})
