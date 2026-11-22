import { writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// outDir is the directory the server embeds. It is emptied on every build, and
// its tracked .gitkeep is written back afterwards so a checkout that never built
// the frontend still compiles the server.
const outDir = fileURLToPath(new URL('../pkg/http/dist', import.meta.url));

export default defineConfig(({ mode }) => ({
  plugins: [
    svelte(),
    {
      name: 'keep-embed-placeholder',
      closeBundle() {
        writeFileSync(`${outDir}/.gitkeep`, '');
      },
    },
  ],
  server: {
    // The settings panels import the widget faces from plugins/, one level
    // above this package, so the dev server must be allowed to read there.
    fs: { allow: ['..'] },
    proxy: {
      '/api': 'http://localhost:8080',
      '/ws': { target: 'ws://localhost:8080', ws: true },
      '/widgets': 'http://localhost:8080',
      '/widget': 'http://localhost:8080',
      '/themes': 'http://localhost:8080',
      '/hook': 'http://localhost:8080',
    },
  },
  build: {
    outDir,
    emptyOutDir: true,
    // Source maps are two thirds of the bundle, and the bundle is compiled
    // into the server, so only a development build carries them.
    sourcemap: mode === 'development',
  },
}));
