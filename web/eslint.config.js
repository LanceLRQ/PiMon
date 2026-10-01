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

// 屏幕端只能用屏幕命名空间颜色类（text-s-ok、bg-s-card 等，定义见 src/themes/screen-colors.css）。
// 管理端映射（text-status-ok、bg-primary 等）在屏幕子树里会静默取到管理端颜色，Tailwind 默认调色板则完全绕开主题。
const colorUtility = String.raw`(bg|text|border|fill|stroke|ring|outline|from|via|to|shadow|accent|caret|decoration|divide)`
const adminColors = [
  'background', 'foreground', 'card(-foreground)?', 'popover(-foreground)?', 'primary(-foreground)?',
  'secondary(-foreground)?', 'muted(-foreground)?', 'accent(-foreground)?', 'destructive', 'border', 'input', 'ring',
  'panel-2', 'ink-2', 'line-strong', 'inv-bg', 'inv-ink', 'signal(-text|-soft)?', 'status-(ok|warn|crit|unknown)',
].join('|')
const paletteColors = String.raw`(red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|slate|gray|zinc|neutral|stone)-\d{2,3}|white|black`
const classStart = String.raw`(^|[\s:!])`
const classEnd = String.raw`(?![\w-])`
forbidden.push(
  {
    pattern: `${classStart}${colorUtility}-(${adminColors})${classEnd}`,
    message: '屏幕端与模板目录禁止管理端颜色类，请改用屏幕命名空间颜色类（text-s-*、bg-s-* 等）',
  },
  {
    pattern: `${classStart}${colorUtility}-(${paletteColors})${classEnd}`,
    message: '屏幕端与模板目录禁止 Tailwind 默认调色板类，请改用屏幕命名空间颜色类（text-s-*、bg-s-* 等）',
  },
)

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
