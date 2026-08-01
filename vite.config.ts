import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

/**
 * dist/ is a build artifact and is not tracked, except for dist/.gitkeep —
 * which exists so `//go:embed all:dist` still resolves in a source-only
 * checkout. Vite empties outDir on every build and would take .gitkeep with
 * it, leaving the Go build broken until someone noticed. Emit it as a build
 * output instead, so it comes back every time.
 */
function keepDistPlaceholder() {
  return {
    name: 'kb-keep-dist-placeholder',
    generateBundle(this: { emitFile: (f: Record<string, string>) => void }) {
      this.emitFile({ type: 'asset', fileName: '.gitkeep', source: '' });
    },
  };
}

export default defineConfig({
  plugins: [react(), keepDistPlaceholder()],
  test: {
    environment: 'node',
    include: ['src/**/*.test.{ts,tsx}'],
    setupFiles: ['src/test/setup.ts'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'json-summary', 'lcov'],
      reportsDirectory: 'coverage',
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        'src/**/*.test.{ts,tsx}',
        'src/**/*.d.ts',
        'src/test/**',
      ],
      thresholds: {
        branches: 95.07,
        functions: 98.71,
        lines: 99.42,
        statements: 98.20,
      },
    },
    // Vitest replaces CSS modules with an empty string by default, which also
    // empties a `?raw` import of the stylesheet — and would make the egress
    // guard's scan of src/styles.css silently vacuous.
    css: true,
  },
});
