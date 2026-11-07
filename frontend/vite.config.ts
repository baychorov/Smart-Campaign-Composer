import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

// In development the Go API runs separately; Vite forwards /api to it so the
// browser sees one origin (no CORS). SSE passes through the proxy unbuffered.
const apiTarget = process.env.VITE_API_TARGET ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: { '/api': { target: apiTarget, changeOrigin: false } },
  },
  test: {
    environment: 'happy-dom',
    include: ['src/**/*.test.ts'],
  },
})
