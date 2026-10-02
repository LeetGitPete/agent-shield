import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      // The console calls the API under /api and the API has no prefix, so the
      // prefix is stripped here exactly as the nginx image does it:
      // /api/findings reaches the API as /findings.
      '/api': {
        target: 'http://localhost:8080',
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
    },
  },
  build: {
    // One page that needs all of its code for the first paint: the chart
    // library is most of the bundle and splitting it off would only add a
    // request. The default limit of 500 kB would warn on every build.
    chunkSizeWarningLimit: 700,
  },
  test: {
    environment: 'jsdom',
    setupFiles: './src/test-setup.ts',
    unstubGlobals: true,
  },
});
