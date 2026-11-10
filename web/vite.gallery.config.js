import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { designSystemTokens, fontLicence } from './src/design-system/build/tokens-css.js';

// The design system element gallery (packet DS-1 section 8). It is not part of the product
// build: its own HTML entry, its own output directory, relative paths (base './') and no
// inlined assets, so that the fonts stay separate files under the panel's CSP.
const root = fileURLToPath(new URL('.', import.meta.url));

export default defineConfig({
  root,
  base: './',
  plugins: [react(), designSystemTokens(), fontLicence()],
  build: {
    outDir: 'dist-gallery',
    emptyOutDir: true,
    assetsInlineLimit: 0,
    rollupOptions: {
      input: fileURLToPath(new URL('./gallery.html', import.meta.url)),
    },
  },
});
