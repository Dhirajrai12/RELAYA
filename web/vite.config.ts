import { fileURLToPath } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// In dev, /api is proxied to the backend so the browser sees one origin (no CORS).
// Set API_PROXY_TARGET=http://localhost:8080 to use a Go API running locally;
// it serves /v1 without the /api prefix that IIS adds, so the prefix is stripped.
const target = process.env.API_PROXY_TARGET ?? 'https://server.aegonassett.com'
const isLocal = target.startsWith('http://localhost') || target.startsWith('http://127.')

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    proxy: {
      '/api': {
        target,
        changeOrigin: true,
        secure: true,
        rewrite: isLocal ? (p) => p.replace(/^\/api/, '') : undefined,
      },
    },
  },
})
