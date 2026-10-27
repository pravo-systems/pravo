// Stylelint for the style sheets of the viewer and of the site. Run from the root of
// the repository: npm --prefix tools/_weblint run lint
export default {
  extends: ['stylelint-config-standard'],
  rules: {
    // The sheets are written dense on purpose: one rule to a line when it is short,
    // colours in the notation of their tokens, and no blank lines between rules
    // that belong together. These rules only ask for another layout.
    'declaration-block-single-line-max-declarations': null,
    'rule-empty-line-before': null,
    'at-rule-empty-line-before': null,
    'comment-empty-line-before': null,
    'declaration-empty-line-before': null,
    'custom-property-empty-line-before': null,
    'alpha-value-notation': null,
    'color-function-notation': null,
    'color-function-alias-notation': null,
    'media-feature-range-notation': null,
    'value-keyword-case': null,
    // Safari still reads -webkit-mask*, -webkit-user-select and
    // -webkit-backdrop-filter and not the plain names, and the sheets are not run
    // through an autoprefixer.
    'property-no-vendor-prefix': null,
    'value-no-vendor-prefix': null,
    // The cascade of a component sheet follows the source order of its parts, so a
    // rule for the base of a component comes after the rules for its states
    // wherever the markup needs it. Moving them to satisfy this rule changes what
    // wins.
    'no-descending-specificity': null,
    // Later rules of a sheet override the earlier ones for a state, a theme or a
    // width, and several of them sit far from the first: merging them moves
    // declarations across other rules and changes what wins.
    'no-duplicate-selectors': null,
    // The class of a signature mark is made from the verdict of the server, which
    // has underscores in it.
    'selector-class-pattern': ['^([a-z][a-z0-9]*(-[a-z0-9]+)*|sig-(key|role)_[a-z_]+)$', { message: (name) => `Expected class selector "${name}" to be kebab-case` }],
  },
  overrides: [
    {
      // The viewer is square: no rounded corner anywhere. The tokens --rad, --rad-s,
      // --rad-p and --r are 0, and a sheet takes its corners from them.
      files: ['internal/serve/web/**/*.css'],
      rules: {
        'declaration-property-value-allowed-list': {
          '/^border(-[a-z]+)*-radius$/': ['0', '/^var\\(--rad(-[sp])?\\)$/', 'var(--r)', 'inherit', 'unset'],
        },
      },
    },
  ],
};
