import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath, URL } from 'node:url';

// API target is reached via:
//   - VITE_API_TARGET env (set when running in Docker network → http://logica-api:8080)
//   - else http://localhost:8080 (host machine running both)
const apiTarget = process.env['VITE_API_TARGET'] ?? 'http://localhost:8080';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    host: '0.0.0.0',
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: true,
      },
    },
  },
  build: {
    sourcemap: true,
    target: 'es2022',
  },
});
