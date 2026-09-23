import { plugin as shadcn } from '@shadcn/lint';
import tsParser from '@typescript-eslint/parser';

export default [
  {
    files: ['src/**/*.{js,jsx}'],
    languageOptions: {
      parser: tsParser,
      parserOptions: { ecmaFeatures: { jsx: true } },
    },
    plugins: { shadcn },
    rules: {
      'shadcn/no-restyle': ['error', { allow: ['layout'] }],
      'shadcn/no-raw-colors': 'error',
      'shadcn/no-arbitrary-values': 'error',
      'shadcn/no-inline-styles': 'error',
      'shadcn/no-unknown-classes': 'error',
      'shadcn/require-static-classes': 'error',
    },
  },
  {
    // Component definitions own their structural values, variants and the
    // one dynamic geometry a Progress indicator needs.
    files: ['src/components/ui/**'],
    rules: {
      'shadcn/no-restyle': 'off',
      'shadcn/no-arbitrary-values': 'off',
      'shadcn/no-inline-styles': 'off',
      'shadcn/require-static-classes': 'off',
    },
  },
];
