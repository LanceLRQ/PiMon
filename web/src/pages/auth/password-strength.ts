// 服务端的最小密码长度（auth.MinPasswordLen），按字符计
export const minPasswordLength = 8

export type StrengthLevel = 0 | 1 | 2 | 3 | 4

// 强度 0–4：长度达 10、达 14，字母数字混用，含符号各得一分
export function passwordStrength(pw: string): StrengthLevel {
  const len = Array.from(pw).length
  let s = 0
  if (len >= 10) s++
  if (len >= 14) s++
  if (/[0-9]/.test(pw) && /[a-z]/i.test(pw)) s++
  if (/[^0-9a-z]/i.test(pw)) s++
  return s as StrengthLevel
}
