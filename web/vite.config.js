import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The Go server embeds the build output, and go:embed only reads files inside
// its own package directory, so the output goes to cmd/intuitik/dist.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../cmd/intuitik/dist',
    emptyOutDir: true,
  },
  test: {
    environment: 'node',
  },
});
