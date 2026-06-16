import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

export default defineConfig({
  test: {
    environment: 'happy-dom',
    globals: true,
    setupFiles: ['./src/test-setup.ts'],
  },
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  optimizeDeps: {
    // The library dynamically imports ./lipsync-en.mjs, ./lipsync-fi.mjs etc.
    // relative to its own dist. Excluding it from prebundling lets Vite resolve
    // those at request time with the correct MIME type.
    exclude: ['@met4citizen/talkinghead'],
  },
  server: {
    proxy: {
      '/zarl.v1.ZarlService': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
      '/zarl.v1.AdminService': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
})
