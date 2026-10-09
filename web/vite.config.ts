import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    // In dev, the Go server runs on :8080 and serves the API.
    proxy: { '/api': 'http://localhost:8080' },
  },
})
