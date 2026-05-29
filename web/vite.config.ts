import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath, URL } from 'node:url';

// API target is reached via:
//   - VITE_API_TARGET env (set when running in Docker network → http://logica-api:8080)
//   - else http://localhost:8080 (host machine running both)
const apiTarget = process.env['VITE_API_TARGET'] ?? 'http://localhost:8080';

// Agent service runs as a separate process on :8090. We proxy /api/agent
// (the path the agent itself registers under) → 8090 so the browser sees
// one origin. Resolution order: explicit VITE_AGENT_TARGET wins; otherwise in
// the Docker dev stack the agent is its own container (logica-agent:8090, not
// the API host), so swap host+port for that case; finally fall back to the
// host-mode convention of mirroring the api host with just the port swapped.
const agentTarget = process.env['VITE_AGENT_TARGET']
  ?? (apiTarget.includes('logica-api:8080')
        ? apiTarget.replace('logica-api:8080', 'logica-agent:8090')
        : apiTarget.replace(/:8080$/, ':8090'));

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
      // /api/agent → agent service, plain /api → ERP. Vite proxy uses prefix
      // matching with insertion order, so the longer prefix must come first.
      '/api/agent': {
        target: agentTarget,
        changeOrigin: true,
      },
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
