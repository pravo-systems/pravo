// ESLint for the JavaScript of the viewer and of the site. Run from the root of the
// repository: npm --prefix tools/_weblint run lint
import js from '@eslint/js';
import globals from 'globals';

const rules = {
  'array-callback-return': 'error',
  'default-case-last': 'error',
  'dot-notation': 'error',
  eqeqeq: ['error', 'always', { null: 'ignore' }],
  'no-array-constructor': 'error',
  'no-caller': 'error',
  'no-constructor-return': 'error',
  'no-duplicate-imports': 'error',
  'no-eval': 'error',
  'no-extend-native': 'error',
  'no-extra-bind': 'error',
  'no-implied-eval': 'error',
  'no-iterator': 'error',
  'no-labels': 'error',
  'no-lone-blocks': 'error',
  'no-new-func': 'error',
  'no-new-wrappers': 'error',
  'no-object-constructor': 'error',
  'no-proto': 'error',
  'no-return-assign': 'error',
  'no-script-url': 'error',
  'no-self-compare': 'error',
  'no-sequences': 'error',
  'no-template-curly-in-string': 'error',
  'no-throw-literal': 'error',
  'no-unmodified-loop-condition': 'error',
  'no-unneeded-ternary': 'error',
  'no-unused-expressions': ['error', { allowShortCircuit: true, allowTernary: true }],
  'no-unused-vars': ['error', { args: 'after-used', argsIgnorePattern: '^_', caughtErrors: 'none' }],
  'no-useless-call': 'error',
  'no-useless-computed-key': 'error',
  'no-useless-concat': 'error',
  'no-useless-rename': 'error',
  'no-useless-return': 'error',
  'object-shorthand': ['error', 'always'],
  'prefer-const': ['error', { destructuring: 'all' }],
  'prefer-rest-params': 'error',
  'prefer-spread': 'error',
  radix: 'error',
  'require-atomic-updates': 'off',
  yoda: 'error',
};

export default [
  {
    ignores: [
      '**/node_modules/**',
      // Output of the site build and test inputs.
      'site/public/**',
      'tools/**/testdata/**',
      // Recorded data, not code.
      'internal/serve/web/dev/fixtures/**',
      'site/assets/search-index.js',
    ],
  },
  js.configs.recommended,
  {
    // The viewer: ES modules in the browser.
    files: ['internal/serve/web/assets/**/*.js'],
    languageOptions: { ecmaVersion: 2024, sourceType: 'module', globals: globals.browser },
    rules,
  },
  {
    // The development material of the viewer: a mock of the server for the
    // browser, and programs for Node that check the page.
    files: ['internal/serve/web/dev/**/*.{js,mjs}'],
    languageOptions: { ecmaVersion: 2024, sourceType: 'module', globals: { ...globals.browser, ...globals.node } },
    rules,
  },
  {
    // The site: plain scripts that run in the page, written to run in old browsers
    // as well, so with var and without modules.
    files: ['site/assets/**/*.js'],
    languageOptions: { ecmaVersion: 2020, sourceType: 'script', globals: globals.browser },
    rules: { ...rules, 'prefer-const': 'off', 'object-shorthand': 'off' },
  },
];
