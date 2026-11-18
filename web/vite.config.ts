import { writeFileSync } from 'node:fs'
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The Go binary embeds web/dist. Vite empties that directory on every build, so this
// plugin writes back the tracked placeholder that keeps `go build` working before the
// frontend has ever been built.
const keepDistPlaceholder = {
  name: 'keep-dist-placeholder',
  closeBundle() {
    writeFileSync(path.resolve(import.meta.dirname, 'dist/.gitkeep'), '')
  },
}

// In development, Vite serves the UI and proxies /api to the Go binary on :8080.
export default defineConfig({
  plugins: [react(), tailwindcss(), keepDistPlaceholder],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
