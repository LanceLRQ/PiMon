// 设计 10.7：屏幕端与小组件模板的 CSS 只能引用语义 token
const themedFiles = ['src/screen/**/*.css', 'src/templates/**/*.css']

export default {
  extends: ['stylelint-config-standard'],
  ignoreFiles: ['node_modules/**', '../src/internal/hub/webui/dist/**', 'lint-fixtures/cases/**'],
  rules: {
    // Tailwind 4 与 shadcn 的 at-rule、自定义写法由工程自身使用，基础规则只放行它们
    'at-rule-no-unknown': [true, { ignoreAtRules: ['theme', 'source', 'utility', 'variant', 'custom-variant', 'plugin', 'apply', 'config'] }],
    'import-notation': null,
    'at-rule-prelude-no-invalid': null,
    'custom-property-pattern': null,
    'selector-class-pattern': null,
  },
  overrides: [
    {
      files: themedFiles,
      rules: {
        'color-no-hex': true,
        'function-disallowed-list': ['rgb', 'rgba', 'hsl', 'hsla', 'hwb', 'lab', 'lch', 'oklab', 'oklch', 'color'],
        'declaration-property-value-disallowed-list': { '/.*/': ['/--p-/'] },
        'property-disallowed-list': ['/^--p-/'],
      },
    },
  ],
}
