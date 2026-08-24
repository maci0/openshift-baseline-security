import tseslint from 'typescript-eslint';
import reactHooks from 'eslint-plugin-react-hooks';

export default tseslint.config(
  // A disable that silences nothing hides future regressions; fail on it.
  { linterOptions: { reportUnusedDisableDirectives: 'error' } },
  { ignores: ['dist/', 'node_modules/', 'coverage/', '.yarn/', 'test-results/', 'playwright-report/'] },
  ...tseslint.configs.recommended,
  {
    files: ['src/**/*.{ts,tsx}', 'e2e/**/*.ts', 'webpack.config.ts'],
    plugins: { 'react-hooks': reactHooks },
    rules: {
      ...reactHooks.configs.recommended.rules,
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
    },
  },
  {
    // Type-aware rules need the tsconfig program (tsconfig.json covers src + e2e;
    // webpack.config.ts stays on the non-type-aware rules above).
    files: ['src/**/*.{ts,tsx}', 'e2e/**/*.ts'],
    languageOptions: { parserOptions: { projectService: true } },
    rules: {
      '@typescript-eslint/no-floating-promises': 'error',
      '@typescript-eslint/no-misused-promises': 'error',
      '@typescript-eslint/await-thenable': 'error',
    },
  },
);
