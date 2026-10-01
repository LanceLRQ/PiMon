// 故意违规的样例：只用于证明 lint 规则生效，不参与构建
export const a = { color: '#ff0000' }
export const b = { color: 'rgb(1, 2, 3)' }
export const c = { color: 'hsl(10 20% 30%)' }
export const d = { color: 'oklch(0.7 0.1 80)' }
export const e = `${1}px solid #abc`
