import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import tseslint from 'typescript-eslint'

// 设计 10.7：屏幕端与小组件模板只能引用语义 token，不得出现颜色字面量、原始调色板（--p-*）
// 与 Tailwind 任意值色彩。管理界面目录不受此约束。
const themedFiles = ['src/screen/**/*.{ts,tsx}', 'src/templates/**/*.{ts,tsx}']

const colorFunctions = String.raw`\b(rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\(`
const forbidden = [
  { pattern: String.raw`#[0-9a-fA-F]{3,8}\b`, message: '屏幕端与模板目录禁止颜色字面量（hex），请改用语义 token' },
  { pattern: colorFunctions, message: '屏幕端与模板目录禁止颜色函数字面量（rgb/hsl/oklch 等），请改用语义 token' },
  { pattern: '--p-', message: '屏幕端与模板目录禁止引用原始调色板 --p-*，请改用语义 token' },
  {
    pattern: String.raw`\b(bg|text|border|fill|stroke|ring|outline|from|via|to|shadow|accent|caret|decoration|divide)-\[(#|rgb|hsl|hwb|lab|lch|oklab|oklch|color)`,
    message: '禁止 Tailwind 任意值色彩，请改用语义 token 类',
  },
]

const restrictedSyntax = forbidden.flatMap(({ pattern, message }) => [
  { selector: `Literal[value=/${pattern}/]`, message },
  { selector: `TemplateElement[value.raw=/${pattern}/]`, message },
])

export default tseslint.config(
  { ignores: ['node_modules', '../src/internal/hub/webui/dist', 'src/types/*.generated.ts', 'lint-fixtures/cases', 'playwright-report', 'test-results'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ['**/*.{ts,tsx}'],
    languageOptions: { globals: { ...globals.browser, ...globals.node } },
    plugins: { 'react-hooks': reactHooks },
    rules: reactHooks.configs.recommended.rules,
  },
  {
    files: themedFiles,
    rules: { 'no-restricted-syntax': ['error', ...restrictedSyntax] },
  },
)
